package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
	"github.com/jcottobboni/leme/engine/internal/project"
)

const flushInterval = 200 * time.Millisecond

// within returns a context cancelled when either parent is done.
func within(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	stop := context.AfterFunc(b, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	// Origin is already validated in ServeHTTP. The client offers "leme" plus the token subprotocol;
	// only "leme" is echoed back so the token never appears in the response.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, Subprotocols: []string{"leme"}, CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(1 << 20)
	return c, nil
}

// gridSub batches cache events into row deltas for one WebSocket.
type gridSub struct {
	mu      sync.Mutex
	sent    map[string]string // uid -> resourceVersion already delivered to the client
	upserts map[string]project.Row
	deletes map[string]struct{}
	errMsg  string
	wake    chan struct{}
	printer *project.Printer // CRD printer columns; nil for built-in kinds
}

func newGridSub(p *project.Printer) *gridSub {
	return &gridSub{sent: map[string]string{}, upserts: map[string]project.Row{}, deletes: map[string]struct{}{}, wake: make(chan struct{}, 1), printer: p}
}

// row projects an object, with the CRD printer columns when there are any.
func (g *gridSub) row(u *unstructured.Unstructured) project.Row {
	r := project.Project(u)
	if g.printer != nil {
		g.printer.Apply(u, &r)
	}
	return r
}

func (g *gridSub) OnUpsert(u *unstructured.Unstructured) {
	uid := string(u.GetUID())
	rv := u.GetResourceVersion()
	g.mu.Lock()
	defer g.mu.Unlock()
	// The informer replays every object while syncing; skip versions the client already has.
	if rv != "" && g.sent[uid] == rv {
		return
	}
	row := g.row(u)
	delete(g.deletes, row.UID)
	g.upserts[row.UID] = row
	g.sent[uid] = rv
}

// markSent records the snapshot so replayed events are not sent twice.
func (g *gridSub) markSent(objs []*unstructured.Unstructured) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, o := range objs {
		uid := string(o.GetUID())
		g.sent[uid] = o.GetResourceVersion()
		if r, ok := g.upserts[uid]; ok && r.UID == uid {
			delete(g.upserts, uid)
		}
	}
}

func (g *gridSub) OnDelete(u *unstructured.Unstructured) {
	uid := string(u.GetUID())
	if uid == "" {
		uid = u.GetNamespace() + "/" + u.GetName()
	}
	g.mu.Lock()
	delete(g.upserts, uid)
	delete(g.sent, uid)
	g.deletes[uid] = struct{}{}
	g.mu.Unlock()
}

func (g *gridSub) OnError(err error) {
	g.mu.Lock()
	g.errMsg = err.Error()
	g.mu.Unlock()
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

func (g *gridSub) drain() (ups []project.Row, dels []string, errMsg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.upserts {
		ups = append(ups, r)
	}
	for uid := range g.deletes {
		dels = append(dels, uid)
	}
	errMsg, g.errMsg = g.errMsg, ""
	g.upserts, g.deletes = map[string]project.Row{}, map[string]struct{}{}
	return
}

type gridMsg struct {
	Type    string           `json:"type"`
	Columns []project.Column `json:"columns,omitempty"`
	Rows    []project.Row    `json:"rows,omitempty"`
	Deletes []string         `json:"deletes,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	res, ok := sess.Lookup(q.Get("group"), q.Get("resource"))
	if !ok {
		httpError(w, http.StatusNotFound, "unknown resource")
		return
	}
	ns := q.Get("namespace")
	if !res.Namespaced {
		ns = ""
	}
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()

	key := cluster.Key{GVR: res.GVR(), Namespace: ns}
	printer := printerFor(ctx, sess, res)
	sub := newGridSub(printer)
	snapCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	objs, err := sess.Informers.Subscribe(snapCtx, key, sub)
	cancel()
	if err != nil {
		_ = wsjson.Write(ctx, c, gridMsg{Type: "error", Error: err.Error()})
		c.Close(websocket.StatusNormalClosure, "")
		return
	}
	defer sess.Informers.Unsubscribe(key, sub)

	rows := make([]project.Row, 0, len(objs))
	for _, o := range objs {
		rows = append(rows, sub.row(o))
	}
	sub.markSent(objs)
	cols := project.Columns(res.Group, res.Kind, res.Namespaced)
	if printer != nil {
		cols = printer.Columns()
	}
	if err := wsjson.Write(ctx, c, gridMsg{Type: "snapshot", Columns: cols, Rows: rows}); err != nil {
		return
	}
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-sub.wake:
		}
		ups, dels, errMsg := sub.drain()
		if errMsg != "" {
			if err := wsjson.Write(ctx, c, gridMsg{Type: "error", Error: errMsg}); err != nil {
				return
			}
		}
		if len(ups) == 0 && len(dels) == 0 {
			continue
		}
		if err := wsjson.Write(ctx, c, gridMsg{Type: "delta", Rows: ups, Deletes: dels}); err != nil {
			return
		}
	}
}

// wsWriter sends every Write as one WebSocket message.
type wsWriter struct {
	ctx context.Context
	c   *websocket.Conn
	typ websocket.MessageType
}

func (w wsWriter) Write(p []byte) (int, error) {
	if err := w.c.Write(w.ctx, w.typ, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	tail, _ := strconv.ParseInt(q.Get("tail"), 10, 64)
	if tail == 0 {
		tail = 500
	}
	o := ops.LogOptions{Namespace: q.Get("namespace"), Pod: q.Get("pod"), Container: q.Get("container"),
		Follow: q.Get("follow") != "0", Previous: q.Get("previous") == "1", Timestamps: q.Get("timestamps") == "1", TailLines: tail}
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, done := within(c.CloseRead(r.Context()), sess.Life())
	defer done()
	if err := ops.StreamLogs(ctx, sess, o, wsWriter{ctx, c, websocket.MessageText}); err != nil {
		_ = c.Write(ctx, websocket.MessageText, []byte("\r\n[Leme] "+err.Error()+"\r\n"))
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// sizeQueue feeds terminal resizes to remotecommand.
type sizeQueue struct {
	ch chan remotecommand.TerminalSize
}

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	s, ok := <-q.ch
	if !ok {
		return nil
	}
	return &s
}

func (s *Server) exec(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	// Locking the cluster (or revoking the shell permission) or disconnecting ends the shell.
	ctx, cancel := within(r.Context(), sess.ShellScope())
	defer cancel()

	stdinR, stdinW := io.Pipe()
	defer stdinR.Close() // unblocks the reader goroutine if exec returns first
	sizes := sizeQueue{ch: make(chan remotecommand.TerminalSize, 4)}
	go func() {
		defer stdinW.Close()
		defer close(sizes.ch)
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := stdinW.Write(data); err != nil {
					return
				}
				continue
			}
			var msg struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				select {
				case sizes.ch <- remotecommand.TerminalSize{Width: msg.Cols, Height: msg.Rows}:
				default:
				}
			}
		}
	}()
	err = ops.Exec(ctx, sess, ops.ExecOptions{Namespace: q.Get("namespace"), Pod: q.Get("pod"), Container: q.Get("container"),
		Stdin: stdinR, Stdout: wsWriter{ctx, c, websocket.MessageBinary}, Sizes: sizes})
	exit := map[string]string{"type": "exit"}
	if err != nil {
		exit["error"] = err.Error()
	}
	b, _ := json.Marshal(exit)
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	_ = c.Write(wctx, websocket.MessageText, b)
	c.Close(websocket.StatusNormalClosure, "")
}
