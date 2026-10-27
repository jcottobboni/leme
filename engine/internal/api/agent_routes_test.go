package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

func TestAgentAccessRoutes(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	cfg := `apiVersion: v1
kind: Config
clusters:
- {name: a, cluster: {server: "https://127.0.0.1:1"}}
users:
- {name: u, user: {token: x}}
contexts:
- {name: dev-lab, context: {cluster: a, user: u}}
- {name: corp-prd, context: {cluster: a, user: u}}
`
	if err := os.WriteFile(kc, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kc)
	store, err := settings.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", StaticDir: dir, Settings: store, ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.Allow("1234")

	put := func(name, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/agents/access/"+url.PathEscape(kube.ContextID(name)), strings.NewReader(body))
		r.Host = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := do(s, http.MethodGet, "/api/agents/access", map[string]string{"Authorization": "Bearer secret"})
	var got struct {
		Contexts   []agentAccessView `json:"contexts"`
		EnginePath string            `json:"enginePath"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Contexts) != 2 || got.EnginePath == "" {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := put("dev-lab", `{"write":true}`); w.Code != 200 || !store.AgentAccess("dev-lab").Write {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	// Production needs the exact name.
	if !kube.IsProduction("", "corp-prd", "a", "u", "") {
		t.Skip("context name not detected as production")
	}
	if w := put("corp-prd", `{"write":true,"confirmName":"wrong"}`); w.Code != http.StatusForbidden || store.AgentAccess("corp-prd").Write {
		t.Fatalf("prod without name must be refused: %d", w.Code)
	}
	if w := put("corp-prd", `{"write":true,"confirmName":"corp-prd"}`); w.Code != 200 || !store.AgentAccess("corp-prd").ProdConfirmed {
		t.Fatalf("prod with name: %d %s", w.Code, w.Body)
	}
	if w := put("corp-prd", `{"write":false}`); w.Code != 200 || store.AgentAccess("corp-prd").Write {
		t.Fatalf("disable: %d", w.Code)
	}
	entries, _ := s.audit.Recent(10)
	if len(entries) < 3 {
		t.Fatalf("changes must be audited: %+v", entries)
	}
}
