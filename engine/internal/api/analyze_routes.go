package api

import (
	"net/http"
	"sort"
	"strings"

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
		errs     map[string]string
		err      error
	)
	if q.Get("kind") != "" && q.Get("name") != "" {
		findings, err = analyze.Object(r.Context(), sess, analyze.Ref{Group: q.Get("group"), Kind: q.Get("kind"), Namespace: q.Get("namespace"), Name: q.Get("name")})
	} else {
		findings, errs, err = analyze.Namespace(r.Context(), sess, q.Get("namespace"))
	}
	if err != nil {
		kubeError(w, err)
		return
	}
	out := map[string]any{"findings": findings}
	if len(errs) > 0 {
		// "group/resource" -> error of the kinds that could not be listed (e.g. no RBAC permission).
		out["errors"] = errs
	}
	writeJSON(w, out)
}

// writeListErrors tells the model which kinds were not diagnosed.
func writeListErrors(b *strings.Builder, errs map[string]string) {
	if len(errs) == 0 {
		return
	}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("\nNão diagnosticados (sem acesso ou erro ao listar): " + strings.Join(keys, ", ") + "\n")
}
