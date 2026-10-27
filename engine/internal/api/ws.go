package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
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
	sel     labels.Selector  // M7: server-side label filter (`-l` in the grid search); nil = everything
	jp      *jpCols          // M7: JSONPath columns the user added; nil = none
}

func newGridSub(p *project.Printer) *gridSub {
	return &gridSub{sent: map[string]string{}, upserts: map[string]project.Row{}, deletes: map[string]struct{}{}, wake: make(chan struct{}, 1), printer: p}
}

// matches reports whether an object passes the label selector of this subscription.
func (g *gridSub) matches(u *unstructured.Unstructured) bool {
	return g.sel == nil || g.sel.Matches(labels.Set(u.GetLabels()))
}

// row projects an object, with the CRD printer columns and the JSONPath columns when there are any.
func (g *gridSub) row(u *unstructured.Unstructured) project.Row {
	r := project.Project(u)
	if g.printer != nil {
		g.printer.Apply(u, &r)
	}
	if g.jp != nil {
		g.jp.Apply(u, &r)
	}
	return r
}

func (g *gridSub) OnUpsert(u *unstructured.Unstructured) {
	uid := string(u.GetUID())
	rv := u.GetResourceVersion()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.matches(u) {
		// Relabeled out of the selector: the client drops it.
		if _, had := g.sent[uid]; had {
			delete(g.sent, uid)
			delete(g.upserts, uid)
			g.deletes[uid] = struct{}{}
		}
		return
	}
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
	// ColErrors: JSONPath columns that do not parse or were switched off, per column key (M7).
	ColErrors map[string]string `json:"colErrors,omitempty"`
}

// gridFilter reads the M7 watch parameters: `labelSelector` (k8s syntax) and `cols` (a JSON array
// of JSONPath expressions). Errors are user-facing.
func gridFilter(q map[string][]string, res cluster.Resource) (labels.Selector, *jpCols, error) {
	var sel labels.Selector
	if raw := first(q["labelSelector"]); raw != "" {
		s, err := labels.Parse(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("seletor de label inválido: %v", err)
		}
		if !s.Empty() {
			sel = s
		}
	}
	jp, err := parseJPCols(first(q["cols"]), res.Group == "" && res.Kind == "Secret")
	if err != nil {
		return nil, nil, err
	}
	return sel, jp, nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
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

	sel, jp, ferr := gridFilter(q, res)
	if ferr != nil {
		// Keep the socket open with the error: closing would only make the client reconnect in a loop.
		_ = wsjson.Write(ctx, c, gridMsg{Type: "error", Error: ferr.Error()})
		<-ctx.Done()
		return
	}
	key := cluster.Key{GVR: res.GVR(), Namespace: ns}
	printer := printerFor(ctx, sess, res)
	sub := newGridSub(printer)
	sub.sel, sub.jp = sel, jp
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
	matched := objs[:0:0]
	for _, o := range objs {
		if !sub.matches(o) {
			continue
		}
		matched = append(matched, o)
		rows = append(rows, sub.row(o))
	}
	sub.markSent(matched)
	cols := project.Columns(res.Group, res.Kind, res.Namespaced)
	if printer != nil {
		cols = printer.Columns()
	}
	var colErrs map[string]string
	if jp != nil {
		cols = withJPColumns(cols, jp.Columns())
		colErrs = jp.Errors()
	}
	if err := wsjson.Write(ctx, c, gridMsg{Type: "snapshot", Columns: cols, Rows: rows, ColErrors: colErrs}); err != nil {
		return
	}
	reported := len(colErrs)
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
		var newErrs map[string]string
		if jp != nil {
			// A JSONPath column switched off while running (too slow) is reported once.
			if e := jp.Errors(); len(e) != reported {
				newErrs, reported = e, len(e)
			}
		}
		if len(ups) == 0 && len(dels) == 0 && newErrs == nil {
			continue
		}
		if err := wsjson.Write(ctx, c, gridMsg{Type: "delta", Rows: ups, Deletes: dels, ColErrors: newErrs}); err != nil {
			return
		}
	}
}

// withJPColumns puts the JSONPath columns before Age (last), like kubectl custom columns.
func withJPColumns(cols, extra []project.Column) []project.Column {
	if len(extra) == 0 {
		return cols
	}
	out := make([]project.Column, 0, len(cols)+len(extra))
	at := len(cols)
	if at > 0 && cols[at-1].Key == "age" {
		at--
	}
	out = append(out, cols[:at]...)
	out = append(out, extra...)
	return append(out, cols[at:]...)
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
