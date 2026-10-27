package api

import (
	"net/http"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/istio"
	"github.com/jcottobboni/leme/engine/internal/security"
)

// istioRoutes serves the Istio panel and the optional istioctl analyze scan (M5, feature istio).
func (s *Server) istioRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/istio/tool", s.istioTool)
	m.HandleFunc("GET /api/clusters/{id}/istio", s.withSession(s.istioReport))
	m.HandleFunc("POST /api/clusters/{id}/istio/analyze", s.withSession(s.istioAnalyze))
}

func (s *Server) istioTool(w http.ResponseWriter, r *http.Request) {
	p, err := security.LookTool("istioctl")
	if err != nil {
		writeJSON(w, map[string]any{"installed": false, "hint": istio.InstallHint})
		return
	}
	writeJSON(w, map[string]any{"installed": true, "path": p})
}

func (s *Server) istioReport(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, istio.Load(r.Context(), sess, istio.InformerLister(sess), r.URL.Query().Get("namespace")))
}

func (s *Server) istioAnalyze(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Namespace string `json:"namespace"`
	}
	if !decode(w, r, &req) {
		return
	}
	// The namespace reaches istioctl's command line: only a DNS-1123 label (or "" for all).
	if req.Namespace != "" && len(validation.IsDNS1123Label(req.Namespace)) > 0 {
		httpError(w, http.StatusBadRequest, "invalid namespace")
		return
	}
	bin, err := security.LookTool("istioctl")
	if err != nil {
		writeJSON(w, map[string]any{"installed": false, "hint": istio.InstallHint})
		return
	}
	if len(istio.Installed(sess)) == 0 {
		httpError(w, http.StatusBadRequest, "o cluster não tem os CRDs do Istio")
		return
	}
	objs, errs := istio.ExportInput(r.Context(), sess, istio.InformerLister(sess), req.Namespace)
	msgs, err := istio.Analyze(r.Context(), bin, objs, req.Namespace)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := map[string]any{"installed": true, "messages": msgs}
	if len(errs) > 0 {
		out["errors"] = errs
	}
	writeJSON(w, out)
}
