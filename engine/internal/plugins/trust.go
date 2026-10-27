package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// hashAction fingerprints what an action runs and where it is offered: trusting it covers exactly
// this command line, these scopes and this title.
// The source file is part of it, so another file reusing the id does not inherit the trust.
func hashAction(a Action) string {
	b, _ := json.Marshal(struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Mode    string   `json:"mode"`
		Mutates bool     `json:"mutates"`
		Confirm bool     `json:"confirm"`
		Scopes  []string `json:"scopes"`
		Title   string   `json:"title"`
		Source  string   `json:"source"`
	}{a.Command, a.Args, a.Mode, a.Mutates, a.Confirm, a.Scopes, a.Title, a.Source})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Trust stores the actions the user approved: id -> hash of the approved definition.
type Trust struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// OpenTrust reads <dir>/plugins-trust.json (missing = nothing trusted).
func OpenTrust(dir string) *Trust {
	t := &Trust{path: filepath.Join(dir, "plugins-trust.json"), m: map[string]string{}}
	if b, err := os.ReadFile(t.path); err == nil {
		_ = json.Unmarshal(b, &t.m)
	}
	return t
}

// Trusted reports whether this exact definition was approved.
func (t *Trust) Trusted(a Action) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[a.ID] != "" && t.m[a.ID] == a.Hash
}

// ErrStale is returned when the definition changed after the user saw it.
var ErrStale = errors.New("a definição da ação mudou: revise o comando de novo")

// Grant approves an action; hash must be the one the user was shown.
func (t *Trust) Grant(a Action, hash string) error {
	if hash != a.Hash {
		return ErrStale
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[a.ID] = a.Hash
	return t.save()
}

// Revoke forgets an approval.
func (t *Trust) Revoke(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, id)
	return t.save()
}

func (t *Trust) save() error {
	if err := os.MkdirAll(filepath.Dir(t.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t.m, "", "  ")
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, t.path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}
