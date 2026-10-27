package api

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/clustermap"
	"github.com/jcottobboni/leme/engine/internal/graph"
	"github.com/jcottobboni/leme/engine/internal/ops"
	"github.com/jcottobboni/leme/engine/internal/prom"
	"github.com/jcottobboni/leme/engine/internal/rbac"
	"github.com/jcottobboni/leme/engine/internal/settings"
	"github.com/jcottobboni/leme/engine/internal/timeline"
)

// m2 holds the per-server state of the M2 features.
type m2 struct {
	rbacCache *rbac.Cache
	searcher  *ops.Searcher
	timelines *timeline.Manager

	detectMu sync.Mutex
	detected map[*cluster.Session]detection
}

type detection struct {
	at         time.Time
	candidates []prom.Candidate
}

func newM2() *m2 {
	return &m2{rbacCache: rbac.NewCache(15 * time.Second), searcher: ops.NewSearcher(), timelines: timeline.NewManager(), detected: map[*cluster.Session]detection{}}
}

func (s *Server) m2Routes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/rbac/mine", s.withSession(s.rbacMine))
	m.HandleFunc("POST /api/clusters/{id}/rbac/can", s.withSession(s.rbacCan))
	m.HandleFunc("GET /api/clusters/{id}/rbac/subjects", s.withSession(s.rbacSubjects))
	m.HandleFunc("GET /api/clusters/{id}/rbac/whocan", s.withSession(s.rbacWhoCan))
	m.HandleFunc("GET /api/clusters/{id}/prometheus", s.withSession(s.promConfig))
	m.HandleFunc("PUT /api/clusters/{id}/prometheus", s.withSession(s.promSave))
	m.HandleFunc("POST /api/clusters/{id}/prometheus/test", s.withSession(s.promTest))
	m.HandleFunc("POST /api/clusters/{id}/prometheus/chart", s.withSession(s.promChart))
	m.HandleFunc("GET /api/clusters/{id}/overview", s.withSession(s.overview))
	m.HandleFunc("GET /api/clusters/{id}/graph", s.withSession(s.graph))
	m.HandleFunc("GET /api/clusters/{id}/map", s.withSession(s.clusterMap))
	m.HandleFunc("GET /api/clusters/{id}/projtopo", s.withSession(s.projectTopology))
	m.HandleFunc("GET /api/clusters/{id}/search", s.withSession(s.search))
	m.HandleFunc("GET /api/clusters/{id}/rollout/history", s.withSession(s.rolloutHistory))
	m.HandleFunc("POST /api/clusters/{id}/rollout/undo", s.withSession(s.rolloutUndo))
	m.HandleFunc("GET /api/clusters/{id}/timeline", s.withSession(s.timeline))
}

// --- RBAC ---

func (s *Server) rbacMine(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var res []rbac.Resource
	for _, x := range sess.Resources() {
		res = append(res, rbac.Resource{Group: x.Group, Resource: x.Resource, Kind: x.Kind, Namespaced: x.Namespaced})
	}
	out, err := rbac.Mine(r.Context(), sess.Typed, r.URL.Query().Get("namespace"), res)
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) rbacCan(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req rbac.Request
	if !decode(w, r, &req) {
		return
	}
	if req.Verb == "" || req.Resource == "" {
		httpError(w, http.StatusBadRequest, "verb and resource are required")
		return
	}
	out, err := rbac.Can(r.Context(), sess.Typed, req)
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, out)
}

func (s *Server) rbacSnapshot(r *http.Request, sess *cluster.Session) *rbac.Snapshot {
	return s.m2.rbacCache.Get(r.Context(), sess.Typed, r.URL.Query().Get("scope"), r.URL.Query().Get("refresh") == "1")
}

func (s *Server) rbacSubjects(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	snap := s.rbacSnapshot(r, sess)
	writeJSON(w, map[string]any{"subjects": snap.Subjects(), "errors": snap.Errors})
}

func (s *Server) rbacWhoCan(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	req := rbac.Request{Verb: q.Get("verb"), Group: q.Get("group"), Resource: q.Get("resource"), Subresource: q.Get("subresource"), Name: q.Get("name"), Namespace: q.Get("namespace")}
	if req.Verb == "" || req.Resource == "" {
		httpError(w, http.StatusBadRequest, "verb and resource are required")
		return
	}
	snap := s.rbacSnapshot(r, sess)
	writeJSON(w, map[string]any{"entries": snap.WhoCan(req), "errors": snap.Errors})
}

// --- Prometheus ---

func (s *Server) candidates(ctx context.Context, sess *cluster.Session, refresh bool) []prom.Candidate {
	s.m2.detectMu.Lock()
	d, ok := s.m2.detected[sess]
	s.m2.detectMu.Unlock()
	if ok && !refresh && time.Since(d.at) < time.Minute {
		return d.candidates
	}
	c, _ := prom.Detect(ctx, sess)
	s.m2.detectMu.Lock()
	for k := range s.m2.detected {
		if k.Life().Err() != nil {
			delete(s.m2.detected, k)
		}
	}
	s.m2.detected[sess] = detection{at: time.Now(), candidates: c}
	s.m2.detectMu.Unlock()
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
	if cands == nil {
		cands = []prom.Candidate{}
	}
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

// --- Overview, graph, search, rollout ---

func (s *Server) overview(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, ops.GetOverview(r.Context(), sess, r.URL.Query().Get("namespace")))
}

func (s *Server) clusterMap(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, clustermap.Collect(r.Context(), sess, r.URL.Query().Get("namespace")))
}

// projectTopology is the OpenShift-console-style topology of one namespace (Topologia do projeto).
func (s *Server) projectTopology(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		httpError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	writeJSON(w, clustermap.CollectProject(r.Context(), sess, ns))
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	ns := q.Get("namespace")
	if ns == "" {
		httpError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	if kind, name := q.Get("kind"), q.Get("name"); kind != "" && name != "" {
		writeJSON(w, graph.ForObject(r.Context(), sess, kind, ns, name))
		return
	}
	writeJSON(w, graph.ForNamespace(r.Context(), sess, ns))
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	hits, errs := s.m2.searcher.Search(r.Context(), sess, r.URL.Query().Get("q"), 50)
	writeJSON(w, map[string]any{"hits": hits, "errors": errs})
}

func (s *Server) rolloutHistory(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	revs, err := ops.RolloutHistory(r.Context(), sess, r.URL.Query().Get("namespace"), r.URL.Query().Get("name"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, revs)
}

func (s *Server) rolloutUndo(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		Revision  int64  `json:"revision"`
		DryRun    bool   `json:"dryRun"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := ops.RolloutUndo(r.Context(), sess, req.Namespace, req.Name, req.Revision, req.DryRun)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("rollout undo", "context", sess.Context.Name, "namespace", req.Namespace, "deployment", req.Name, "revision", req.Revision)
	}
	writeJSON(w, res)
}

// --- Timeline (WebSocket) ---

type timelineMsg struct {
	Type   string           `json:"type"`
	Events []timeline.Event `json:"events,omitempty"`
	Error  string           `json:"error,omitempty"`
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()
	col, err := s.m2.timelines.Get(ctx, sess, r.URL.Query().Get("namespace"))
	if err != nil {
		_ = wsjson.Write(ctx, c, timelineMsg{Type: "error", Error: err.Error()})
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	drain, wake, cancel := col.Subscribe()
	defer cancel()
	if err := wsjson.Write(ctx, c, timelineMsg{Type: "snapshot", Events: col.Snapshot()}); err != nil {
		return
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-wake:
			// Batch bursts: wait for the next tick.
			continue
		}
		if evs := drain(); len(evs) > 0 {
			if err := wsjson.Write(ctx, c, timelineMsg{Type: "delta", Events: evs}); err != nil {
				return
			}
		}
	}
}
