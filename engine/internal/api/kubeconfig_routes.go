package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jcottobboni/leme/engine/internal/kube"
)

// kubeconfigRoutes lets the user add kubeconfig files or folders in the app instead of setting
// $KUBECONFIG or passing --kubeconfig. Only paths are stored (settings.json); the files are read
// on every reload, like ~/.kube/config.
func (s *Server) kubeconfigRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/kubeconfigs", s.kubeconfigs)
	m.HandleFunc("PUT /api/kubeconfigs", s.setKubeconfigs)
}

// maxKubeconfigSources keeps the list to something a person manages by hand.
const maxKubeconfigSources = 50

type kubeconfigSource struct {
	Path  string   `json:"path"`
	Kind  string   `json:"kind"` // file | dir | missing
	Files []string `json:"files"`
}

func (s *Server) kubeconfigView() map[string]any {
	l := s.mgr.Loader()
	sources := []kubeconfigSource{}
	for _, p := range l.Sources() {
		src := kubeconfigSource{Path: p, Kind: "missing", Files: kube.ExpandSource(p)}
		if fi, err := os.Stat(p); err == nil {
			src.Kind = map[bool]string{true: "dir", false: "file"}[fi.IsDir()]
		}
		if src.Files == nil {
			src.Files = []string{}
		}
		sources = append(sources, src)
	}
	return map[string]any{"base": l.Base(), "sources": sources}
}

func (s *Server) kubeconfigs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.kubeconfigView())
}

func (s *Server) setKubeconfigs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid body")
		return
	}
	paths, err := cleanKubeconfigPaths(body.Paths)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.settings != nil {
		if err := s.settings.SetKubeconfigs(paths); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.mgr.Loader().SetSources(paths)
	s.log.Info("kubeconfig sources changed", "count", len(paths))
	writeJSON(w, s.kubeconfigView())
}

// cleanKubeconfigPaths expands ~, requires absolute paths that exist and drops duplicates.
// A path that later disappears stays in the list and shows as missing.
func cleanKubeconfigPaths(in []string) ([]string, error) {
	if len(in) > maxKubeconfigSources {
		return nil, fmt.Errorf("at most %d kubeconfig paths", maxKubeconfigSources)
	}
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	out := []string{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if home != "" && (p == "~" || strings.HasPrefix(p, "~/")) {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("use an absolute path: %s", p)
		}
		p = filepath.Clean(p)
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("not found: %s", p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}
