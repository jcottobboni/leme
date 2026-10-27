package api

import (
	"net/http"
	"testing"
)

func TestTrafficRouteRequiresAuthAndSession(t *testing.T) {
	s := newTestServer(t)
	if w := do(s, http.MethodGet, "/api/clusters/abc/traffic", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without token: got %d want 401", w.Code)
	}
	// Registered (not 404) and gated on a connected cluster like every other cluster route.
	if w := do(s, http.MethodGet, "/api/clusters/abc/traffic?window=5m", map[string]string{"Authorization": "Bearer secret"}); w.Code != http.StatusConflict {
		t.Fatalf("not connected: got %d want 409", w.Code)
	}
}
