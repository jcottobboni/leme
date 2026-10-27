package api

import (
	"net/http"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/clustermap"
	"github.com/jcottobboni/leme/engine/internal/graph"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// Cluster overview, map, topology graph and global search.

func (s *Server) overview(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, ops.GetOverview(r.Context(), sess, r.URL.Query().Get("namespace")))
}

func (s *Server) clusterMap(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	writeJSON(w, clustermap.Collect(r.Context(), sess, r.URL.Query().Get("namespace")))
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	ns := q.Get("namespace")
	if ns == "" {
		httpError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	if kind, name := q.Get("kind"), q.Get("name"); kind != "" && name != "" {
		writeJSON(w, graph.ForObject(r.Context(), sess, q.Get("group"), kind, ns, name))
		return
	}
	writeJSON(w, graph.ForNamespace(r.Context(), sess, ns))
}

func (s *Server) search(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	hits, errs := s.m2.searcher.Search(r.Context(), sess, r.URL.Query().Get("q"), 50)
	writeJSON(w, map[string]any{"hits": hits, "errors": errs})
}
