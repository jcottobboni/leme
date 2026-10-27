package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// Env is everything the tools depend on. Tests replace Contexts and Connect with fakes.
type Env struct {
	Contexts func() ([]kube.Context, error)
	Connect  func(ctx context.Context, kc kube.Context) (*cluster.Session, error)
	Settings *settings.Store
	Audit    *audit.Log
	Changes  *change.Store
	Version  string

	mu       sync.Mutex
	sessions map[string]*cluster.Session
}

// NewEnv wires the real kubeconfig loader and the settings the desktop app writes.
func NewEnv(kubeconfigs []string, dir, version string) (*Env, error) {
	store, err := settings.Open(dir)
	if err != nil {
		return nil, err
	}
	// Same contexts as the app: the kubeconfig files and folders added there are in settings.json.
	loader := kube.NewLoader(kubeconfigs)
	loader.SetSources(store.Kubeconfigs())
	mgr := cluster.NewManager(loader)
	return &Env{
		Contexts: mgr.Loader().Contexts,
		Connect: func(ctx context.Context, kc kube.Context) (*cluster.Session, error) {
			return mgr.Connect(ctx, kc.ID)
		},
		Settings: store,
		Audit:    audit.Open(dir),
		Changes:  change.NewStore(),
		Version:  version,
	}, nil
}

// resolve finds a context by name (or id); empty means the kubeconfig's current-context. Sessions
// are opened lazily and reused.
func (e *Env) resolve(ctx context.Context, name string) (*cluster.Session, error) {
	list, err := e.Contexts()
	if err != nil {
		return nil, err
	}
	var kc *kube.Context
	for i := range list {
		switch {
		case name != "" && (list[i].Name == name || list[i].ID == name):
			kc = &list[i]
		case name == "" && list[i].Current:
			kc = &list[i]
		}
		if kc != nil {
			break
		}
	}
	if kc == nil {
		if name == "" {
			return nil, errors.New("the kubeconfig has no current-context: pass the context argument (see list_contexts)")
		}
		return nil, fmt.Errorf("unknown context %q (see list_contexts)", name)
	}
	e.mu.Lock()
	if s, ok := e.sessions[kc.ID]; ok {
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := e.Connect(cctx, *kc)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to context %q: %w", kc.Name, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessions == nil {
		e.sessions = map[string]*cluster.Session{}
	}
	e.sessions[kc.ID] = s
	return s, nil
}

// writesAllowed is the single gate for dry-run and apply: the user must have enabled agent writes
// for the context in the app, and for production also typed the context name there. It is read
// from disk on every call, so disabling it in the app takes effect immediately.
func (e *Env) writesAllowed(kc kube.Context) (bool, string) {
	a := e.Settings.AgentAccess(kc.Name)
	switch {
	case a.Write && !a.Granted(kc.Fingerprint()):
		return false, "agent writes were enabled for context " + kc.Name + " when it pointed to another cluster, server or user; the user must enable them again in the Leme app (cluster menu > \"Acesso de agentes (MCP)…\")."
	case !a.Write:
		return false, "agent writes are disabled for context " + kc.Name + ". The user can enable them in the Leme app: cluster menu > \"Acesso de agentes (MCP)…\" > \"Permitir escrita por agentes neste contexto\"."
	case kc.Prod && !a.ProdConfirmed:
		return false, "context " + kc.Name + " looks like production and the user has not confirmed agent writes for it. This must be confirmed in the Leme app (cluster menu > \"Acesso de agentes (MCP)…\") by typing the context name."
	}
	return true, ""
}

func (e *Env) audit(en audit.Entry) {
	en.Source = "mcp"
	_ = e.Audit.Add(en)
}

func serverHost(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
}
