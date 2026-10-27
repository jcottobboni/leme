package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jcottobboni/leme/engine/internal/ai"
	"github.com/jcottobboni/leme/engine/internal/anonymize"
	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// aiState holds the Copilot's in-memory state. API keys are handed over by the desktop shell and
// never written to disk by the engine nor returned by any endpoint. A key is bound to the endpoint
// it was entered for, so pointing the settings at another server never sends it there.
type aiState struct {
	mu    sync.Mutex
	keys  map[string]string // provider|baseURL -> key
	convs map[string]*conversation
}

// conversation keeps the anonymization aliases of one Copilot conversation: the UI resends the
// history with real names, and names learned in earlier turns (tool results) must stay hidden.
type conversation struct {
	mu      sync.Mutex
	cluster string
	an      *anonymize.Map
	used    time.Time
}

const conversationTTL = 2 * time.Hour

func newAIState() *aiState {
	return &aiState{keys: map[string]string{}, convs: map[string]*conversation{}}
}

// conversation returns the conversation's alias map, locked; the caller must unlock it.
// fresh is true when no earlier turn is known (new conversation, engine restart, setting changed).
func (a *aiState) conversation(id, cluster string, anonymizeOn bool) (c *conversation, fresh bool, err error) {
	a.mu.Lock()
	now := time.Now()
	for k, v := range a.convs {
		if now.Sub(v.used) > conversationTTL {
			delete(a.convs, k)
		}
	}
	c = a.convs[id]
	if c == nil || c.cluster != cluster || c.an.Enabled() != anonymizeOn || id == "" {
		c = &conversation{cluster: cluster, an: anonymize.New(anonymizeOn)}
		if id != "" {
			a.convs[id] = c
		}
		fresh = true
	}
	c.used = now
	a.mu.Unlock()
	if !c.mu.TryLock() {
		return nil, false, errors.New("esta conversa já tem uma pergunta em andamento")
	}
	return c, fresh, nil
}

func endpointKey(provider, baseURL string) string {
	return provider + "|" + strings.TrimRight(baseURL, "/")
}

func (a *aiState) setKey(provider, baseURL, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if key == "" {
		delete(a.keys, endpointKey(provider, baseURL))
	} else {
		a.keys[endpointKey(provider, baseURL)] = key
	}
}

// key returns the key for an endpoint: the one the shell handed over, else environment variables
// for the browser mode (`leme serve`).
func (a *aiState) key(provider, baseURL string) string {
	a.mu.Lock()
	k := a.keys[endpointKey(provider, baseURL)]
	a.mu.Unlock()
	if k != "" {
		return k
	}
	// Environment keys only go to the provider's official endpoint, or to the endpoint named in
	// LEME_AI_BASE_URL: a key must never follow a base URL changed through the API.
	if v := os.Getenv("LEME_AI_API_KEY"); v != "" && strings.TrimRight(baseURL, "/") == strings.TrimRight(os.Getenv("LEME_AI_BASE_URL"), "/") {
		return v
	}
	if baseURL == "" {
		switch provider {
		case ai.Anthropic:
			return os.Getenv("ANTHROPIC_API_KEY")
		case ai.OpenAI:
			return os.Getenv("OPENAI_API_KEY")
		}
	}
	return ""
}

func (s *Server) aiRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/ai/settings", s.aiSettings)
	m.HandleFunc("PUT /api/ai/settings", s.aiSetSettings)
	m.HandleFunc("POST /api/ai/keys", s.aiSetKey)
	m.HandleFunc("POST /api/ai/test", s.aiTest)
	m.HandleFunc("GET /api/audit", s.auditList)
	m.HandleFunc("POST /api/clusters/{id}/ai/preview", s.withSession(s.aiPreview))
	m.HandleFunc("GET /api/clusters/{id}/ai/chat", s.withSession(s.aiChat))
	m.HandleFunc("POST /api/clusters/{id}/changes/{token}/apply", s.withSession(s.applyChange))
}

func isLocalEndpoint(provider, baseURL string) bool {
	if baseURL == "" {
		return provider == ai.Ollama
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// anonymizeOn: on by default for cloud providers, off for local models; the user can override.
func anonymizeOn(a settings.AI) bool {
	if a.Anonymize != nil {
		return *a.Anonymize
	}
	return !isLocalEndpoint(a.Provider, a.BaseURL)
}

func (s *Server) aiSettings(w http.ResponseWriter, r *http.Request) {
	a := s.settings.AI()
	if a.Provider == "" {
		a.Provider = ai.Anthropic
	}
	keys := map[string]bool{}
	for _, p := range []string{ai.Anthropic, ai.OpenAI, ai.Ollama} {
		base := ""
		if p == a.Provider {
			base = a.BaseURL
		}
		keys[p] = s.ai.key(p, base) != "" || p == ai.Ollama
	}
	writeJSON(w, map[string]any{
		"ai": a, "keys": keys, "defaultModels": ai.DefaultModel,
		"anonymize": anonymizeOn(a), "local": isLocalEndpoint(a.Provider, a.BaseURL),
		"consented": isLocalEndpoint(a.Provider, a.BaseURL) || slices.Contains(a.Consented, endpointKey(a.Provider, a.BaseURL)),
	})
}

var efforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func (s *Server) aiSetSettings(w http.ResponseWriter, r *http.Request) {
	var a settings.AI
	if !decode(w, r, &a) {
		return
	}
	if a.Provider != ai.Anthropic && a.Provider != ai.OpenAI && a.Provider != ai.Ollama {
		httpError(w, http.StatusBadRequest, "unknown provider")
		return
	}
	if !efforts[a.Effort] {
		httpError(w, http.StatusBadRequest, "invalid effort")
		return
	}
	if a.BaseURL != "" {
		u, err := url.Parse(a.BaseURL)
		if err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && isLocalEndpoint(a.Provider, a.BaseURL))) || u.Host == "" {
			httpError(w, http.StatusBadRequest, "the endpoint must be an https URL (http only for localhost)")
			return
		}
	}
	// Consent can only be added by the explicit consent call, never dropped silently by an edit.
	old := s.settings.AI()
	a.Consented = old.Consented
	if err := s.settings.SetAI(a); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.aiSettings(w, r)
}

func (s *Server) aiSetKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"baseURL"`
		Key      string `json:"key"`
		Consent  bool   `json:"consent"` // records consent for the current endpoint instead of a key
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Consent {
		a := s.settings.AI()
		k := endpointKey(a.Provider, a.BaseURL)
		if !slices.Contains(a.Consented, k) {
			a.Consented = append(a.Consented, k)
		}
		if err := s.settings.SetAI(a); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	s.ai.setKey(body.Provider, body.BaseURL, strings.TrimSpace(body.Key))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) aiConfig() (ai.Config, settings.AI, error) {
	a := s.settings.AI()
	if a.Provider == "" {
		a.Provider = ai.Anthropic
	}
	cfg := ai.Config{Provider: a.Provider, Model: a.Model, BaseURL: a.BaseURL, Effort: a.Effort, APIKey: s.ai.key(a.Provider, a.BaseURL)}
	if cfg.Provider == ai.Anthropic && cfg.Effort == "" {
		cfg.Effort = "medium"
	}
	if cfg.APIKey == "" && cfg.Provider == ai.Anthropic {
		return cfg, a, errors.New("configure a chave de API do Claude em Configurações de IA")
	}
	return cfg, a, nil
}

func (s *Server) aiTest(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := s.aiConfig()
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.MaxTokens = 2048
	conv, err := ai.Start(cfg, "Responda exatamente: OK", nil, "Teste de conexão.", nil)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var text strings.Builder
	if _, err := ai.Run(ctx, conv, nil, 1, ai.Hooks{OnText: func(t string) { text.WriteString(t) }}); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]string{"text": strings.TrimSpace(text.String()), "model": cfg.Model})
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	l, err := s.audit.Recent(200)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"entries": l})
}

// chatRequest is the single message a client sends on the chat socket (and the preview body).
type chatRequest struct {
	Conversation string      `json:"conversation"`
	History      []ai.Turn   `json:"history"`
	Question     string      `json:"question"`
	Ref          *copilotRef `json:"ref,omitempty"`
	AllowLogs    bool        `json:"allowLogs"`
	LogLines     int         `json:"logLines"`
}

func (s *Server) aiPreview(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	var req chatRequest
	if !decode(w, r, &req) {
		return
	}
	_, a, _ := s.aiConfig()
	conv, _, err := s.ai.conversation(req.Conversation, sess.Context.ID, anonymizeOn(a))
	if err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	defer conv.mu.Unlock()
	an := conv.an
	user, err := s.copilotPrompt(r.Context(), sess, &req, an)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"system": copilotSystem, "user": user, "anonymized": an.Enabled(), "bytes": len(copilotSystem) + len(user), "provider": a.Provider, "endpoint": a.BaseURL})
}

// chatEvent is streamed to the UI. Text is always restored (real names); the provider only ever
// sees the anonymized version.
type chatEvent struct {
	Type     string `json:"type"` // meta | text | tool | proposal | usage | refusal | consent | error | done
	Text     string `json:"text,omitempty"`
	Name     string `json:"name,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Proposal any    `json:"proposal,omitempty"`
	Input    int64  `json:"input,omitempty"`
	Output   int64  `json:"output,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Anon     bool   `json:"anonymized,omitempty"`
}

func (s *Server) aiChat(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	var mu sync.Mutex
	send := func(e chatEvent) {
		mu.Lock()
		defer mu.Unlock()
		_ = wsjson.Write(ctx, c, e)
	}
	var req chatRequest
	if err := wsjson.Read(ctx, c, &req); err != nil {
		return
	}
	// Reading the socket afterwards only detects the client going away (cancel button / closed tab).
	go func() {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()
	finish := func() { send(chatEvent{Type: "done"}); c.Close(websocket.StatusNormalClosure, "") }

	cfg, a, err := s.aiConfig()
	if err != nil {
		send(chatEvent{Type: "error", Text: err.Error()})
		finish()
		return
	}
	if !a.Enabled {
		send(chatEvent{Type: "error", Text: "O Copilot está desligado. Ative em Configurações de IA."})
		finish()
		return
	}
	if !isLocalEndpoint(a.Provider, a.BaseURL) && !slices.Contains(a.Consented, endpointKey(a.Provider, a.BaseURL)) {
		send(chatEvent{Type: "consent", Provider: a.Provider, Text: a.BaseURL})
		finish()
		return
	}
	cv, fresh, err := s.ai.conversation(req.Conversation, sess.Context.ID, anonymizeOn(a))
	if err != nil {
		send(chatEvent{Type: "error", Text: err.Error()})
		finish()
		return
	}
	defer cv.mu.Unlock()
	an := cv.an
	user, err := s.copilotPrompt(ctx, sess, &req, an)
	if err != nil {
		send(chatEvent{Type: "error", Text: err.Error()})
		finish()
		return
	}
	if fresh && an.Enabled() {
		// The aliases of earlier turns are gone: real names in that history would go out in clear.
		req.History = nil
	}
	history := make([]ai.Turn, 0, len(req.History))
	for _, h := range req.History {
		if h.Role == "user" || h.Role == "assistant" {
			history = append(history, ai.Turn{Role: h.Role, Text: an.Text(h.Text)})
		}
	}
	tools := s.copilotTools(sess, &req, an, send)
	defs := make([]ai.ToolDef, len(tools))
	for i, t := range tools {
		defs[i] = t.Def
	}
	conv, err := ai.Start(cfg, copilotSystem, history, user, defs)
	if err != nil {
		send(chatEvent{Type: "error", Text: err.Error()})
		finish()
		return
	}
	model := cfg.Model
	if model == "" {
		model = ai.DefaultModel[cfg.Provider]
	}
	send(chatEvent{Type: "meta", Provider: cfg.Provider, Model: model, Anon: an.Enabled()})
	restorer := an.NewRestorer()
	entry := audit.Entry{Source: "copilot", Action: "ask", Context: sess.Context.Name, Provider: cfg.Provider, Model: model, Bytes: len(copilotSystem) + len(user)}
	if req.Ref != nil {
		entry.Object = req.Ref.Kind + "/" + req.Ref.Namespace + "/" + req.Ref.Name
	}
	res, err := ai.Run(ctx, conv, tools, 8, ai.Hooks{
		OnText: func(d string) {
			if out := restorer.Push(d); out != "" {
				send(chatEvent{Type: "text", Text: out})
			}
		},
		OnTool: func(call ai.ToolCall) {
			if out := restorer.Flush(); out != "" {
				send(chatEvent{Type: "text", Text: out})
			}
			send(chatEvent{Type: "tool", Name: call.Name, Detail: an.Restore(string(call.Input))})
		},
	})
	if out := restorer.Flush(); out != "" {
		send(chatEvent{Type: "text", Text: out})
	}
	switch {
	case err != nil:
		entry.Result, entry.Detail = "error", truncate(err.Error(), 200)
		send(chatEvent{Type: "error", Text: err.Error()})
	case res.Refused:
		entry.Result, entry.Detail = "refused", res.RefusalCategory
		send(chatEvent{Type: "refusal", Text: res.RefusalCategory})
	default:
		entry.Result = "ok"
	}
	if res != nil {
		send(chatEvent{Type: "usage", Input: res.InputTokens, Output: res.OutputTokens})
	}
	s.auditAdd(entry)
	finish()
}

func (s *Server) applyChange(w http.ResponseWriter, r *http.Request, sess *cluster.Session) {
	tok := r.PathValue("token")
	p, err := s.changes.Apply(r.Context(), sess, tok, sess.CheckWritable)
	entry := audit.Entry{Source: "copilot", Action: "apply", Context: sess.Context.Name}
	if p != nil {
		entry.Object = p.Ref.String()
		if p.Source != "" {
			// Proposals from other integrations (Argo CD) are audited under their own source.
			entry.Source = p.Source
		}
	}
	if err != nil {
		entry.Result, entry.Detail = "denied", truncate(err.Error(), 200)
		s.auditAdd(entry)
		if errors.Is(err, cluster.ErrReadOnly) {
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		kubeError(w, err)
		return
	}
	entry.Result = "ok"
	s.auditAdd(entry)
	s.log.Info("change applied", "context", sess.Context.Name, "object", p.Ref.String(), "source", p.Source)
	writeJSON(w, map[string]any{"applied": true, "ref": p.Ref})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mustJSON renders a tool result compactly.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
