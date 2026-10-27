package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// Aggregated logs of every pod of a workload or label selector (M7, RF01). A read: allowed on
// read-only clusters, like the logs of one pod.

func (s *Server) multiLogRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/clusters/{id}/logs/multi", s.withSession(s.multiLogs))
}

// maxPendingLines bounds what waits for a slow client; older lines are dropped (and counted).
const maxPendingLines = 10000

// logQueue merges consecutive lines into one message and keeps the order of the other messages.
type logQueue struct {
	mu      sync.Mutex
	msgs    []ops.MultiLogMsg
	lines   int
	dropped int
	wake    chan struct{}
}

func (q *logQueue) push(m ops.MultiLogMsg) {
	q.mu.Lock()
	if m.Type == "lines" {
		q.lines += len(m.Lines)
		if n := len(q.msgs); n > 0 && q.msgs[n-1].Type == "lines" {
			q.msgs[n-1].Lines = append(q.msgs[n-1].Lines, m.Lines...)
		} else {
			q.msgs = append(q.msgs, m)
		}
		for q.lines > maxPendingLines {
			q.dropOldestLine()
		}
	} else {
		q.msgs = append(q.msgs, m)
	}
	q.mu.Unlock()
	if m.Type != "lines" {
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
}

func (q *logQueue) dropOldestLine() {
	for i := range q.msgs {
		if q.msgs[i].Type == "lines" && len(q.msgs[i].Lines) > 0 {
			q.msgs[i].Lines = q.msgs[i].Lines[1:]
			q.lines--
			q.dropped++
			return
		}
	}
	q.lines = 0
}

func (q *logQueue) drain() ([]ops.MultiLogMsg, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out, dropped := q.msgs, q.dropped
	q.msgs, q.lines, q.dropped = nil, 0, 0
	return out, dropped
}

func (s *Server) multiLogs(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	tail, _ := strconv.ParseInt(q.Get("tail"), 10, 64)
	if tail <= 0 || tail > 10000 {
		tail = 200
	}
	o := ops.MultiLogOptions{Namespace: q.Get("namespace"), Selector: q.Get("selector"), Container: q.Get("container"), TailLines: tail, MaxPods: ops.DefaultMaxPods}
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()
	fail := func(msg string) {
		_ = wsjson.Write(ctx, c, ops.MultiLogMsg{Type: "error", Error: msg})
		c.Close(websocket.StatusNormalClosure, "")
	}
	if o.Selector == "" {
		sel, err := workloadSelector(ctx, sess, q.Get("group"), q.Get("resource"), q.Get("kind"), o.Namespace, q.Get("name"))
		if err != nil {
			fail(err.Error())
			return
		}
		o.Selector = sel
	}
	if err := wsjson.Write(ctx, c, ops.MultiLogMsg{Type: "selector", Selector: o.Selector, Namespace: o.Namespace, Max: o.MaxPods}); err != nil {
		return
	}
	queue := &logQueue{wake: make(chan struct{}, 1)}
	streamErr := make(chan error, 1)
	go func() { streamErr <- ops.StreamMultiLogs(ctx, sess, o, queue.push) }()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	flush := func() bool {
		msgs, dropped := queue.drain()
		if dropped > 0 {
			msgs = append([]ops.MultiLogMsg{{Type: "dropped", Dropped: dropped}}, msgs...)
		}
		for _, m := range msgs {
			if m.Type == "lines" && len(m.Lines) == 0 {
				continue
			}
			if err := wsjson.Write(ctx, c, m); err != nil {
				return false
			}
		}
		return true
	}
	for {
		select {
		case <-ctx.Done():
			<-streamErr
			return
		case err := <-streamErr:
			flush()
			if err != nil {
				fail(err.Error())
			} else {
				c.Close(websocket.StatusNormalClosure, "")
			}
			return
		case <-tick.C:
		case <-queue.wake:
		}
		if !flush() {
			done()
			<-streamErr
			return
		}
	}
}

// workloadSelector reads the pod selector of a workload (group + resource, or group + kind).
func workloadSelector(ctx context.Context, sess *cluster.Session, group, resource, kind, namespace, name string) (string, error) {
	res, ok := sess.Lookup(group, resource)
	if !ok && kind != "" {
		res, ok = sess.LookupKind(group, kind)
	}
	if !ok || name == "" {
		return "", errors.New("informe o workload (group, resource e name) ou um seletor de rótulos")
	}
	ns := namespace
	if !res.Namespaced {
		ns = ""
	}
	u, err := sess.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return ops.WorkloadSelector(u)
}
