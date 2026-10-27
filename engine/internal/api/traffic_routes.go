package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/prom"
	"github.com/jcottobboni/leme/engine/internal/traffic"
)

// trafficRoutes serves observed traffic edges (M5, feature traffic): GET /api/clusters/{id}/traffic.
func (s *Server) trafficRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/traffic", s.withSession(s.traffic))
}

// traffic reads Caretta or Hubble metrics from the configured (or detected) Prometheus. It never
// installs anything; without a source the snapshot says how to get one.
func (s *Server) traffic(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	ns, window := q.Get("namespace"), q.Get("window")
	if window == "" {
		window = traffic.DefaultWindow
	}
	if _, ok := traffic.Windows[window]; !ok {
		httpError(w, http.StatusBadRequest, "window must be one of 1m, 5m, 15m, 30m, 1h")
		return
	}
	if !traffic.ValidNamespace(ns) {
		httpError(w, http.StatusBadRequest, "invalid namespace")
		return
	}
	cfg, _, found := s.activeProm(r.Context(), sess)
	if !found {
		writeJSON(w, traffic.Snapshot{Edges: []traffic.Edge{}, Window: window, Hint: traffic.HintNoPrometheus})
		return
	}
	c, err := prom.NewClient(sess, cfg)
	if err != nil {
		writeJSON(w, traffic.Snapshot{Edges: []traffic.Edge{}, Window: window, Hint: traffic.HintNoPrometheus, Errors: map[string]string{"prometheus": err.Error()}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, traffic.Collect(ctx, c, traffic.Options{Namespace: ns, Window: window}))
}
