// Package audit keeps a local, append-only record of what AI providers and MCP agents did: who
// asked, which object, which action and the outcome. It never stores prompts, responses, object
// contents or credentials.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one audited action.
type Entry struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"` // copilot | mcp
	Action   string    `json:"action"` // ask | propose | apply | read
	Context  string    `json:"context,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Object   string    `json:"object,omitempty"` // Kind/namespace/name
	Result   string    `json:"result"`           // ok | denied | error
	Detail   string    `json:"detail,omitempty"` // short reason (no content)
	Bytes    int       `json:"bytes,omitempty"`  // size sent to the provider
}

// Log appends entries to <dir>/audit.jsonl (0600).
type Log struct {
	path string
	mu   sync.Mutex
}

// Open returns the audit log of a config directory.
func Open(dir string) *Log { return &Log{path: filepath.Join(dir, "audit.jsonl")} }

// Add appends an entry. Failures are returned but never block the audited action's caller from
// reporting them.
func (l *Log) Add(e Entry) error {
	if l == nil {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// Recent returns up to n of the newest entries, newest first.
func (l *Log) Recent(n int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	out := make([]Entry, 0, n)
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out, sc.Err()
}
