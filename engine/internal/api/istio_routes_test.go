package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func TestIstioRoutesWithoutIstio(t *testing.T) {
	s := newTestServer(t)
	// No istioctl anywhere: empty PATH and a HOME without ~/.local/bin.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	w := do(s, http.MethodGet, "/api/istio/tool", map[string]string{"Authorization": "Bearer secret"})
	var tool map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &tool); err != nil || w.Code != http.StatusOK || tool["installed"] != false || tool["hint"] == "" {
		t.Fatalf("tool: %d %s", w.Code, w.Body)
	}
	if w := do(s, http.MethodGet, "/api/clusters/x/istio", map[string]string{"Authorization": "Bearer secret"}); w.Code != http.StatusConflict {
		t.Fatalf("not connected: %d", w.Code)
	}

	// A cluster without the Istio CRDs: the panel gets installed=false and empty lists.
	sess := cluster.NewStaticSession(kube.Context{ID: "k", Name: "k"}, nil, nil, []cluster.Resource{{Version: "v1", Resource: "namespaces", Kind: "Namespace"}}, true)
	rec := httptest.NewRecorder()
	s.istioReport(rec, httptest.NewRequest(http.MethodGet, "/api/clusters/k/istio", nil), sess)
	var rep struct {
		Installed       bool  `json:"installed"`
		Namespaces      []any `json:"namespaces"`
		VirtualServices []any `json:"virtualServices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rep.Installed || rep.Namespaces == nil || rep.VirtualServices == nil {
		t.Fatalf("report: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	s.istioAnalyze(rec, httptest.NewRequest(http.MethodPost, "/api/clusters/k/istio/analyze", strings.NewReader(`{"namespace":"shop"}`)), sess)
	if !strings.Contains(rec.Body.String(), `"installed":false`) || !strings.Contains(rec.Body.String(), "istioctl") {
		t.Fatalf("analyze without istioctl: %d %s", rec.Code, rec.Body)
	}
}
