package plugins

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/analyze"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

// Rule is a compiled CEL diagnosis rule.
type Rule struct {
	Plugin   string
	Spec     RuleSpec
	kinds    []analyze.Kind
	match    *program
	evidence *program
}

func compileRule(plugin string, r RuleSpec) (*Rule, error) {
	m, err := compile(r.Match)
	if err != nil {
		return nil, fmt.Errorf("regra %s: %w", r.ID, err)
	}
	out := &Rule{Plugin: plugin, Spec: r, match: m}
	if r.Evidence != "" {
		if out.evidence, err = compile(r.Evidence); err != nil {
			return nil, fmt.Errorf("regra %s, evidence: %w", r.ID, err)
		}
	}
	for _, k := range r.Kinds {
		g, kind := SplitKind(k)
		out.kinds = append(out.kinds, analyze.Kind{Group: g, Kind: kind})
	}
	return out, nil
}

// RuleSet adapts the loaded rules to the analyzer.
type RuleSet []*Rule

var _ analyze.Extension = RuleSet(nil)

// Kinds lists every kind some rule looks at.
func (rs RuleSet) Kinds() []analyze.Kind {
	seen := map[analyze.Kind]bool{}
	var out []analyze.Kind
	for _, r := range rs {
		for _, k := range r.kinds {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// Check evaluates the rules of the object's kind. The object is sanitized first (Secret values,
// credentials in env): findings reach the Copilot and its providers.
func (rs RuleSet) Check(u *unstructured.Unstructured) []analyze.Finding {
	gv, _ := schema.ParseGroupVersion(u.GetAPIVersion())
	k := analyze.Kind{Group: gv.Group, Kind: u.GetKind()}
	var obj map[string]any
	var out []analyze.Finding
	for _, r := range rs {
		if !r.applies(k) {
			continue
		}
		if obj == nil {
			obj = redact.Object(u).Object
		}
		ok, err := r.match.Bool(obj)
		if err != nil || !ok {
			continue
		}
		f := analyze.Finding{
			ID:          "plugin:" + r.Plugin + "/" + r.Spec.ID,
			Severity:    r.Spec.Severity,
			Object:      analyze.Ref{Group: gv.Group, Kind: u.GetKind(), Namespace: u.GetNamespace(), Name: u.GetName()},
			Title:       r.Spec.Title,
			Explanation: r.Spec.Explanation,
			Suggestion:  r.Spec.Suggestion,
			Evidence:    []string{"regra do plugin " + r.Plugin + ": " + short(r.Spec.Match)},
		}
		if r.evidence != nil {
			if ev, err := r.evidence.Strings(obj); err == nil && len(ev) > 0 {
				f.Evidence = ev
			}
		}
		out = append(out, f)
	}
	return out
}

func (r *Rule) applies(k analyze.Kind) bool {
	for _, x := range r.kinds {
		if x == k {
			return true
		}
	}
	return false
}
