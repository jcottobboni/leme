package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(dir, "kubeconfig")
	_ = os.WriteFile(kc, []byte("apiVersion: v1\nkind: Config\ncontexts: []\n"), 0o600)
	t.Setenv("KUBECONFIG", kc)
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", StaticDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.Allow("1234")
	return s
}

func do(s *Server, method, url string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, nil)
	r.Host = "127.0.0.1:1234"
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestAuthAndOrigin(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no token", nil, http.StatusUnauthorized},
		{"wrong token", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"ok", map[string]string{"Authorization": "Bearer secret"}, http.StatusOK},
		{"same origin", map[string]string{"Authorization": "Bearer secret", "Origin": "http://127.0.0.1:1234"}, http.StatusOK},
		{"foreign origin", map[string]string{"Authorization": "Bearer secret", "Origin": "http://evil.example"}, http.StatusForbidden},
		{"dns rebinding host", map[string]string{"Authorization": "Bearer secret", "Host": "evil.example:1234"}, http.StatusForbidden},
	}
	for _, c := range cases {
		if got := do(s, http.MethodGet, "/api/clusters", c.hdr).Code; got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestTokenTransport(t *testing.T) {
	s := newTestServer(t)
	if got := do(s, http.MethodGet, "/api/clusters?token=secret", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("query token must not authenticate: got %d", got)
	}
	if got := do(s, http.MethodGet, "/api/clusters", map[string]string{"Authorization": "secret"}).Code; got != http.StatusUnauthorized {
		t.Fatalf("token without Bearer prefix must not authenticate: got %d", got)
	}
	ws := map[string]string{"Upgrade": "websocket", "Sec-WebSocket-Protocol": "leme, bearer.secret"}
	if got := do(s, http.MethodGet, "/api/clusters", ws).Code; got != http.StatusOK {
		t.Fatalf("subprotocol token on upgrade: got %d want 200", got)
	}
	plain := map[string]string{"Sec-WebSocket-Protocol": "bearer.secret"}
	if got := do(s, http.MethodGet, "/api/clusters", plain).Code; got != http.StatusUnauthorized {
		t.Fatalf("subprotocol token without upgrade: got %d want 401", got)
	}
}

func TestStaticWithoutToken(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("static: code %d csp %q", w.Code, w.Header().Get("Content-Security-Policy"))
	}
}

func TestMutationRequiresConnectedCluster(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodPost, "/api/clusters/abc/delete", map[string]string{"Authorization": "Bearer secret"})
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d want 409", w.Code)
	}
}
