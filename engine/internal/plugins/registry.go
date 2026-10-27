package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxFileSize bounds one plugin file.
const maxFileSize = 1 << 20

// File reports what one plugin file contributed, or why it was not loaded.
type File struct {
	Path     string   `json:"path"`
	Format   string   `json:"format,omitempty"`
	Plugin   string   `json:"plugin,omitempty"`
	Origin   string   `json:"origin"` // leme | k9s
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Actions  int      `json:"actions"`
	Links    int      `json:"links"`
	Views    int      `json:"views"`
	Rules    int      `json:"rules"`
}

// Snapshot is the loaded state; it is immutable once built.
type Snapshot struct {
	Actions []Action
	Links   []Link
	Views   []*View
	Rules   RuleSet
	Files   []File
	Dir     string
	K9s     bool
}

// Action finds an action by id.
func (s *Snapshot) Action(id string) (Action, bool) {
	for _, a := range s.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// ViewFor returns the view of a kind (the first plugin wins), or nil.
func (s *Snapshot) ViewFor(group, kind string) *View {
	for _, v := range s.Views {
		if v.Group == group && v.Kind == kind {
			return v
		}
	}
	return nil
}

// Registry loads plugins from the Leme plugin folder and, when enabled, from k9s.
type Registry struct {
	dir     string
	k9s     func() bool
	mu      sync.Mutex
	snap    *Snapshot
	stamp   string
	checked time.Time
}

// NewRegistry watches <configDir>/plugins; k9s tells whether k9s' plugin files are read too.
func NewRegistry(configDir string, k9s func() bool) *Registry {
	return &Registry{dir: filepath.Join(configDir, "plugins"), k9s: k9s}
}

// Dir is the Leme plugin folder.
func (r *Registry) Dir() string { return r.dir }

// Current returns the loaded plugins, reloading when a file changed (checked at most once a second).
func (r *Registry) Current() *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap != nil && time.Since(r.checked) < time.Second {
		return r.snap
	}
	r.checked = time.Now()
	files := r.sources()
	stamp := fmt.Sprint(r.k9s())
	for _, f := range files {
		if st, err := os.Stat(f.path); err == nil {
			stamp += fmt.Sprintf("|%s:%d:%d", f.path, st.Size(), st.ModTime().UnixNano())
		}
	}
	if r.snap == nil || stamp != r.stamp {
		r.snap, r.stamp = load(files, r.dir, r.k9s()), stamp
	}
	return r.snap
}

// Reload forces a reload on the next Current.
func (r *Registry) Reload() *Snapshot {
	r.mu.Lock()
	r.snap = nil
	r.mu.Unlock()
	return r.Current()
}

type source struct {
	path   string
	origin string
}

// sources lists the plugin files: the Leme folder, then k9s' plugins.yaml and plugin folder.
func (r *Registry) sources() []source {
	var out []source
	out = append(out, yamlFiles(r.dir, "leme")...)
	if r.k9s() {
		if d := k9sConfigDir(); d != "" {
			if p := filepath.Join(d, "plugins.yaml"); exists(p) {
				out = append(out, source{p, "k9s"})
			}
		}
		if d := k9sDataDir(); d != "" {
			out = append(out, yamlFiles(filepath.Join(d, "plugins"), "k9s")...)
		}
	}
	return out
}

func yamlFiles(dir, origin string) []source {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []source
	for _, e := range ents {
		n := strings.ToLower(e.Name())
		if e.IsDir() || strings.HasPrefix(n, ".") || !(strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
			continue
		}
		out = append(out, source{filepath.Join(dir, e.Name()), origin})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// k9sConfigDir follows k9s: $K9S_CONFIG_DIR, else the XDG config home (which k9s resolves per OS).
func k9sConfigDir() string {
	if d := os.Getenv("K9S_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "k9s")
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "k9s")
		}
		return ""
	case "darwin":
		h, _ := os.UserHomeDir()
		return filepath.Join(h, "Library", "Application Support", "k9s")
	default:
		h, _ := os.UserHomeDir()
		return filepath.Join(h, ".config", "k9s")
	}
}

// k9sDataDir is where k9s keeps plugin files besides plugins.yaml.
func k9sDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "k9s")
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "k9s")
		}
		return ""
	case "darwin":
		h, _ := os.UserHomeDir()
		return filepath.Join(h, "Library", "Application Support", "k9s")
	default:
		h, _ := os.UserHomeDir()
		return filepath.Join(h, ".local", "share", "k9s")
	}
}

func load(srcs []source, dir string, k9s bool) *Snapshot {
	snap := &Snapshot{Dir: dir, K9s: k9s, Actions: []Action{}, Links: []Link{}, Files: []File{}}
	actionAt := map[string]int{}
	plugins := map[string]string{}
	for _, src := range srcs {
		f := File{Path: src.path, Origin: src.origin}
		p, err := parseFile(src.path)
		if err == nil && p.Format == FormatLeme {
			if prev, dup := plugins[p.Plugin]; dup {
				err = fmt.Errorf("plugin %q já definido em %s", p.Plugin, prev)
			}
		}
		var views []*View
		var rules []*Rule
		if err == nil {
			views, rules, err = compileAll(p)
		}
		if err != nil {
			f.Error = err.Error()
			snap.Files = append(snap.Files, f)
			continue
		}
		if p.Format == FormatLeme {
			plugins[p.Plugin] = src.path
		}
		f.Format, f.Plugin, f.Warnings = p.Format, p.Plugin, p.Warnings
		f.Actions, f.Links, f.Views, f.Rules = len(p.Actions), len(p.Links), len(views), len(rules)
		for _, a := range p.Actions {
			a.Hash = hashAction(a)
			if i, dup := actionAt[a.ID]; dup {
				// Like k9s, a later file overrides an earlier definition of the same plugin name.
				f.Warnings = append(f.Warnings, fmt.Sprintf("%s substitui a definição de %s", a.ID, snap.Actions[i].Source))
				snap.Actions[i] = a
				continue
			}
			actionAt[a.ID] = len(snap.Actions)
			snap.Actions = append(snap.Actions, a)
		}
		snap.Links = append(snap.Links, p.Links...)
		snap.Views = append(snap.Views, views...)
		snap.Rules = append(snap.Rules, rules...)
		snap.Files = append(snap.Files, f)
	}
	return snap
}

func parseFile(path string) (*Parsed, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > maxFileSize {
		return nil, fmt.Errorf("arquivo maior que %d KiB", maxFileSize>>10)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".yaml"), ".yml")
	return Parse(b, name, path)
}

func compileAll(p *Parsed) ([]*View, []*Rule, error) {
	var views []*View
	var rules []*Rule
	for _, v := range p.Views {
		cv, err := compileView(p.Plugin, v)
		if err != nil {
			return nil, nil, err
		}
		views = append(views, cv)
	}
	for _, r := range p.Rules {
		cr, err := compileRule(p.Plugin, r)
		if err != nil {
			return nil, nil, err
		}
		rules = append(rules, cr)
	}
	return views, rules, nil
}
