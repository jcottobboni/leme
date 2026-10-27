package api

import (
	"fmt"
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// Deployment rollout history and undo.

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
	if !req.DryRun {
		s.auditUI(sess, "rollout-undo", "Deployment/"+req.Namespace+"/"+req.Name, fmt.Sprintf("revision=%d", req.Revision), err)
	}
	if err != nil {
		kubeError(w, err)
		return
	}
	if !req.DryRun {
		s.log.Info("rollout undo", "context", sess.Context.Name, "namespace", req.Namespace, "deployment", req.Name, "revision", req.Revision)
	}
	writeJSON(w, res)
}
