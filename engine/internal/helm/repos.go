package helm

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/helmpath"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// Repo is a configured chart repository (credentials are never returned).
type Repo struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Charts  int    `json:"charts"`
	Indexed bool   `json:"indexed"`
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

func (s *Service) repoFile() (*repo.File, error) {
	f, err := repo.LoadFile(s.settings.RepositoryConfig)
	if errors.Is(err, fs.ErrNotExist) {
		return repo.NewFile(), nil
	}
	return f, err
}

func (s *Service) indexPath(name string) string {
	return filepath.Join(s.settings.RepositoryCache, helmpath.CacheIndexFile(name))
}

// Repos lists configured repositories.
func (s *Service) Repos() ([]Repo, error) {
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	f, err := s.repoFile()
	if err != nil {
		return nil, err
	}
	out := make([]Repo, 0, len(f.Repositories))
	for _, e := range f.Repositories {
		r := Repo{Name: e.Name, URL: redact(e.URL)}
		if idx, err := repo.LoadIndexFile(s.indexPath(e.Name)); err == nil {
			r.Indexed, r.Charts = true, len(idx.Entries)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Service) download(e *repo.Entry) error {
	cr, err := repo.NewChartRepository(e, getter.All(s.settings, getter.WithTimeout(60*time.Second)))
	if err != nil {
		return err
	}
	cr.CachePath = s.settings.RepositoryCache
	_, err = cr.DownloadIndexFile()
	return err
}

// redact drops credentials and query strings (tokens) from a URL shown to the UI or logs.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	u.User, u.RawQuery = nil, ""
	return u.String()
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// validateRemote accepts https anywhere and http only on loopback, without embedded credentials.
func validateRemote(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("invalid URL")
	}
	if u.User != nil {
		return errors.New("credentials in URLs are not supported")
	}
	switch {
	case u.Scheme == "https", u.Scheme == "oci":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return errors.New("use https:// (plain http only for localhost)")
	}
	return nil
}

var repoChart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}/[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidateChartRef allows repo/chart, oci:// and https:// references. Local charts (an absolute path to a
// .tgz or to a directory with Chart.yaml) are only accepted where allowLocal is set (install/upgrade).
func ValidateChartRef(ref string, allowLocal bool) error {
	switch {
	case repoChart.MatchString(ref):
		return nil
	case strings.Contains(ref, "://"):
		return validateRemote(ref)
	case allowLocal && filepath.IsAbs(ref):
		clean := filepath.Clean(ref)
		if st, err := os.Stat(clean); err == nil && st.IsDir() {
			if _, err := os.Stat(filepath.Join(clean, "Chart.yaml")); err == nil {
				return nil
			}
		} else if err == nil && strings.HasSuffix(clean, ".tgz") {
			return nil
		}
		return errors.New("local chart must be a .tgz file or a directory containing Chart.yaml")
	}
	return errors.New("chart must be repo/chart, oci://… or https://…")
}

// AddRepo validates the repository by downloading its index, then saves it to the Helm config.
func (s *Service) AddRepo(name, rawURL string) error {
	if !repoName.MatchString(name) {
		return errors.New("invalid repository name")
	}
	if err := validateRemote(rawURL); err != nil {
		return err
	}
	s.repoMu.Lock()
	f, err := s.repoFile()
	exists := err == nil && f.Has(name)
	s.repoMu.Unlock()
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("repository %q already exists", name)
	}
	e := &repo.Entry{Name: name, URL: strings.TrimRight(rawURL, "/")}
	// Network I/O happens outside the lock so a slow repository does not freeze search and listing.
	if err := s.download(e); err != nil {
		return fmt.Errorf("cannot read %s/index.yaml: %w", e.URL, err)
	}
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	if f, err = s.repoFile(); err != nil {
		return err
	}
	if f.Has(name) {
		return fmt.Errorf("repository %q already exists", name)
	}
	f.Update(e)
	if err := os.MkdirAll(filepath.Dir(s.settings.RepositoryConfig), 0o700); err != nil {
		return err
	}
	return f.WriteFile(s.settings.RepositoryConfig, 0o600)
}

// RemoveRepo deletes a repository and its cached index.
func (s *Service) RemoveRepo(name string) error {
	s.repoMu.Lock()
	defer s.repoMu.Unlock()
	f, err := s.repoFile()
	if err != nil {
		return err
	}
	if !f.Remove(name) {
		return fmt.Errorf("repository %q not found", name)
	}
	if err := f.WriteFile(s.settings.RepositoryConfig, 0o600); err != nil {
		return err
	}
	_ = os.Remove(s.indexPath(name))
	_ = os.Remove(filepath.Join(s.settings.RepositoryCache, helmpath.CacheChartsFile(name)))
	return nil
}

// UpdateRepos refreshes the index of every repository (or only names). It returns per-repo errors.
func (s *Service) UpdateRepos(names []string) map[string]string {
	errs := map[string]string{}
	s.repoMu.Lock()
	f, err := s.repoFile()
	s.repoMu.Unlock()
	if err != nil {
		errs["*"] = err.Error()
		return errs
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for _, e := range f.Repositories {
		if len(want) > 0 && !want[e.Name] {
			continue
		}
		if err := s.download(e); err != nil {
			errs[e.Name] = err.Error()
		}
	}
	return errs
}

// ChartHit is a search result (latest version of a chart in a repository).
type ChartHit struct {
	Repo        string   `json:"repo"`
	Name        string   `json:"name"`
	Ref         string   `json:"ref"`
	Version     string   `json:"version"`
	AppVersion  string   `json:"appVersion"`
	Description string   `json:"description"`
	Icon        string   `json:"icon,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
	Versions    []string `json:"versions,omitempty"`
}

// Search finds charts in the cached repository indexes by name, description or keyword.
func (s *Service) Search(q string, limit int) ([]ChartHit, error) {
	s.repoMu.Lock()
	f, err := s.repoFile()
	s.repoMu.Unlock()
	if err != nil {
		return nil, err
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var out []ChartHit
	for _, e := range f.Repositories {
		idx, err := repo.LoadIndexFile(s.indexPath(e.Name))
		if err != nil {
			continue
		}
		for name, versions := range idx.Entries {
			if len(versions) == 0 {
				continue
			}
			latest := versions[0]
			hay := strings.ToLower(e.Name + "/" + name + " " + latest.Description + " " + strings.Join(latest.Keywords, " "))
			if q != "" && !strings.Contains(hay, q) {
				continue
			}
			h := ChartHit{Repo: e.Name, Name: name, Ref: e.Name + "/" + name, Version: latest.Version, AppVersion: latest.AppVersion,
				Description: latest.Description, Icon: latest.Icon, Deprecated: latest.Deprecated}
			for i, v := range versions {
				if i >= 30 {
					break
				}
				h.Versions = append(h.Versions, v.Version)
			}
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		// Exact name matches first, then alphabetical.
		ei, ej := out[i].Name == q, out[j].Name == q
		if ei != ej {
			return ei
		}
		return out[i].Ref < out[j].Ref
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ChartInfo is what "show chart" returns.
type ChartInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	AppVersion  string `json:"appVersion"`
	Description string `json:"description"`
	Home        string `json:"home,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Readme      string `json:"readme"`
	Values      string `json:"values"`
}

// Show downloads (or reads from cache) a chart and returns its README and default values.
func (s *Service) Show(ref, version string) (*ChartInfo, error) {
	reg, err := s.registry()
	if err != nil {
		return nil, err
	}
	// ChartPathOptions only exposes its registry client through an action.
	in := action.NewInstall(action.NewConfiguration())
	in.SetRegistryClient(reg)
	in.Version = version
	if err := ValidateChartRef(ref, false); err != nil {
		return nil, err
	}
	path, err := in.LocateChart(ref, s.settings)
	if err != nil {
		return nil, err
	}
	ch, err := loader.Load(path)
	if err != nil {
		return nil, err
	}
	return chartInfo(ch), nil
}

func chartInfo(ch *chartv2.Chart) *ChartInfo {
	info := &ChartInfo{}
	if m := ch.Metadata; m != nil {
		info.Name, info.Version, info.AppVersion, info.Description, info.Home, info.Icon = m.Name, m.Version, m.AppVersion, m.Description, m.Home, m.Icon
	}
	for _, f := range ch.Raw {
		switch strings.ToLower(f.Name) {
		case "values.yaml":
			info.Values = string(f.Data)
		case "readme.md", "readme.txt", "readme":
			info.Readme = string(f.Data)
		}
	}
	return info
}
