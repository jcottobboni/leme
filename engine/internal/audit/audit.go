// Package audit keeps a local, append-only record of the changes made through Leme and of what AI
// providers and MCP agents did: who asked, which object, which action and the outcome. It never
// stores prompts, responses, object contents or credentials.
package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Sources of entries.
const (
	SourceUI      = "ui"      // a change made by the user in the app (apply, delete, scale, Helm…)
	SourceCopilot = "copilot" // the in-app AI assistant
	SourceMCP     = "mcp"     // an external agent through `leme mcp`
	SourceApp     = "app"     // app settings that govern agents
)

// Entry is one audited action.
type Entry struct {
	Time     time.Time `json:"time"`
	Source   string    `json:"source"` // ui | copilot | mcp | app | argocd…
	Action   string    `json:"action"` // ask | propose | apply | delete | scale | restart | helm-upgrade…
	Context  string    `json:"context,omitempty"`
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Object   string    `json:"object,omitempty"` // Kind/namespace/name
	Result   string    `json:"result"`           // ok | denied | error
	Detail   string    `json:"detail,omitempty"` // short reason (no content)
	Bytes    int       `json:"bytes,omitempty"`  // size sent to the provider
}

// MaxSize is the size from which the log rotates: the current file becomes <name>.1 (replacing the
// previous one), so at most about twice this size is kept.
const MaxSize = 10 << 20

// maxLine bounds one entry when reading; longer lines are skipped.
const maxLine = 1 << 20

// Log appends entries to <dir>/audit.jsonl (0600).
type Log struct {
	path    string
	maxSize int64
	mu      sync.Mutex
}

// Open returns the audit log of a config directory.
func Open(dir string) *Log {
	return &Log{path: filepath.Join(dir, "audit.jsonl"), maxSize: MaxSize}
}

func (l *Log) old() string { return l.path + ".1" }

// Add appends an entry. Failures are returned so callers can log them; they never undo the action.
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
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	if st, err := os.Stat(l.path); err == nil && st.Size() > 0 && st.Size()+int64(len(b)) > l.maxSize {
		if err := os.Rename(l.path, l.old()); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Recent returns up to n of the newest entries, newest first. It reads the files from the end, so
// the cost depends on n, not on the size of the log.
func (l *Log) Recent(n int) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, n)
	for _, p := range []string{l.path, l.old()} {
		if len(out) >= n {
			break
		}
		got, err := tail(p, n-len(out))
		if err != nil {
			return out, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// tail returns up to n entries of a JSONL file, last line first. A missing file has none.
func tail(path string, n int) ([]Entry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const chunk = 64 << 10
	var out []Entry
	var rest []byte // start of a line whose beginning is in a chunk not read yet
	pos := st.Size()
	for pos > 0 && len(out) < n {
		size := int64(chunk)
		if size > pos {
			size = pos
		}
		pos -= size
		buf := make([]byte, size, int(size)+len(rest))
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return out, err
		}
		buf = append(buf, rest...)
		lines := bytes.Split(buf, []byte{'\n'})
		first := 0
		if pos > 0 {
			// The first piece may continue in the previous chunk.
			rest = append([]byte(nil), lines[0]...)
			if len(rest) > maxLine {
				rest = nil
			}
			first = 1
		}
		for i := len(lines) - 1; i >= first && len(out) < n; i-- {
			line := bytes.TrimSpace(lines[i])
			if len(line) == 0 {
				continue
			}
			var e Entry
			if json.Unmarshal(line, &e) == nil {
				out = append(out, e)
			}
		}
	}
	return out, nil
}
