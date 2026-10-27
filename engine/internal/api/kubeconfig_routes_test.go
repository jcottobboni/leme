package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

func TestKubeconfigRoutes(t *testing.T) {
	dir := t.TempDir()
	cfg := func(ctx string) []byte {
		return []byte("apiVersion: v1\nkind: Config\nclusters:\n- {name: c, cluster: {server: \"https://10.0.0.1:6443\"}}\n" +
			"users:\n- {name: u, user: {token: x}}\ncontexts:\n- {name: " + ctx + ", context: {cluster: c, user: u}}\n")
	}
	base := filepath.Join(dir, "config")
	clients := filepath.Join(dir, "clients")
	if err := os.MkdirAll(clients, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{base: cfg("base"), filepath.Join(clients, "acme.yaml"): cfg("acme-stg")} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("KUBECONFIG", base)
	store, err := settings.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", StaticDir: dir, Settings: store, ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.Allow("1234")

	put := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/kubeconfigs", strings.NewReader(body))
		r.Host = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	contexts := func() int {
		w := do(s, http.MethodGet, "/api/clusters", map[string]string{"Authorization": "Bearer secret"})
		var got struct {
			Clusters []clusterView `json:"clusters"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatalf("clusters: %d %s", w.Code, w.Body)
		}
		return len(got.Clusters)
	}

	if n := contexts(); n != 1 {
		t.Fatalf("before: %d contexts", n)
	}
	// Bodies are built with json.Marshal: Windows paths carry backslashes.
	body := func(paths ...string) string {
		b, _ := json.Marshal(map[string][]string{"paths": paths})
		return string(b)
	}
	for _, bad := range []string{body("relative/dir"), body(filepath.Join(dir, "gone")), `not json`} {
		if w := put(bad); w.Code != http.StatusBadRequest {
			t.Fatalf("%s must be refused: %d", bad, w.Code)
		}
	}
	w := put(body(clients, clients+string(filepath.Separator), " "))
	var view struct {
		Base    []string           `json:"base"`
		Sources []kubeconfigSource `json:"sources"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if len(view.Sources) != 1 || view.Sources[0].Kind != "dir" || len(view.Sources[0].Files) != 1 || len(view.Base) != 1 {
		t.Fatalf("view: %+v", view)
	}
	if n := contexts(); n != 2 {
		t.Fatalf("after: %d contexts", n)
	}
	if got := store.Kubeconfigs(); len(got) != 1 || got[0] != clients {
		t.Fatalf("not persisted: %v", got)
	}
	// A new engine picks the saved sources up from settings.json.
	reopened, _ := settings.Open(dir)
	if got := reopened.Kubeconfigs(); len(got) != 1 {
		t.Fatalf("not saved to disk: %v", got)
	}
	if w := put(`{"paths":[]}`); w.Code != 200 || contexts() != 1 {
		t.Fatalf("clear: %d", w.Code)
	}
}
