// Package plugins loads declarative plugins: actions (local commands run on a selected object),
// links, CRD views and CEL diagnosis rules. Plugins are YAML files in <config>/plugins, in the Leme
// format or in the k9s plugins.yaml format; k9s' own plugin files are read as well. No plugin code
// runs inside the app: an action runs a command the user installed, after the user trusted it.
package plugins

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// APIVersion marks a file in the Leme format.
const APIVersion = "leme.dev/v1"

// Modes of an action.
const (
	ModeTerminal   = "terminal"   // interactive, in a terminal panel
	ModeOutput     = "output"     // runs to completion and shows its output
	ModeBackground = "background" // runs to completion; the UI only reports the result
)

// Formats of a plugin file.
const (
	FormatLeme = "leme"
	FormatK9s  = "k9s"
)

// Action is a local command run on the selected object.
type Action struct {
	ID          string   `json:"id"` // <plugin>/<action>, stable across reloads
	Plugin      string   `json:"plugin"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Mode        string   `json:"mode"`
	Mutates     bool     `json:"mutates"`
	Confirm     bool     `json:"confirm"`
	ShortCut    string   `json:"shortCut,omitempty"`
	Source      string   `json:"source"`
	Format      string   `json:"format"`
	Hash        string   `json:"hash"`
}

// Link is a URL built from the selected object, opened in the browser.
type Link struct {
	ID     string   `json:"id"`
	Plugin string   `json:"plugin"`
	Title  string   `json:"title"`
	Scopes []string `json:"scopes"`
	URL    string   `json:"url"`
}

// ViewColumn is a grid column or a detail field of a CRD view: a JSONPath or a CEL expression.
type ViewColumn struct {
	Name     string `json:"name"`
	JSONPath string `json:"jsonPath,omitempty"`
	Expr     string `json:"expr,omitempty"`
}

// HealthRule maps an object to a grid health when its CEL expression is true.
type HealthRule struct {
	When   string `json:"when"`
	Status string `json:"status"` // ok | warn | error | neutral
}

// ViewSpec describes how one kind is shown.
type ViewSpec struct {
	Group   string       `json:"group"`
	Kind    string       `json:"kind"`
	Columns []ViewColumn `json:"columns,omitempty"`
	Fields  []ViewColumn `json:"fields,omitempty"`
	Health  []HealthRule `json:"health,omitempty"`
}

// RuleSpec is a CEL diagnosis rule.
type RuleSpec struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Severity    string   `json:"severity"`
	Kinds       []string `json:"kinds"` // "Kind" (core) or "group/Kind"
	Match       string   `json:"match"`
	Explanation string   `json:"explanation,omitempty"`
	Suggestion  string   `json:"suggestion,omitempty"`
	// Evidence is an optional CEL expression returning a string or a list of strings.
	Evidence string `json:"evidence,omitempty"`
}

// lemeFile is a plugin file in the Leme format.
type lemeFile struct {
	APIVersion  string       `json:"apiVersion"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Actions     []lemeAction `json:"actions,omitempty"`
	Links       []lemeLink   `json:"links,omitempty"`
	Views       []ViewSpec   `json:"views,omitempty"`
	Rules       []RuleSpec   `json:"rules,omitempty"`
}

type lemeAction struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Scopes      []string `json:"scopes"`
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Mutates     bool     `json:"mutates,omitempty"`
	Confirm     bool     `json:"confirm,omitempty"`
}

type lemeLink struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Scopes []string `json:"scopes"`
	URL    string   `json:"url"`
}

// Parsed is what one file contributes.
type Parsed struct {
	Format   string
	Plugin   string
	Actions  []Action
	Links    []Link
	Views    []ViewSpec
	Rules    []RuleSpec
	Warnings []string
}

var idRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)

// Parse reads one plugin file. name is used for k9s files holding a single plugin.
func Parse(data []byte, name, source string) (*Parsed, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("YAML inválido: %w", err)
	}
	if len(probe) == 0 {
		return nil, errors.New("arquivo vazio")
	}
	if v, _ := probe["apiVersion"].(string); v != "" {
		if v != APIVersion {
			return nil, fmt.Errorf("apiVersion %q não suportada (use %s)", v, APIVersion)
		}
		return parseLeme(data, source)
	}
	return parseK9s(probe, name, source)
}

func parseLeme(data []byte, source string) (*Parsed, error) {
	var f lemeFile
	if err := yaml.UnmarshalStrict(data, &f); err != nil {
		return nil, fmt.Errorf("formato Leme: %w", err)
	}
	if !idRe.MatchString(f.Name) {
		return nil, errors.New("name obrigatório (letras, números, ., _ e -)")
	}
	if f.Name == "k9s" {
		return nil, errors.New(`name "k9s" é reservado para os plugins lidos do k9s`)
	}
	p := &Parsed{Format: FormatLeme, Plugin: f.Name}
	seen := map[string]bool{}
	for i, a := range f.Actions {
		if !idRe.MatchString(a.ID) {
			return nil, fmt.Errorf("actions[%d]: id inválido", i)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("actions[%d]: id %q repetido", i, a.ID)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Command) == "" {
			return nil, fmt.Errorf("ação %s: command obrigatório", a.ID)
		}
		if len(a.Scopes) == 0 {
			return nil, fmt.Errorf("ação %s: scopes obrigatório", a.ID)
		}
		mode := a.Mode
		if mode == "" {
			mode = ModeOutput
		}
		if mode != ModeTerminal && mode != ModeOutput && mode != ModeBackground {
			return nil, fmt.Errorf("ação %s: mode deve ser terminal, output ou background", a.ID)
		}
		title := a.Title
		if title == "" {
			title = a.ID
		}
		p.Actions = append(p.Actions, Action{ID: f.Name + "/" + a.ID, Plugin: f.Name, Title: title, Description: a.Description, Scopes: lower(a.Scopes),
			Command: a.Command, Args: nonNil(a.Args), Mode: mode, Mutates: a.Mutates, Confirm: a.Confirm, Source: source, Format: FormatLeme})
	}
	seen = map[string]bool{}
	for i, l := range f.Links {
		if !idRe.MatchString(l.ID) || seen[l.ID] {
			return nil, fmt.Errorf("links[%d]: id inválido ou repetido", i)
		}
		seen[l.ID] = true
		if !strings.HasPrefix(l.URL, "https://") && !strings.HasPrefix(l.URL, "http://") {
			return nil, fmt.Errorf("link %s: só URLs http(s)", l.ID)
		}
		if len(l.Scopes) == 0 {
			return nil, fmt.Errorf("link %s: scopes obrigatório", l.ID)
		}
		title := l.Title
		if title == "" {
			title = l.ID
		}
		p.Links = append(p.Links, Link{ID: f.Name + "/" + l.ID, Plugin: f.Name, Title: title, Scopes: lower(l.Scopes), URL: l.URL})
	}
	for i, v := range f.Views {
		if v.Kind == "" {
			return nil, fmt.Errorf("views[%d]: kind obrigatório", i)
		}
		for _, c := range append(append([]ViewColumn{}, v.Columns...), v.Fields...) {
			if c.Name == "" || (c.JSONPath == "") == (c.Expr == "") {
				return nil, fmt.Errorf("view %s: cada coluna precisa de name e de jsonPath ou expr (um só)", v.Kind)
			}
		}
		for _, h := range v.Health {
			switch h.Status {
			case "ok", "warn", "error", "neutral":
			default:
				return nil, fmt.Errorf("view %s: health.status deve ser ok, warn, error ou neutral", v.Kind)
			}
			if h.When == "" {
				return nil, fmt.Errorf("view %s: health.when obrigatório", v.Kind)
			}
		}
		p.Views = append(p.Views, v)
	}
	seen = map[string]bool{}
	for i, r := range f.Rules {
		if !idRe.MatchString(r.ID) || seen[r.ID] {
			return nil, fmt.Errorf("rules[%d]: id inválido ou repetido", i)
		}
		seen[r.ID] = true
		switch r.Severity {
		case "critical", "high", "medium", "low", "info":
		case "":
			r.Severity = "medium"
		default:
			return nil, fmt.Errorf("regra %s: severity deve ser critical, high, medium, low ou info", r.ID)
		}
		if r.Match == "" || len(r.Kinds) == 0 || r.Title == "" {
			return nil, fmt.Errorf("regra %s: title, kinds e match são obrigatórios", r.ID)
		}
		p.Rules = append(p.Rules, r)
	}
	return p, nil
}

// SplitKind parses "Kind" (core group) or "group/Kind".
func SplitKind(s string) (group, kind string) {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func lower(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
