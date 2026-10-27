// Package flux reads Flux CD objects (Kustomizations, HelmReleases and their sources) through the
// cluster's own API, with the user's kubeconfig, and builds the only changes Leme may propose on
// them: a reconcile request (the reconcile.fluxcd.io/requestedAt annotation) and suspend/resume
// (spec.suspend). The patches are built here from the action name and the current object; they
// never come from the UI, an AI or an agent.
package flux

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// API groups of the Flux toolkit.
const (
	KustomizeGroup = "kustomize.toolkit.fluxcd.io"
	HelmGroup      = "helm.toolkit.fluxcd.io"
	SourceGroup    = "source.toolkit.fluxcd.io"
)

// ReconcileAnnotation asks a Flux controller to reconcile the object now (what `flux reconcile` sets).
const ReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

// Hint explains what is missing when no Flux CRD is installed.
const Hint = "Os CRDs do Flux (kustomize.toolkit.fluxcd.io, helm.toolkit.fluxcd.io e source.toolkit.fluxcd.io) não existem neste cluster. " +
	"Instale o Flux no cluster (flux install ou flux bootstrap) para ver as Kustomizations, HelmReleases e fontes aqui. " +
	"O Leme lê os objetos do Flux pela API do Kubernetes, com o seu kubeconfig: não precisa do CLI flux."

// Errors of the actions.
var (
	ErrSuspended    = errors.New("o objeto está suspenso: retome (resume) antes de pedir reconcile")
	ErrAlreadySusp  = errors.New("o objeto já está suspenso")
	ErrNotSuspended = errors.New("o objeto não está suspenso")
	ErrUnknownKind  = errors.New("tipo do Flux desconhecido")
)

// kindGroups are the kinds this integration lists and may change, with their group.
var kindGroups = map[string]string{
	"Kustomization": KustomizeGroup, "HelmRelease": HelmGroup,
	"GitRepository": SourceGroup, "OCIRepository": SourceGroup, "HelmRepository": SourceGroup, "Bucket": SourceGroup,
}

// SourceKinds are the source kinds listed, in display order.
var SourceKinds = []string{"GitRepository", "OCIRepository", "HelmRepository", "Bucket"}

// GroupOf returns the group of a Flux kind handled here.
func GroupOf(kind string) (string, bool) {
	g, ok := kindGroups[kind]
	return g, ok
}

// Allow is the change policy for this feature: only the Flux kinds above, in their own group.
func Allow(group, kind string) error {
	if g, ok := kindGroups[kind]; ok && g == group {
		return nil
	}
	return fmt.Errorf("the Flux integration only changes Flux Kustomizations, HelmReleases and sources, not %s/%s", group, kind)
}

// Ref is a reference to another Flux object (sourceRef, chartRef).
type Ref struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Condition is a status condition.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// Object is the summary shared by Kustomizations, HelmReleases and sources.
type Object struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Ready is the Ready condition status: True, False or Unknown.
	Ready       string      `json:"ready"`
	Reason      string      `json:"reason,omitempty"`
	Message     string      `json:"message,omitempty"`
	Suspended   bool        `json:"suspended"`
	Reconciling bool        `json:"reconciling,omitempty"`
	Interval    string      `json:"interval,omitempty"`
	Conditions  []Condition `json:"conditions"`
	// Revision is the applied revision (Kustomization, HelmRelease) or the artifact revision (source).
	Revision string `json:"revision,omitempty"`
	// Attempted is the last attempted revision when it differs from the applied one.
	Attempted string `json:"attempted,omitempty"`
	// Source is where a Kustomization or HelmRelease gets its manifests or chart.
	Source *Ref `json:"source,omitempty"`
	// Path (Kustomization), chart and version (HelmRelease), URL and ref (sources).
	Path    string `json:"path,omitempty"`
	Chart   string `json:"chart,omitempty"`
	URL     string `json:"url,omitempty"`
	RefName string `json:"ref,omitempty"`
	// TargetNamespace is where a Kustomization or HelmRelease applies, when set.
	TargetNamespace string `json:"targetNamespace,omitempty"`
	// RequestedAt is the reconcile request annotation; HandledAt is the last one the controller handled.
	RequestedAt string `json:"requestedAt,omitempty"`
	HandledAt   string `json:"handledAt,omitempty"`
	// UpdatedAt is when the Ready condition (or the artifact) last changed.
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// Overview is the list response.
type Overview struct {
	Installed      bool              `json:"installed"`
	Groups         map[string]bool   `json:"groups"`
	Kustomizations []Object          `json:"kustomizations"`
	HelmReleases   []Object          `json:"helmReleases"`
	Sources        []Object          `json:"sources"`
	Hint           string            `json:"hint,omitempty"`
	Errors         map[string]string `json:"errors,omitempty"`
}

// Detect reports which Flux groups the cluster serves.
func Detect(s *cluster.Session) map[string]bool {
	out := map[string]bool{KustomizeGroup: false, HelmGroup: false, SourceGroup: false}
	for kind, g := range kindGroups {
		if _, ok := s.LookupKind(g, kind); ok {
			out[g] = true
		}
	}
	return out
}

// Installed reports whether any Flux CRD this integration uses is served.
func Installed(s *cluster.Session) bool {
	for _, ok := range Detect(s) {
		if ok {
			return true
		}
	}
	return false
}

// List returns the Flux objects of a namespace ("" = all). A kind that cannot be listed (RBAC) is
// reported in Errors and does not hide the others.
func List(ctx context.Context, s *cluster.Session, ns string) (*Overview, error) {
	groups := Detect(s)
	out := &Overview{Groups: groups, Kustomizations: []Object{}, HelmReleases: []Object{}, Sources: []Object{}}
	for _, ok := range groups {
		out.Installed = out.Installed || ok
	}
	if !out.Installed {
		out.Hint = Hint
		return out, nil
	}
	kinds := append([]string{"Kustomization", "HelmRelease"}, SourceKinds...)
	results := make([][]Object, len(kinds))
	errs := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, kind := range kinds {
		res, ok := s.LookupKind(kindGroups[kind], kind)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, kind string, res cluster.Resource) {
			defer wg.Done()
			l, err := s.Dynamic.Resource(res.GVR()).Namespace(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				mu.Lock()
				errs[strings.ToLower(res.Resource)+"."+res.Group] = err.Error()
				mu.Unlock()
				return
			}
			objs := make([]Object, 0, len(l.Items))
			for j := range l.Items {
				objs = append(objs, Parse(&l.Items[j]))
			}
			results[i] = objs
		}(i, kind, res)
	}
	wg.Wait()
	out.Kustomizations = append(out.Kustomizations, results[0]...)
	out.HelmReleases = append(out.HelmReleases, results[1]...)
	for _, r := range results[2:] {
		out.Sources = append(out.Sources, r...)
	}
	for _, l := range [][]Object{out.Kustomizations, out.HelmReleases, out.Sources} {
		sortObjects(l)
	}
	if len(errs) > 0 {
		out.Errors = errs
	}
	return out, nil
}

func sortObjects(l []Object) {
	sort.Slice(l, func(i, j int) bool {
		a, b := l[i], l[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
}

// Get returns a Flux object of a handled kind.
func Get(ctx context.Context, s *cluster.Session, kind, ns, name string) (*unstructured.Unstructured, error) {
	g, ok := kindGroups[kind]
	if !ok {
		return nil, ErrUnknownKind
	}
	res, ok := s.LookupKind(g, kind)
	if !ok {
		return nil, errors.New(Hint)
	}
	return s.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
}

// Parse reads the summary of any handled Flux object.
func Parse(u *unstructured.Unstructured) Object {
	o := u.Object
	gv := strings.SplitN(u.GetAPIVersion(), "/", 2)
	ob := Object{Group: gv[0], Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName(), Ready: "Unknown", Conditions: []Condition{},
		Interval: str(o, "spec", "interval"), RequestedAt: u.GetAnnotations()[ReconcileAnnotation], HandledAt: str(o, "status", "lastHandledReconcileAt")}
	ob.Suspended, _, _ = unstructured.NestedBool(o, "spec", "suspend")
	for _, m := range maps(o, "status", "conditions") {
		c := Condition{Type: str(m, "type"), Status: str(m, "status"), Reason: str(m, "reason"), Message: str(m, "message"), LastTransitionTime: str(m, "lastTransitionTime")}
		ob.Conditions = append(ob.Conditions, c)
		switch c.Type {
		case "Ready":
			ob.Ready, ob.Reason, ob.Message, ob.UpdatedAt = orUnknown(c.Status), c.Reason, c.Message, c.LastTransitionTime
		case "Reconciling":
			ob.Reconciling = c.Status == "True"
		}
	}
	switch u.GetKind() {
	case "Kustomization":
		ob.Revision, ob.Attempted = str(o, "status", "lastAppliedRevision"), str(o, "status", "lastAttemptedRevision")
		ob.Path, ob.TargetNamespace = str(o, "spec", "path"), str(o, "spec", "targetNamespace")
		ob.Source = sourceRef(mapOr(o, "spec", "sourceRef"), u.GetNamespace())
	case "HelmRelease":
		ob.TargetNamespace = str(o, "spec", "targetNamespace")
		if m, ok := mapAt(o, "spec", "chart", "spec"); ok {
			ob.Chart = str(m, "chart")
			if v := str(m, "version"); v != "" {
				ob.Chart += "@" + v
			}
			ob.Source = sourceRef(mapOr(m, "sourceRef"), u.GetNamespace())
		} else if m, ok := mapAt(o, "spec", "chartRef"); ok {
			ob.Source = sourceRef(m, u.GetNamespace())
		}
		// The newest history entry is the release Helm installed last.
		if h := maps(o, "status", "history"); len(h) > 0 {
			ob.Revision = str(h[0], "chartVersion")
			if n := str(h[0], "chartName"); n != "" && ob.Revision != "" {
				ob.Revision = n + "@" + ob.Revision
			}
			if st := str(h[0], "status"); st != "" && st != "deployed" {
				ob.Revision += " (" + st + ")"
			}
		}
		if ob.Revision == "" {
			ob.Revision = str(o, "status", "lastAppliedRevision")
		}
		ob.Attempted = str(o, "status", "lastAttemptedRevision")
	default: // sources
		ob.Revision = str(o, "status", "artifact", "revision")
		if t := str(o, "status", "artifact", "lastUpdateTime"); t != "" && ob.UpdatedAt == "" {
			ob.UpdatedAt = t
		}
		ob.URL = str(o, "spec", "url")
		if u.GetKind() == "Bucket" {
			ob.URL = strings.TrimSuffix(str(o, "spec", "endpoint"), "/") + "/" + str(o, "spec", "bucketName")
		}
		ob.RefName = refName(mapOr(o, "spec", "ref"))
	}
	if ob.Attempted == ob.Revision {
		ob.Attempted = ""
	}
	return ob
}

func sourceRef(m map[string]any, ns string) *Ref {
	if m == nil || str(m, "name") == "" {
		return nil
	}
	r := &Ref{Kind: str(m, "kind"), Name: str(m, "name"), Namespace: str(m, "namespace")}
	if r.Namespace == "" {
		r.Namespace = ns
	}
	return r
}

// refName renders a Git or OCI reference (branch, tag, semver, commit or digest).
func refName(m map[string]any) string {
	for _, k := range []string{"commit", "digest", "tag", "semver", "name", "branch"} {
		if v := str(m, k); v != "" {
			if k == "branch" || k == "tag" || k == "name" {
				return v
			}
			return k + " " + v
		}
	}
	return ""
}

// ReconcilePatch builds the merge patch that asks the controller to reconcile now. A suspended
// object is refused: the controller ignores requests until it is resumed.
func ReconcilePatch(u *unstructured.Unstructured, now time.Time) (map[string]any, error) {
	if s, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); s {
		return nil, ErrSuspended
	}
	return annotation(now), nil
}

// SuspendPatch sets spec.suspend: the controller stops reconciling the object.
func SuspendPatch(u *unstructured.Unstructured) (map[string]any, error) {
	if s, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); s {
		return nil, ErrAlreadySusp
	}
	return map[string]any{"spec": map[string]any{"suspend": true}}, nil
}

// ResumePatch removes spec.suspend and requests a reconcile, as `flux resume` does.
func ResumePatch(u *unstructured.Unstructured, now time.Time) (map[string]any, error) {
	if s, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend"); !s {
		return nil, ErrNotSuspended
	}
	p := annotation(now)
	p["spec"] = map[string]any{"suspend": nil}
	return p, nil
}

func annotation(now time.Time) map[string]any {
	return map[string]any{"metadata": map[string]any{"annotations": map[string]any{ReconcileAnnotation: now.UTC().Format(time.RFC3339)}}}
}

// --- unstructured helpers ---

func str(o map[string]any, path ...string) string {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func mapAt(o map[string]any, path ...string) (map[string]any, bool) {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func mapOr(o map[string]any, path ...string) map[string]any {
	m, _ := mapAt(o, path...)
	return m
}

func maps(o map[string]any, path ...string) []map[string]any {
	v, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	if !ok {
		return nil
	}
	l, _ := v.([]any)
	out := make([]map[string]any, 0, len(l))
	for _, it := range l {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "Unknown"
	}
	return s
}
