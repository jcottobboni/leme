package api

import (
	"net/http"
	"strconv"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/history"
)

// Persistent change history (M7, RF06): what the informers of a connected cluster observed, kept
// per cluster on disk. Reads only.

func (s *Server) historyRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/history", s.withSession(s.getHistory))
}

// attachHistory makes the session's informers record into the cluster's history file.
func (s *Server) attachHistory(sess *cluster.Session) {
	if s.history == nil || sess.Informers == nil {
		return
	}
	rec, err := s.history.Recorder(sess.Context.Fingerprint(), s.log)
	if err != nil {
		s.log.Warn("history disabled for cluster", "context", sess.Context.Name, "err", err)
		return
	}
	sess.Informers.SetObserver(rec)
}

type historyPage struct {
	history.Page
	RetentionDays int `json:"retentionDays"`
}

// getHistory: ?group=&resource=&namespace=&name=&type=&before=&limit= (newest first; "next" is the
// cursor for before=). resource selects one kind (group compared exactly, "" = core).
func (s *Server) getHistory(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	page, err := s.history.Query(sess.Context.Fingerprint(), history.Query{
		Group: q.Get("group"), Resource: q.Get("resource"), Namespace: q.Get("namespace"), Name: q.Get("name"),
		Type: q.Get("type"), Before: before, Limit: limit,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if page.Entries == nil {
		page.Entries = []history.Entry{}
	}
	writeJSON(w, historyPage{Page: page, RetentionDays: int(s.history.MaxAge().Hours() / 24)})
}
