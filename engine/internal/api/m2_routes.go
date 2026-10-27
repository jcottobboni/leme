package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
	"github.com/jcottobboni/leme/engine/internal/prom"
	"github.com/jcottobboni/leme/engine/internal/rbac"
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
	m.HandleFunc("GET /api/clusters/{id}/search", s.withSession(s.search))
	m.HandleFunc("GET /api/clusters/{id}/rollout/history", s.withSession(s.rolloutHistory))
	m.HandleFunc("POST /api/clusters/{id}/rollout/undo", s.withSession(s.rolloutUndo))
	m.HandleFunc("GET /api/clusters/{id}/timeline", s.withSession(s.timeline))
}
