// Package api exposes the engine to the UI over HTTP and WebSocket on loopback.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/helm"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/ops"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// Server is the HTTP front of the engine.
type Server struct {
	mgr      *cluster.Manager
	forwards *ops.Forwards
	helm     *helm.Service
	settings *settings.Store
	m2       *m2
	m3       *m3
	audit    *audit.Log
	changes  *change.Store
	ai       *aiState
	token    string
	origins  map[string]bool
	static   fs.FS
	log      *slog.Logger
	mux      *http.ServeMux
}

// Options configure the server.
type Options struct {
	Token      string
	StaticDir  string
	DevOrigins []string
	Logger     *slog.Logger
	Settings   *settings.Store
	ConfigDir  string
}

// New builds the server; call Allow(port) once the listener port is known.
func New(mgr *cluster.Manager, o Options) *Server {
	s := &Server{mgr: mgr, forwards: ops.NewForwards(), helm: helm.New(o.Logger), settings: o.Settings, m2: newM2(), m3: newM3(o.ConfigDir), audit: audit.Open(o.ConfigDir), changes: change.NewStore(), ai: newAIState(), token: o.Token, origins: map[string]bool{}, log: o.Logger, mux: http.NewServeMux()}
	for _, d := range o.DevOrigins {
		s.origins[d] = true
	}
	if o.StaticDir != "" {
		s.static = os.DirFS(o.StaticDir)
	}
	s.routes()
	return s
}

// Allow registers the loopback origins for the bound port.
func (s *Server) Allow(port string) {
	s.origins["http://127.0.0.1:"+port] = true
	s.origins["http://localhost:"+port] = true
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"ok": true}) })
	m.HandleFunc("GET /api/clusters", s.listClusters)
	m.HandleFunc("POST /api/clusters/{id}/connect", s.connect)
	m.HandleFunc("POST /api/clusters/{id}/disconnect", s.disconnect)
	m.HandleFunc("POST /api/clusters/{id}/readonly", s.setReadOnly)
	m.HandleFunc("GET /api/clusters/{id}/resources", s.withSession(s.resources))
	m.HandleFunc("GET /api/clusters/{id}/watch", s.withSession(s.watch))
	m.HandleFunc("GET /api/clusters/{id}/object", s.withSession(s.getObject))
	m.HandleFunc("POST /api/clusters/{id}/apply", s.withSession(s.apply))
	m.HandleFunc("POST /api/clusters/{id}/delete", s.withSession(s.deleteObject))
	m.HandleFunc("POST /api/clusters/{id}/scale", s.withSession(s.scale))
	m.HandleFunc("POST /api/clusters/{id}/restart", s.withSession(s.restart))
	m.HandleFunc("GET /api/clusters/{id}/events", s.withSession(s.events))
	m.HandleFunc("GET /api/clusters/{id}/metrics/pods", s.withSession(s.podMetrics))
	m.HandleFunc("GET /api/clusters/{id}/metrics/nodes", s.withSession(s.nodeMetrics))
	m.HandleFunc("GET /api/clusters/{id}/logs", s.withSession(s.logs))
	m.HandleFunc("GET /api/clusters/{id}/exec", s.withSession(s.exec))
	m.HandleFunc("POST /api/clusters/{id}/portforwards", s.withSession(s.startForward))
	m.HandleFunc("GET /api/portforwards", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.forwards.List()) })
	m.HandleFunc("DELETE /api/portforwards/{fid}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"stopped": s.forwards.Stop(r.PathValue("fid"))})
	})
	s.helmRoutes(m)
	s.m2Routes(m)
	s.m3Routes(m)
	s.analyzeRoutes(m)
	s.agentRoutes(m)
	s.aiRoutes(m)
	m.HandleFunc("/", s.serveStatic)
}

// ServeHTTP enforces loopback host, origin and token before routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "127.0.0.1" && host != "localhost" {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if o := r.Header.Get("Origin"); o != "" && !s.origins[o] {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
	}
	s.mux.ServeHTTP(w, r)
}

// wsTokenPrefix marks the WebSocket subprotocol that carries the session token.
// Browsers cannot set Authorization on upgrades, and query strings end up in logs and history.
const wsTokenPrefix = "bearer."

func (s *Server) authorized(r *http.Request) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	} else if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			if p = strings.TrimSpace(p); strings.HasPrefix(p, wsTokenPrefix) {
				got = strings.TrimPrefix(p, wsTokenPrefix)
			}
		}
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.Error(w, "UI not bundled; run with --static", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(s.static, p); err != nil {
		p = "index.html"
	}
	if strings.HasSuffix(p, ".html") {
		// popout.html receives DOM moved from the main window, so it gets the same policy.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws://127.0.0.1:* ws://localhost:*; worker-src 'self' blob:; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeFileFS(w, r, s.static, p)
}

type sessionHandler func(w http.ResponseWriter, r *http.Request, sess *cluster.Session)

func (s *Server) withSession(h sessionHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.mgr.Get(r.PathValue("id"))
		if !ok {
			httpError(w, http.StatusConflict, "cluster not connected")
			return
		}
		h(w, r, sess)
	}
}

type clusterView struct {
	kube.Context
	Connected  bool `json:"connected"`
	ReadOnly   bool `json:"readOnly"`
	InAppLogin bool `json:"inAppLogin"`
}

func (s *Server) listClusters(w http.ResponseWriter, r *http.Request) {
	ctxs, err := s.mgr.Loader().Contexts()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]clusterView, 0, len(ctxs))
	for _, c := range ctxs {
		v := clusterView{Context: c, ReadOnly: c.Prod || !c.Local, InAppLogin: s.mgr.HasToken(c.ID)}
		if sess, ok := s.mgr.Get(c.ID); ok {
			v.Connected, v.ReadOnly = true, sess.ReadOnly()
		}
		out = append(out, v)
	}
	writeJSON(w, map[string]any{"clusters": out, "kubeconfigs": s.mgr.Loader().Paths()})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sess, err := s.mgr.Connect(ctx, r.PathValue("id"))
	if err != nil {
		s.log.Warn("connect failed", "cluster", r.PathValue("id"), "err", err)
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	version := ""
	if raw, err := sess.Typed.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw(); err == nil {
		var v struct {
			GitVersion string `json:"gitVersion"`
		}
		if json.Unmarshal(raw, &v) == nil {
			version = v.GitVersion
		}
	}
	writeJSON(w, map[string]any{"resources": sess.Resources(), "namespace": sess.Namespace, "readOnly": sess.ReadOnly(), "version": version})
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.forwards.StopCluster(id)
	s.mgr.Disconnect(id)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) setReadOnly(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		httpError(w, http.StatusConflict, "cluster not connected")
		return
	}
	var body struct {
		ReadOnly bool   `json:"readOnly"`
		Confirm  string `json:"confirm"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !body.ReadOnly && sess.Context.Prod && body.Confirm != sess.Context.Name {
		httpError(w, http.StatusBadRequest, "type the exact context name to unlock a production cluster")
		return
	}
	sess.SetReadOnly(body.ReadOnly)
	s.log.Info("read-only changed", "context", sess.Context.Name, "readOnly", body.ReadOnly)
	writeJSON(w, map[string]bool{"readOnly": sess.ReadOnly()})
}

func (s *Server) resources(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, sess.Resources())
}

func refFromQuery(r *http.Request) ops.Ref {
	q := r.URL.Query()
	return ops.Ref{Group: q.Get("group"), Version: q.Get("version"), Resource: q.Get("resource"), Namespace: q.Get("namespace"), Name: q.Get("name")}
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ref := refFromQuery(r)
	reveal := r.URL.Query().Get("reveal") == "1"
	v, err := ops.Get(r.Context(), sess, ref, reveal)
	if err != nil {
		kubeError(w, err)
		return
	}
	if reveal && v.Object["kind"] == "Secret" {
		s.log.Info("secret revealed", "context", sess.Context.Name, "namespace", ref.Namespace, "name", ref.Name)
	}
	writeJSON(w, v)
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var body struct {
		YAML   string `json:"yaml"`
		DryRun bool   `json:"dryRun"`
	}
	if !decode(w, r, &body) {
		return
	}
	res, err := ops.Apply(r.Context(), sess, body.YAML, body.DryRun)
	if err != nil {
		kubeError(w, err)
		return
	}
	if !body.DryRun {
		s.log.Info("applied", "context", sess.Context.Name, "created", res.Created)
	}
	writeJSON(w, res)
}

func (s *Server) deleteObject(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var ref ops.Ref
	if !decode(w, r, &ref) {
		return
	}
	if err := ops.Delete(r.Context(), sess, ref); err != nil {
		kubeError(w, err)
		return
	}
	s.log.Info("deleted", "context", sess.Context.Name, "resource", ref.Resource, "namespace", ref.Namespace, "name", ref.Name)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) scale(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var body struct {
		ops.Ref
		Replicas int64 `json:"replicas"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := ops.Scale(r.Context(), sess, body.Ref, body.Replicas); err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var ref ops.Ref
	if !decode(w, r, &ref) {
		return
	}
	if err := ops.Restart(r.Context(), sess, ref); err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	ev, err := ops.Events(r.Context(), sess, r.URL.Query().Get("namespace"), r.URL.Query().Get("uid"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, ev)
}

func (s *Server) podMetrics(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	m, err := ops.PodUsage(r.Context(), sess, r.URL.Query().Get("namespace"))
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, m)
}

func (s *Server) nodeMetrics(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	m, err := ops.NodeUsage(r.Context(), sess)
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, m)
}

func (s *Server) startForward(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req ops.ForwardRequest
	if !decode(w, r, &req) {
		return
	}
	f, err := s.forwards.Start(context.WithoutCancel(r.Context()), sess, req)
	if err != nil {
		kubeError(w, err)
		return
	}
	writeJSON(w, f)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func kubeError(w http.ResponseWriter, err error) {
	var st apierrors.APIStatus
	switch {
	case errors.Is(err, cluster.ErrReadOnly):
		httpError(w, http.StatusForbidden, err.Error())
	case helm.IsNotFound(err):
		httpError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &st) && st.Status().Code >= 400:
		httpError(w, int(st.Status().Code), st.Status().Message)
	default:
		httpError(w, http.StatusBadRequest, err.Error())
	}
}
