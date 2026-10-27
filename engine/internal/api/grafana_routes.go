package api

import (
	"context"
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/grafana"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// grafanaRoutes detects Grafana/Prometheus services and opens them through a port-forward (M5, feature grafana).
func (s *Server) grafanaRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/grafana/targets", s.withSession(s.grafanaTargets))
	m.HandleFunc("POST /api/clusters/{id}/grafana/open", s.withSession(s.grafanaOpen))
}

// grafanaForward is the active port-forward of a target, when there is one.
type grafanaForward struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type grafanaTarget struct {
	grafana.Target
	Forward *grafanaForward `json:"forward,omitempty"`
}

// activeGrafanaForward returns a healthy forward of this cluster to the target's service port.
func (s *Server) activeGrafanaForward(sess *cluster.Session, t grafana.Target) (ops.Forward, bool) {
	for _, f := range s.forwards.List() {
		if f.ClusterID == sess.Context.ID && f.Kind == "service" && f.Namespace == t.Namespace && f.Name == t.Service && f.RemotePort == t.Port && f.Error == "" {
			return f, true
		}
	}
	return ops.Forward{}, false
}

func (s *Server) grafanaTargets(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ts, err := grafana.Detect(r.Context(), sess)
	if err != nil {
		kubeError(w, err)
		return
	}
	out := make([]grafanaTarget, 0, len(ts))
	for _, t := range ts {
		v := grafanaTarget{Target: t}
		if f, ok := s.activeGrafanaForward(sess, t); ok {
			v.Forward = &grafanaForward{ID: f.ID, URL: f.URL + "/"}
		}
		out = append(out, v)
	}
	writeJSON(w, map[string]any{"targets": out})
}

// grafanaOpen opens (or reuses) a port-forward to a detected target. The target is re-detected on
// every call: the client cannot point the forward at an arbitrary service.
func (s *Server) grafanaOpen(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Namespace string `json:"namespace"`
		Service   string `json:"service"`
		Port      int    `json:"port"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Namespace == "" || req.Service == "" || req.Port <= 0 || req.Port > 65535 {
		httpError(w, http.StatusBadRequest, "namespace, service and port are required")
		return
	}
	ts, err := grafana.Detect(r.Context(), sess)
	if err != nil {
		kubeError(w, err)
		return
	}
	t, ok := grafana.Find(ts, req.Namespace, req.Service, req.Port)
	if !ok {
		httpError(w, http.StatusNotFound, "not a detected Grafana or Prometheus service")
		return
	}
	if t.Scheme != "http" {
		// The app window only loads http://127.0.0.1:<port>; TLS backends would need a proxy.
		httpError(w, http.StatusBadRequest, "HTTPS services cannot be opened in the app")
		return
	}
	if f, ok := s.activeGrafanaForward(sess, t); ok {
		writeJSON(w, map[string]any{"url": f.URL + "/", "forwardId": f.ID, "reused": true})
		return
	}
	f, err := s.forwards.Start(context.WithoutCancel(r.Context()), sess, ops.ForwardRequest{Namespace: t.Namespace, Kind: "service", Name: t.Service, RemotePort: t.Port, LocalPort: 0})
	if err != nil {
		kubeError(w, err)
		return
	}
	if s.log != nil {
		s.log.Info("grafana: port-forward opened", "cluster", sess.Context.Name, "kind", t.Kind, "namespace", t.Namespace, "service", t.Service, "port", t.Port, "localPort", f.LocalPort)
	}
	writeJSON(w, map[string]any{"url": f.URL + "/", "forwardId": f.ID, "reused": false})
}
