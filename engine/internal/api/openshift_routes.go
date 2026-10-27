package api

import (
	"net/http"

	"github.com/coder/websocket"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// OpenShift actions: rollout latest, start build, build logs.

func (s *Server) osRollout(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct{ Namespace, Name string }
	if !decode(w, r, &req) {
		return
	}
	v, err := ops.RolloutLatest(r.Context(), sess, req.Namespace, req.Name)
	s.auditUI(sess, "rollout-latest", "DeploymentConfig/"+req.Namespace+"/"+req.Name, "", err)
	if err != nil {
		kubeError(w, err)
		return
	}
	s.log.Info("openshift rollout latest", "context", sess.Context.Name, "namespace", req.Namespace, "dc", req.Name, "revision", v)
	writeJSON(w, map[string]int64{"revision": v})
}

func (s *Server) osStartBuild(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req struct{ Namespace, Name string }
	if !decode(w, r, &req) {
		return
	}
	b, err := ops.StartBuild(r.Context(), sess, req.Namespace, req.Name)
	s.auditUI(sess, "start-build", "BuildConfig/"+req.Namespace+"/"+req.Name, "", err)
	if err != nil {
		kubeError(w, err)
		return
	}
	s.log.Info("openshift start build", "context", sess.Context.Name, "namespace", req.Namespace, "bc", req.Name, "build", b)
	writeJSON(w, map[string]string{"build": b})
}

func (s *Server) osBuildLog(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()
	if err := ops.StreamBuildLogs(ctx, sess, q.Get("namespace"), q.Get("name"), q.Get("follow") != "0", wsWriter{ctx, c, websocket.MessageText}); err != nil {
		_ = c.Write(ctx, websocket.MessageText, []byte("\r\n[Leme] "+err.Error()+"\r\n"))
	}
	c.Close(websocket.StatusNormalClosure, "")
}
