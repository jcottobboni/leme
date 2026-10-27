package api

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/prom"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// Prometheus: detection, configuration and charts.

type promDetectKey struct{}

// candidates returns the Prometheus endpoints detected in the cluster, re-detected after a minute.
func (s *Server) candidates(ctx context.Context, sess *cluster.Session, refresh bool) []prom.Candidate {
	c, _ := cluster.Cached(sess, promDetectKey{}, time.Minute, refresh, func() ([]prom.Candidate, error) {
		c, _ := prom.Detect(ctx, sess) // partial results are still useful; detection errors are not reported
		return c, nil
	})
	return c
}

// promKey identifies a cluster in settings.json: context names alone can repeat across kubeconfig files.
func promKey(sess *cluster.Session) string { return sess.Context.Name + "@" + sess.Context.Server }

// activeProm returns the saved configuration, or the best detected candidate.
func (s *Server) activeProm(ctx context.Context, sess *cluster.Session) (settings.Prometheus, bool, bool) {
	if s.settings != nil {
		if p, ok := s.settings.Prometheus(promKey(sess)); ok {
			return p, true, true
		}
	}
	if c := s.candidates(ctx, sess, false); len(c) > 0 {
		return c[0].Prometheus, false, true
	}
	return settings.Prometheus{}, false, false
}

func (s *Server) promConfig(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	cands := s.candidates(r.Context(), sess, r.URL.Query().Get("refresh") == "1")
	active, saved, ok := s.activeProm(r.Context(), sess)
	out := map[string]any{"candidates": cands, "saved": saved}
	if ok {
		out["active"] = active
	}
	writeJSON(w, out)
}

func (s *Server) promSave(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var p settings.Prometheus
	if !decode(w, r, &p) {
		return
	}
	if err := prom.Validate(p); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Mode == "url" && p.SendToken && !prom.TokenAllowed(sess.Config.Host, hostOf(p.URL)) {
		httpError(w, http.StatusBadRequest, "the cluster token is only sent to the API server or hosts under its domain")
		return
	}
	if s.settings == nil {
		httpError(w, http.StatusInternalServerError, "settings store unavailable")
		return
	}
	if err := s.settings.SetPrometheus(promKey(sess), p); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) promTest(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var p settings.Prometheus
	if !decode(w, r, &p) {
		return
	}
	c, err := prom.NewClient(sess, p)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	series, err := c.Query(ctx, "count(up)")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	targets := 0.0
	if len(series) > 0 && len(series[0].Points) > 0 {
		targets = series[0].Points[0][1]
	}
	writeJSON(w, map[string]any{"ok": true, "targets": targets})
}

func (s *Server) promChart(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Scope   prom.Scope `json:"scope"`
		Metrics []string   `json:"metrics"`
		Range   string     `json:"range"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Metrics) > 8 || len(req.Scope.Pods) > 300 {
		httpError(w, http.StatusBadRequest, "too many metrics or pods in one request")
		return
	}
	rng, ok := prom.Ranges[req.Range]
	if !ok {
		httpError(w, http.StatusBadRequest, "range must be one of 15m, 1h, 6h, 24h, 7d")
		return
	}
	cfg, _, found := s.activeProm(r.Context(), sess)
	if !found {
		httpError(w, http.StatusNotFound, "prometheus not configured")
		return
	}
	c, err := prom.NewClient(sess, cfg)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	charts := make([]*prom.Chart, len(req.Metrics))
	errs := make([]string, len(req.Metrics))
	var wg sync.WaitGroup
	for i, metric := range req.Metrics {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := c.Chart(ctx, sess.Context.ID, req.Scope, metric, rng)
			if err != nil {
				errs[i] = err.Error()
				return
			}
			charts[i] = ch
		}()
	}
	wg.Wait()
	writeJSON(w, map[string]any{"charts": charts, "errors": errs, "source": cfg})
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
