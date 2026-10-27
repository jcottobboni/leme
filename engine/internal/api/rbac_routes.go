package api

import (
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/rbac"
)

// RBAC: my permissions, can-i, subjects and who-can.

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
