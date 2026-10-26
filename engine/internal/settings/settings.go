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

// Data is the persisted document.
type Data struct {
	// Prometheus is keyed by kubeconfig context name.
	Prometheus map[string]Prometheus `json:"prometheus,omitempty"`
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
