package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	ri "helm.sh/helm/v4/pkg/release"
	rspb "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

const opTimeout = 5 * time.Minute

type discoKey struct{}

// Service runs Helm actions. Repository configuration follows the Helm CLI (HELM_* env, ~/.config/helm).
type Service struct {
	settings *cli.EnvSettings
	log      *slog.Logger

	regOnce sync.Once
	reg     *registry.Client
	regErr  error
	repoMu  sync.Mutex
}

// New creates the service.
func New(log *slog.Logger) *Service {
	return &Service{settings: cli.New(), log: log}
}

func (s *Service) config(sess *cluster.Session, namespace string) (*action.Configuration, error) {
	// Discovery and the REST mapper are shared by every Helm action of the session.
	d, _ := cluster.Cached(sess, discoKey{}, 0, false, func() (*discoCache, error) { return &discoCache{}, nil })
	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	g := &restGetter{cfg: sess.Config, cc: sess.ClientConfig(namespace), disco: d}
	if err := cfg.Init(g, namespace, "secret"); err != nil {
		return nil, err
	}
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	cfg.RegistryClient = reg
	return cfg, nil
}

func (s *Service) registry() (*registry.Client, error) {
	s.regOnce.Do(func() {
		s.reg, s.regErr = registry.NewClient(
			registry.ClientOptCredentialsFile(s.settings.RegistryConfig),
			registry.ClientOptWriter(io.Discard),
			registry.ClientOptEnableCache(true),
		)
	})
	return s.reg, s.regErr
}

// Release is the list view of a release.
type Release struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	Chart        string    `json:"chart"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Updated      time.Time `json:"updated"`
	Description  string    `json:"description,omitempty"`
	Icon         string    `json:"icon,omitempty"`
}

func summarize(r *rspb.Release) Release {
	out := Release{Name: r.Name, Namespace: r.Namespace, Revision: r.Version}
	if r.Info != nil {
		out.Status, out.Updated, out.Description = string(r.Info.Status), r.Info.LastDeployed, r.Info.Description
	}
	if r.Chart != nil && r.Chart.Metadata != nil {
		m := r.Chart.Metadata
		out.Chart, out.ChartVersion, out.AppVersion, out.Icon = m.Name, m.Version, m.AppVersion, m.Icon
	}
	return out
}

func v1(rel ri.Releaser) (*rspb.Release, error) {
	switch r := rel.(type) {
	case *rspb.Release:
		return r, nil
	case rspb.Release:
		return &r, nil
	case nil:
		return nil, errors.New("release not found")
	default:
		return nil, fmt.Errorf("unsupported release type %T", rel)
	}
}

// List returns the latest revision of every release in namespace ("" = all namespaces).
func (s *Service) List(sess *cluster.Session, namespace string) ([]Release, error) {
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return nil, err
	}
	l := action.NewList(cfg)
	l.AllNamespaces = namespace == ""
	l.All = true
	l.SetStateMask()
	rels, err := l.Run()
	if err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(rels))
	for _, x := range rels {
		r, err := v1(x)
		if err != nil {
			continue
		}
		out = append(out, summarize(r))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Object is one manifest entry of a release.
type Object struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
}

// Detail is the full view of a release.
type Detail struct {
	Release
	Notes    string    `json:"notes"`
	Values   string    `json:"values"`   // user-supplied values (YAML)
	Computed string    `json:"computed"` // chart defaults merged with user values
	Manifest string    `json:"manifest"`
	Objects  []Object  `json:"objects"`
	History  []Release `json:"history"`
	Masked   bool      `json:"masked"`
}

// Get loads a release. Values are only returned with reveal; Secrets in the manifest are masked unless reveal.
func (s *Service) Get(sess *cluster.Session, namespace, name string, reveal bool) (*Detail, error) {
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return nil, err
	}
	x, err := action.NewGet(cfg).Run(name)
	if err != nil {
		return nil, err
	}
	r, err := v1(x)
	if err != nil {
		return nil, err
	}
	d := &Detail{Release: summarize(r), Masked: !reveal}
	// NOTES.txt is rendered from values and sometimes prints passwords: same treatment as values.
	if r.Info != nil && reveal {
		d.Notes = r.Info.Notes
	}
	d.Objects = objects(r.Manifest, r.Namespace, sess)
	d.Manifest = r.Manifest
	if !reveal {
		d.Manifest = MaskManifest(r.Manifest)
	} else {
		if len(r.Config) > 0 {
			b, _ := yaml.Marshal(r.Config)
			d.Values = string(b)
		}
		gv := action.NewGetValues(cfg)
		gv.AllValues = true
		if all, err := gv.Run(name); err == nil {
			b, _ := yaml.Marshal(all)
			d.Computed = string(b)
		}
	}
	h := action.NewHistory(cfg)
	h.Max = 50
	if hist, err := h.Run(name); err == nil {
		for _, hx := range hist {
			if hr, err := v1(hx); err == nil {
				d.History = append(d.History, summarize(hr))
			}
		}
		sort.Slice(d.History, func(i, j int) bool { return d.History[i].Revision > d.History[j].Revision })
	}
	return d, nil
}

// Manifest returns one revision's manifest (masked unless reveal).
func (s *Service) Manifest(sess *cluster.Session, namespace, name string, revision int, reveal bool) (string, error) {
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return "", err
	}
	g := action.NewGet(cfg)
	g.Version = revision
	x, err := g.Run(name)
	if err != nil {
		return "", err
	}
	r, err := v1(x)
	if err != nil {
		return "", err
	}
	if reveal {
		return r.Manifest, nil
	}
	return MaskManifest(r.Manifest), nil
}

// Result reports the outcome (or preview) of a mutating action.
type Result struct {
	DryRun   bool   `json:"dryRun"`
	Revision int    `json:"revision"`
	Status   string `json:"status"`
	Previous string `json:"previous"` // manifest before (masked)
	Manifest string `json:"manifest"` // manifest after (masked)
}

// UpgradeRequest describes an upgrade.
type UpgradeRequest struct {
	// Chart is empty to reuse the chart stored in the release, or a repo/chart, oci:// or URL reference.
	Chart   string `json:"chart,omitempty"`
	Version string `json:"version,omitempty"`
	// Values replaces the user-supplied values when ReplaceValues is set; otherwise the current values are kept.
	Values        string `json:"values,omitempty"`
	ReplaceValues bool   `json:"replaceValues"`
	DryRun        bool   `json:"dryRun"`
}

func parseValues(text string) (map[string]any, error) {
	vals := map[string]any{}
	if strings.TrimSpace(text) == "" {
		return vals, nil
	}
	if err := yaml.Unmarshal([]byte(text), &vals); err != nil {
		return nil, fmt.Errorf("invalid values YAML: %w", err)
	}
	return vals, nil
}

func (s *Service) loadChart(ref, version string, opts *action.ChartPathOptions, allowLocal bool) (chart.Charter, error) {
	if err := ValidateChartRef(ref, allowLocal); err != nil {
		return nil, err
	}
	opts.Version = version
	path, err := opts.LocateChart(ref, s.settings)
	if err != nil {
		return nil, err
	}
	return loader.Load(path)
}

// Upgrade upgrades a release; with DryRun the API server validates the rendered manifest without persisting.
func (s *Service) Upgrade(ctx context.Context, sess *cluster.Session, namespace, name string, req UpgradeRequest) (*Result, error) {
	if err := sess.CheckWritable(); err != nil {
		return nil, err
	}
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return nil, err
	}
	cur, err := action.NewGet(cfg).Run(name)
	if err != nil {
		return nil, err
	}
	current, err := v1(cur)
	if err != nil {
		return nil, err
	}
	u := action.NewUpgrade(cfg)
	u.Namespace = namespace
	u.WaitStrategy = kube.HookOnlyStrategy
	u.Timeout = opTimeout
	if req.DryRun {
		u.DryRunStrategy = action.DryRunServer
	}
	var vals map[string]any
	if req.ReplaceValues {
		if vals, err = parseValues(req.Values); err != nil {
			return nil, err
		}
		u.ResetValues = true
	} else {
		vals = map[string]any{}
		u.ReuseValues = true
	}
	var res ri.Releaser
	if req.Chart == "" {
		if current.Chart == nil {
			return nil, errors.New("release has no stored chart; choose a chart to upgrade")
		}
		res, err = u.RunWithContext(ctx, name, current.Chart, vals)
	} else {
		u.SetRegistryClient(cfg.RegistryClient)
		ch, lerr := s.loadChart(req.Chart, req.Version, &u.ChartPathOptions, true)
		if lerr != nil {
			return nil, lerr
		}
		res, err = u.RunWithContext(ctx, name, ch, vals)
	}
	if err != nil {
		return nil, err
	}
	r, err := v1(res)
	if err != nil {
		return nil, err
	}
	out := &Result{DryRun: req.DryRun, Revision: r.Version, Previous: MaskManifest(current.Manifest), Manifest: MaskManifest(r.Manifest)}
	if r.Info != nil {
		out.Status = string(r.Info.Status)
	}
	return out, nil
}

// Rollback rolls a release back to revision. DryRun only returns the manifest difference.
func (s *Service) Rollback(sess *cluster.Session, namespace, name string, revision int, dryRun bool) (*Result, error) {
	if err := sess.CheckWritable(); err != nil {
		return nil, err
	}
	if revision < 1 {
		return nil, errors.New("revision must be >= 1")
	}
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return nil, err
	}
	cur, err := action.NewGet(cfg).Run(name)
	if err != nil {
		return nil, err
	}
	current, err := v1(cur)
	if err != nil {
		return nil, err
	}
	g := action.NewGet(cfg)
	g.Version = revision
	tx, err := g.Run(name)
	if err != nil {
		return nil, err
	}
	target, err := v1(tx)
	if err != nil {
		return nil, err
	}
	out := &Result{DryRun: dryRun, Revision: current.Version + 1, Previous: MaskManifest(current.Manifest), Manifest: MaskManifest(target.Manifest), Status: "pending-rollback"}
	if dryRun {
		return out, nil
	}
	rb := action.NewRollback(cfg)
	rb.Version = revision
	rb.WaitStrategy = kube.HookOnlyStrategy
	rb.Timeout = opTimeout
	if err := rb.Run(name); err != nil {
		return nil, err
	}
	if after, err := action.NewGet(cfg).Run(name); err == nil {
		if r, err := v1(after); err == nil {
			out.Revision = r.Version
			if r.Info != nil {
				out.Status = string(r.Info.Status)
			}
		}
	}
	return out, nil
}

// Uninstall removes a release. DryRun lists what would be deleted.
func (s *Service) Uninstall(sess *cluster.Session, namespace, name string, dryRun bool) (*Result, error) {
	if err := sess.CheckWritable(); err != nil {
		return nil, err
	}
	cfg, err := s.config(sess, namespace)
	if err != nil {
		return nil, err
	}
	cur, err := action.NewGet(cfg).Run(name)
	if err != nil {
		return nil, err
	}
	current, err := v1(cur)
	if err != nil {
		return nil, err
	}
	out := &Result{DryRun: dryRun, Revision: current.Version, Previous: MaskManifest(current.Manifest), Status: "uninstalled"}
	if dryRun {
		return out, nil
	}
	un := action.NewUninstall(cfg)
	un.WaitStrategy = kube.HookOnlyStrategy
	un.Timeout = opTimeout
	if _, err := un.Run(name); err != nil {
		return nil, err
	}
	return out, nil
}

// InstallRequest describes a new release.
type InstallRequest struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Chart           string `json:"chart"` // repo/chart, oci://…, https://….tgz or an absolute local path
	Version         string `json:"version,omitempty"`
	Values          string `json:"values,omitempty"`
	CreateNamespace bool   `json:"createNamespace"`
	DryRun          bool   `json:"dryRun"`
}

// Install installs a chart.
func (s *Service) Install(ctx context.Context, sess *cluster.Session, req InstallRequest) (*Result, error) {
	if err := sess.CheckWritable(); err != nil {
		return nil, err
	}
	if req.Name == "" || req.Namespace == "" || req.Chart == "" {
		return nil, errors.New("name, namespace and chart are required")
	}
	vals, err := parseValues(req.Values)
	if err != nil {
		return nil, err
	}
	cfg, err := s.config(sess, req.Namespace)
	if err != nil {
		return nil, err
	}
	in := action.NewInstall(cfg)
	in.Namespace = req.Namespace
	in.ReleaseName = req.Name
	in.CreateNamespace = req.CreateNamespace
	in.WaitStrategy = kube.HookOnlyStrategy
	in.Timeout = opTimeout
	if req.DryRun {
		in.DryRunStrategy = action.DryRunServer
	}
	in.SetRegistryClient(cfg.RegistryClient)
	ch, err := s.loadChart(req.Chart, req.Version, &in.ChartPathOptions, true)
	if err != nil {
		return nil, err
	}
	res, err := in.RunWithContext(ctx, ch, vals)
	if err != nil {
		return nil, err
	}
	r, err := v1(res)
	if err != nil {
		return nil, err
	}
	out := &Result{DryRun: req.DryRun, Revision: r.Version, Manifest: MaskManifest(r.Manifest)}
	if r.Info != nil {
		out.Status = string(r.Info.Status)
	}
	return out, nil
}

// IsNotFound reports a missing release or revision.
func IsNotFound(err error) bool { return errors.Is(err, driver.ErrReleaseNotFound) }
