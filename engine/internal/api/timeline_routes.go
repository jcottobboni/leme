package api

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/timeline"
)

// Timeline of events (WebSocket).

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
