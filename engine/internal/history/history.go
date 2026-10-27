// Package history keeps a local, persistent record of the changes observed by the informers a
// session already runs: creations, changes of the desired state (spec and other top-level fields,
// labels, annotations) and deletions, each with a JSON Patch of what changed. It outlives the TTL of
// Kubernetes events: one JSONL file per cluster (by kubeconfig fingerprint) under the config
// directory, 0600, kept for 7 days and at most 50 MB, compacted when opened.
//
// Values that must not be stored never are: Secrets and ConfigMaps record only which keys changed,
// and other objects go through the same redaction as data sent out of the machine (env values,
// credential-like fields and tokens masked).
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Defaults of a store.
const (
	DefaultMaxAge   = 7 * 24 * time.Hour
	DefaultMaxBytes = 50 << 20
	// maxLine bounds one record; a bigger change is stored with its operations truncated.
	maxLine = 64 << 10
)

// Event types.
const (
	Created = "created"
	Updated = "updated"
	Deleted = "deleted"
)

// Op is one change of a record. Value and Old are omitted when the value is hidden (KeysOnly).
type Op struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
	Old   any    `json:"old,omitempty"`
}

// Entry is one observed change.
type Entry struct {
	ID              int64     `json:"id"` // unique and increasing per cluster: the paging cursor
	Time            time.Time `json:"time"`
	Type            string    `json:"type"` // created | updated | deleted
	Group           string    `json:"group"`
	Version         string    `json:"version"`
	Resource        string    `json:"resource"`
	Kind            string    `json:"kind"`
	Namespace       string    `json:"namespace,omitempty"`
	Name            string    `json:"name"`
	UID             string    `json:"uid,omitempty"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	Ops             []Op      `json:"ops,omitempty"`
	// KeysOnly: the values were not recorded (Secret, ConfigMap), only the paths that changed.
	KeysOnly bool `json:"keysOnly,omitempty"`
	// Truncated: the change was too big; only the first operations were kept.
	Truncated bool `json:"truncated,omitempty"`
}

// Store holds the history files of every cluster under one directory.
type Store struct {
	dir      string
	maxAge   time.Duration
	maxBytes int64
	now      func() time.Time

	mu   sync.Mutex
	logs map[string]*clusterLog
}

// Open returns the store of a config directory (files in <dir>/history). An empty dir disables it.
func Open(dir string) *Store {
	s := &Store{maxAge: DefaultMaxAge, maxBytes: DefaultMaxBytes, now: time.Now, logs: map[string]*clusterLog{}}
	if dir != "" {
		s.dir = filepath.Join(dir, "history")
	}
	return s
}

// SetLimits changes retention and size (tests, settings).
func (s *Store) SetLimits(maxAge time.Duration, maxBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxAge, s.maxBytes = maxAge, maxBytes
}

// MaxAge is the retention.
func (s *Store) MaxAge() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxAge
}

var validKey = regexp.MustCompile(`^[a-f0-9]{8,64}$`)

// log returns the file of a cluster, compacting it the first time it is opened in this process.
func (s *Store) log(key string) (*clusterLog, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("history disabled")
	}
	if !validKey.MatchString(key) {
		return nil, errors.New("invalid history key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.logs[key]; ok {
		return l, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	l := &clusterLog{store: s, path: filepath.Join(s.dir, key+".jsonl"), seen: map[string]string{}}
	if err := l.compact(s.maxAge, s.maxBytes); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	s.logs[key] = l
	return l, nil
}

type clusterLog struct {
	store  *Store
	path   string
	mu     sync.Mutex
	size   int64
	lastID int64
	// seen dedupes events delivered by two informers of the same resource (cluster-wide and one
	// namespace): uid -> resourceVersion already recorded ("" after a delete).
	seen map[string]string
}

const maxSeen = 200_000

// append writes one entry, giving it its id, and compacts the file when it grows past the limit.
func (l *clusterLog) append(e *Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = l.store.now().UTC()
	}
	id := e.Time.UnixNano()
	if id <= l.lastID {
		id = l.lastID + 1
	}
	e.ID = id
	b, err := encode(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	l.lastID = id
	l.size += int64(len(b))
	if maxAge, limit := l.store.limits(); l.size > limit {
		return l.compact(maxAge, limit)
	}
	return nil
}

// encode marshals an entry as one line, dropping operations until it fits maxLine.
func encode(e *Entry) ([]byte, error) {
	for {
		b, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if len(b) < maxLine || len(e.Ops) == 0 {
			return append(b, '\n'), nil
		}
		e.Ops = e.Ops[:len(e.Ops)/2]
		e.Truncated = true
	}
}

func (s *Store) limits() (time.Duration, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxAge, s.maxBytes
}

// compact rewrites the file without the entries older than the retention and, when it is still
// over the size limit, without the oldest ones until it is under 80 % of it. Called with l.mu held
// (or before the log is shared); it must not take the store's lock.
func (l *clusterLog) compact(maxAge time.Duration, limit int64) error {
	f, err := os.Open(l.path)
	if err != nil {
		return err
	}
	cutoff := l.store.now().Add(-maxAge)
	var lines [][]byte
	var total int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine*2)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			ID   int64     `json:"id"`
			Time time.Time `json:"time"`
		}
		if json.Unmarshal(line, &head) != nil || head.Time.Before(cutoff) {
			continue
		}
		if head.ID > l.lastID {
			l.lastID = head.ID
		}
		c := append(append([]byte(nil), line...), '\n')
		lines = append(lines, c)
		total += int64(len(c))
	}
	scanErr := sc.Err()
	_ = f.Close()
	if scanErr != nil && !errors.Is(scanErr, bufio.ErrTooLong) {
		return scanErr
	}
	if total > limit {
		target := limit * 8 / 10
		i := 0
		for total > target && i < len(lines) {
			total -= int64(len(lines[i]))
			i++
		}
		lines = lines[i:]
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	w := bufio.NewWriter(tmp)
	for _, line := range lines {
		_, _ = w.Write(line)
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), l.path); err != nil {
		return err
	}
	l.size = total
	return nil
}

// Query selects entries. Resource set means one object (or one resource) and Group is compared
// exactly, "" being the core group.
type Query struct {
	Group     string
	Resource  string
	Namespace string
	Name      string
	Type      string
	Before    int64 // only entries with a smaller id (next page)
	Limit     int
}

// Page is a result page, newest first. Next is the cursor of the following page (0: none).
type Page struct {
	Entries []Entry `json:"entries"`
	Next    int64   `json:"next,omitempty"`
}

func (q Query) match(e *Entry) bool {
	if q.Before > 0 && e.ID >= q.Before {
		return false
	}
	if q.Resource != "" && (e.Resource != q.Resource || e.Group != q.Group) {
		return false
	}
	if q.Namespace != "" && e.Namespace != q.Namespace {
		return false
	}
	if q.Name != "" && e.Name != q.Name {
		return false
	}
	return q.Type == "" || e.Type == q.Type
}

// Query returns the newest entries of a cluster that match q.
func (s *Store) Query(key string, q Query) (Page, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	l, err := s.log(key)
	if err != nil {
		return Page{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return Page{Entries: []Entry{}}, nil
	}
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	cutoff := s.now().Add(-s.MaxAge())
	// Keep the newest limit+1 matches in a ring while scanning forward (the file is in id order).
	ring := make([]Entry, 0, q.Limit+1)
	var name []byte
	if q.Name != "" {
		name, _ = json.Marshal(q.Name)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine*2)
	for sc.Scan() {
		line := sc.Bytes()
		if name != nil && !bytes.Contains(line, name) {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil || e.Time.Before(cutoff) || !q.match(&e) {
			continue
		}
		if len(ring) == q.Limit+1 {
			copy(ring, ring[1:])
			ring = ring[:q.Limit]
		}
		ring = append(ring, e)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return Page{}, err
	}
	sort.Slice(ring, func(i, j int) bool { return ring[i].ID > ring[j].ID })
	p := Page{Entries: ring}
	if len(ring) > q.Limit {
		p.Entries = ring[:q.Limit]
		p.Next = ring[q.Limit-1].ID
	}
	return p, nil
}
