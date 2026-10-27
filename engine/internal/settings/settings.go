// Package settings persists small per-user preferences of the engine (never credentials).
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Prometheus describes how to reach a Prometheus-compatible API for one context.
type Prometheus struct {
	// Mode is "service" (API server service proxy) or "url" (direct). Empty means auto-detect.
	Mode      string `json:"mode,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Service   string `json:"service,omitempty"`
	Port      string `json:"port,omitempty"`   // number or port name
	Scheme    string `json:"scheme,omitempty"` // http or https (service mode)
	Path      string `json:"path,omitempty"`   // path prefix, e.g. /prometheus
	URL       string `json:"url,omitempty"`
	// SendToken forwards the kubeconfig bearer token to a direct URL (e.g. OpenShift Thanos route).
	SendToken bool `json:"sendToken,omitempty"`
}

// AI configures the Copilot. API keys are never stored here (the desktop shell keeps them in the
// OS keychain and hands them to the engine in memory).
type AI struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"` // anthropic | openai | ollama
	Model    string `json:"model,omitempty"`
	BaseURL  string `json:"baseURL,omitempty"` // empty = the provider's official endpoint
	Effort   string `json:"effort,omitempty"`  // anthropic only: low | medium | high | xhigh | max
	// Anonymize replaces cluster identifiers before they leave the machine (nil = provider default).
	Anonymize *bool `json:"anonymize,omitempty"`
	// Consented lists provider endpoints the user agreed to send cluster data to ("provider|baseURL").
	Consented []string `json:"consented,omitempty"`
}

// AgentAccess controls what MCP agents may change in one kubeconfig context.
type AgentAccess struct {
	Write bool `json:"write,omitempty"`
	// ProdConfirmed records that the user typed the context name to allow writes on production.
	ProdConfirmed bool `json:"prodConfirmed,omitempty"`
}

// Data is the persisted document.
type Data struct {
	// Prometheus is keyed by kubeconfig context name.
	Prometheus map[string]Prometheus `json:"prometheus,omitempty"`
	AI         *AI                   `json:"ai,omitempty"`
	// Agents is keyed by kubeconfig context name.
	Agents map[string]AgentAccess `json:"agents,omitempty"`
}

// Store reads and writes settings.json.
type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

// Dir returns the config directory: $LEME_CONFIG_DIR or <user config dir>/leme.
func Dir() (string, error) {
	if d := os.Getenv("LEME_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "leme"), nil
}

// Open loads the store from dir (missing file = empty settings).
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "settings.json")}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Prometheus returns the saved config for a context.
func (s *Store) Prometheus(context string) (Prometheus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Prometheus[context]
	return p, ok
}

// SetPrometheus saves (or with an empty Mode, clears) the config for a context.
func (s *Store) SetPrometheus(context string, p Prometheus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Prometheus == nil {
		s.data.Prometheus = map[string]Prometheus{}
	}
	if p.Mode == "" {
		delete(s.data.Prometheus, context)
	} else {
		s.data.Prometheus[context] = p
	}
	return s.save()
}

// AI returns the Copilot configuration (zero value when never configured).
func (s *Store) AI() AI {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.AI == nil {
		return AI{}
	}
	a := *s.data.AI
	a.Consented = append([]string(nil), a.Consented...)
	return a
}

// SetAI saves the Copilot configuration.
func (s *Store) SetAI(a AI) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.AI = &a
	return s.save()
}

// AgentAccess re-reads the file first: the MCP server runs in another process and must see
// changes made in the app immediately.
func (s *Store) AgentAccess(context string) AgentAccess {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return s.data.Agents[context]
}

// SetAgentAccess saves (or with Write=false, clears) what agents may do in a context.
func (s *Store) SetAgentAccess(context string, a AgentAccess) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	if s.data.Agents == nil {
		s.data.Agents = map[string]AgentAccess{}
	}
	if !a.Write {
		delete(s.data.Agents, context)
	} else {
		s.data.Agents[context] = a
	}
	return s.save()
}

// reload picks up changes written by another process; on any error the in-memory copy stays.
func (s *Store) reload() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var d Data
	if json.Unmarshal(b, &d) == nil {
		s.data = d
	}
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
