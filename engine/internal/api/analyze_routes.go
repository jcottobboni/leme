package api

import (
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/analyze"
	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// analyzeRoutes serves the deterministic analyzer (M4, group G1).
func (s *Server) analyzeRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/diagnose", s.withSession(s.diagnose))
}

func (s *Server) diagnose(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	var (
		findings []analyze.Finding
		err      error
	)
	if q.Get("kind") != "" && q.Get("name") != "" {
		findings, err = analyze.Object(r.Context(), sess, analyze.Ref{Group: q.Get("group"), Kind: q.Get("kind"), Namespace: q.Get("namespace"), Name: q.Get("name")})
	} else {
		findings, err = analyze.Namespace(r.Context(), sess, q.Get("namespace"))
	}
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, map[string]any{"findings": findings})
}
