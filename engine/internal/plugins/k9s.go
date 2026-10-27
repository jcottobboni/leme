package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// k9sPlugin is one entry of a k9s plugins file. Field names follow k9s' documented format.
type k9sPlugin struct {
	ShortCut        string   `json:"shortCut"`
	Override        bool     `json:"override"`
	Pipes           []string `json:"pipes"`
	Description     string   `json:"description"`
	Scopes          []string `json:"scopes"`
	Command         string   `json:"command"`
	Args            []string `json:"args"`
	Background      bool     `json:"background"`
	Confirm         bool     `json:"confirm"`
	Dangerous       bool     `json:"dangerous"`
	OverwriteOutput bool     `json:"overwriteOutput"`
}

// k9sOnlyScopes are k9s views with no Leme counterpart.
var k9sOnlyScopes = map[string]bool{
	"helm": true, "pulses": true, "xray": true, "contexts": true, "ctx": true, "portforward": true, "pf": true,
	"benchmarks": true, "be": true, "aliases": true, "screendumps": true, "sd": true, "users": true, "groups": true, "popeye": true,
}

// parseK9s accepts the three shapes k9s reads: {plugins: {name: plugin}}, {name: plugin} and a
// single plugin (named after the file).
func parseK9s(probe map[string]any, name, source string) (*Parsed, error) {
	entries := map[string]any{}
	switch {
	case probe["plugins"] != nil:
		m, ok := probe["plugins"].(map[string]any)
		if !ok {
			return nil, errors.New("formato k9s: plugins deve ser um mapa")
		}
		entries = m
	case probe["command"] != nil:
		entries[name] = probe
	default:
		for k, v := range probe {
			m, ok := v.(map[string]any)
			if !ok || m["command"] == nil {
				return nil, errors.New("formato não reconhecido: não é um plugin do Leme (apiVersion: " + APIVersion + ") nem do k9s")
			}
			entries[k] = m
		}
	}
	p := &Parsed{Format: FormatK9s, Plugin: "k9s"}
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		b, _ := json.Marshal(entries[n])
		var kp k9sPlugin
		if err := json.Unmarshal(b, &kp); err != nil {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: ignorado (%v)", n, err))
			continue
		}
		if !idRe.MatchString(n) {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%q: ignorado (nome com caracteres não suportados)", n))
			continue
		}
		if strings.TrimSpace(kp.Command) == "" {
			p.Warnings = append(p.Warnings, n+": ignorado (sem command)")
			continue
		}
		if len(kp.Pipes) > 0 {
			p.Warnings = append(p.Warnings, n+": ignorado (pipes não é suportado)")
			continue
		}
		var scopes []string
		var dropped []string
		for _, s := range lower(kp.Scopes) {
			if k9sOnlyScopes[s] {
				dropped = append(dropped, s)
				continue
			}
			scopes = append(scopes, s)
		}
		if len(dropped) > 0 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s: escopo sem equivalente no Leme: %s", n, strings.Join(dropped, ", ")))
		}
		if len(scopes) == 0 {
			p.Warnings = append(p.Warnings, n+": ignorado (nenhum escopo suportado)")
			continue
		}
		mode := ModeTerminal
		if kp.Background {
			mode = ModeBackground
		}
		title := kp.Description
		if title == "" {
			title = n
		}
		p.Actions = append(p.Actions, Action{ID: "k9s/" + n, Plugin: "k9s", Title: title, Description: kp.Description, Scopes: scopes,
			Command: kp.Command, Args: nonNil(kp.Args), Mode: mode, Mutates: kp.Dangerous, Confirm: kp.Confirm, ShortCut: kp.ShortCut,
			Source: source, Format: FormatK9s})
	}
	return p, nil
}
