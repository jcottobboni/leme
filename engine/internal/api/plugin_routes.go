package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/coder/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/plugins"
)

// pluginRoutes serves declarative plugins (M8): listing, trust, settings, runs and CRD view fields.
func (s *Server) pluginRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/plugins", s.pluginList)
	m.HandleFunc("POST /api/plugins/reload", func(w http.ResponseWriter, r *http.Request) {
		s.plugins.Reload()
		s.pluginList(w, r)
	})
	m.HandleFunc("POST /api/plugins/trust", s.pluginTrust)
	m.HandleFunc("DELETE /api/plugins/trust/{plugin}/{action}", s.pluginRevoke)
	m.HandleFunc("PUT /api/plugins/settings", s.pluginSettings)
	m.HandleFunc("POST /api/clusters/{id}/plugins/run", s.withSession(s.pluginRun))
	m.HandleFunc("GET /api/clusters/{id}/plugins/term", s.withSession(s.pluginTerm))
	m.HandleFunc("GET /api/clusters/{id}/plugins/fields", s.withSession(s.pluginFields))
}

// pluginSource tags audit entries of plugin runs.
const pluginSource = "plugin"

type pluginActionView struct {
	plugins.Action
	Trusted bool `json:"trusted"`
}

type pluginViewInfo struct {
	Plugin  string   `json:"plugin"`
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	Columns []string `json:"columns"`
}

type pluginRuleInfo struct {
	Plugin   string   `json:"plugin"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Kinds    []string `json:"kinds"`
}

func (s *Server) pluginList(w http.ResponseWriter, r *http.Request) {
	snap := s.plugins.Current()
	_ = os.MkdirAll(snap.Dir, 0o700) // so "open folder" always has somewhere to go
	acts := make([]pluginActionView, 0, len(snap.Actions))
	for _, a := range snap.Actions {
		acts = append(acts, pluginActionView{Action: a, Trusted: s.pluginTrustStore.Trusted(a)})
	}
	views := make([]pluginViewInfo, 0, len(snap.Views))
	for _, v := range snap.Views {
		cols := []string{}
		for _, c := range v.Columns() {
			cols = append(cols, c.Label)
		}
		views = append(views, pluginViewInfo{Plugin: v.Plugin, Group: v.Group, Kind: v.Kind, Columns: cols})
	}
	rules := make([]pluginRuleInfo, 0, len(snap.Rules))
	for _, rl := range snap.Rules {
		rules = append(rules, pluginRuleInfo{Plugin: rl.Plugin, ID: rl.Spec.ID, Title: rl.Spec.Title, Severity: rl.Spec.Severity, Kinds: rl.Spec.Kinds})
	}
	writeJSON(w, map[string]any{"dir": snap.Dir, "k9s": snap.K9s, "files": snap.Files, "actions": acts, "links": snap.Links, "views": views, "rules": rules})
}

type pluginTrustRequest struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

func (s *Server) pluginTrust(w http.ResponseWriter, r *http.Request) {
	var req pluginTrustRequest
	if !decode(w, r, &req) {
		return
	}
	a, ok := s.plugins.Current().Action(req.ID)
	if !ok {
		httpError(w, http.StatusNotFound, "ação não encontrada")
		return
	}
	if err := s.pluginTrustStore.Grant(a, req.Hash); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, plugins.ErrStale) {
			code = http.StatusConflict
		}
		httpError(w, code, err.Error())
		return
	}
	s.auditAdd(audit.Entry{Source: pluginSource, Action: "trust", Object: a.ID, Result: "ok", Detail: truncate(a.Command, 120)})
	writeJSON(w, map[string]bool{"trusted": true})
}

func (s *Server) pluginRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("plugin") + "/" + r.PathValue("action")
	if err := s.pluginTrustStore.Revoke(id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditAdd(audit.Entry{Source: pluginSource, Action: "revoke", Object: id, Result: "ok"})
	writeJSON(w, map[string]bool{"trusted": false})
}

func (s *Server) pluginSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		K9s bool `json:"k9s"`
	}
	if !decode(w, r, &req) {
		return
	}
	if s.settings == nil {
		httpError(w, http.StatusServiceUnavailable, "settings unavailable")
		return
	}
	if err := s.settings.SetPluginsK9s(req.K9s); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.plugins.Reload()
	s.pluginList(w, r)
}

type pluginRunRequest struct {
	Action string        `json:"action"`
	Input  plugins.Input `json:"input"`
}

// pluginAuthorize finds the action and checks trust and the write guard; entry is filled for the audit.
func (s *Server) pluginAuthorize(sess *cluster.Session, id string, in plugins.Input, entry *audit.Entry) (plugins.Action, int, error) {
	a, ok := s.plugins.Current().Action(id)
	if !ok {
		return a, http.StatusNotFound, errors.New("ação de plugin não encontrada (o arquivo mudou?)")
	}
	entry.Detail = a.ID + " · " + a.Mode
	if !s.pluginTrustStore.Trusted(a) {
		return a, http.StatusForbidden, errors.New("ação não confiável: revise e aprove o comando antes de executar")
	}
	if a.Mutates {
		if err := sess.CheckWritable(); err != nil {
			return a, http.StatusForbidden, errors.New("esta ação altera o cluster e ele está travado: destrave a escrita para executar")
		}
	}
	if in.Name == "" {
		return a, http.StatusBadRequest, errors.New("objeto não informado")
	}
	return a, 0, nil
}

func (s *Server) pluginTarget(sess *cluster.Session) plugins.Target {
	return plugins.Target{Config: sess.Config, Context: sess.Context.Name, Cluster: sess.Context.Cluster, User: sess.Context.User, Namespace: sess.Namespace,
		Policy: plugins.Policy{ReadOnly: sess.ReadOnly, Shell: sess.Shell}}
}

// pluginScope ends a mutating run when the cluster is locked again, any run on disconnect.
func pluginScope(sess *cluster.Session, a plugins.Action) context.Context {
	if a.Mutates {
		return sess.WriteScope()
	}
	return sess.Life()
}

func objectOf(in plugins.Input) string {
	if in.Namespace != "" {
		return in.Resource + "/" + in.Namespace + "/" + in.Name
	}
	return in.Resource + "/" + in.Name
}

func (s *Server) pluginRun(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req pluginRunRequest
	if !decode(w, r, &req) {
		return
	}
	entry := audit.Entry{Source: pluginSource, Action: "run", Context: sess.Context.Name, Object: objectOf(req.Input)}
	a, code, err := s.pluginAuthorize(sess, req.Action, req.Input, &entry)
	if err == nil && a.Mode == plugins.ModeTerminal {
		code, err = http.StatusBadRequest, errors.New("ação de terminal: use o painel de terminal")
	}
	if err != nil {
		entry.Result = "denied"
		entry.Detail = truncate(entry.Detail+": "+err.Error(), 200)
		s.auditAdd(entry)
		httpError(w, code, err.Error())
		return
	}
	ctx, cancel := within(r.Context(), pluginScope(sess, a))
	defer cancel()
	ctx, cancelT := context.WithTimeout(ctx, plugins.RunTimeout)
	defer cancelT()
	p, err := plugins.Prepare(ctx, a, req.Input, s.pluginTarget(sess))
	if err != nil {
		entry.Result, entry.Detail = "error", truncate(entry.Detail+": "+err.Error(), 200)
		s.auditAdd(entry)
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	res := plugins.Run(p)
	entry.Result = "ok"
	if res.ExitCode != 0 || res.Error != "" {
		entry.Result = "error"
	}
	entry.Detail += fmt.Sprintf(" · exit %d · %dms", res.ExitCode, res.Duration)
	s.auditAdd(entry)
	writeJSON(w, res)
}

// pluginTerm runs a terminal action over a WebSocket with the exec protocol: binary frames both
// ways, {"type":"resize"} from the UI, {"type":"info"|"exit"} from the engine.
func (s *Server) pluginTerm(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	var in plugins.Input
	if err := json.Unmarshal([]byte(q.Get("input")), &in); err != nil {
		httpError(w, http.StatusBadRequest, "input inválido")
		return
	}
	entry := audit.Entry{Source: pluginSource, Action: "run", Context: sess.Context.Name, Object: objectOf(in)}
	a, code, err := s.pluginAuthorize(sess, q.Get("action"), in, &entry)
	if err != nil {
		entry.Result = "denied"
		entry.Detail = truncate(entry.Detail+": "+err.Error(), 200)
		s.auditAdd(entry)
		httpError(w, code, err.Error())
		return
	}
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := within(r.Context(), pluginScope(sess, a))
	defer cancel()
	sendExit := func(code int, msg string) {
		b, _ := json.Marshal(map[string]any{"type": "exit", "code": code, "error": msg})
		wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer wcancel()
		_ = c.Write(wctx, websocket.MessageText, b)
		c.Close(websocket.StatusNormalClosure, "")
	}
	p, err := plugins.Prepare(ctx, a, in, s.pluginTarget(sess))
	if err != nil {
		entry.Result, entry.Detail = "error", truncate(entry.Detail+": "+err.Error(), 200)
		s.auditAdd(entry)
		sendExit(-1, err.Error())
		return
	}
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	if cols <= 0 || cols > 1000 {
		cols = 120
	}
	if rows <= 0 || rows > 1000 {
		rows = 32
	}
	t, err := plugins.StartTerminal(p, uint16(cols), uint16(rows))
	if err != nil {
		entry.Result, entry.Detail = "error", truncate(entry.Detail+": "+err.Error(), 200)
		s.auditAdd(entry)
		sendExit(-1, err.Error())
		return
	}
	defer t.Close()
	info, _ := json.Marshal(map[string]any{"type": "info", "pty": t.PTY()})
	_ = c.Write(ctx, websocket.MessageText, info)
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := t.Write(data); err != nil {
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
				_ = t.Resize(msg.Cols, msg.Rows)
			}
		}
	}()
	go func() {
		<-ctx.Done()
		_ = t.Close() // UI closed, cluster locked (mutating action) or disconnected
	}()
	out := wsWriter{ctx, c, websocket.MessageBinary}
	_, _ = io.Copy(out, t) // a PTY read fails with EIO once the command exits
	exit, werr := t.Wait()
	msg := ""
	if werr != nil {
		msg = werr.Error()
	}
	entry.Result = "ok"
	if exit != 0 || msg != "" {
		entry.Result = "error"
	}
	entry.Detail += fmt.Sprintf(" · exit %d", exit)
	s.auditAdd(entry)
	sendExit(exit, msg)
}

// pluginFields evaluates the detail fields of a CRD view for one object.
func (s *Server) pluginFields(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	q := r.URL.Query()
	res, ok := sess.Lookup(q.Get("group"), q.Get("resource"))
	if !ok {
		httpError(w, http.StatusNotFound, "unknown resource")
		return
	}
	v := s.plugins.Current().ViewFor(res.Group, res.Kind)
	if v == nil {
		writeJSON(w, map[string]any{"fields": []plugins.Field{}})
		return
	}
	var ri dynamic.ResourceInterface = sess.Dynamic.Resource(res.GVR())
	if res.Namespaced {
		ri = sess.Dynamic.Resource(res.GVR()).Namespace(q.Get("namespace"))
	}
	u, err := ri.Get(r.Context(), q.Get("name"), metav1.GetOptions{})
	if err != nil {
		kubeError(w, err)
		return
	}
	fields := v.Fields(u)
	writeJSON(w, map[string]any{"plugin": v.Plugin, "fields": fields})
}
