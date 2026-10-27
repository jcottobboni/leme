// Package analyze diagnoses common problems from object status, events and relations, without any
// AI: every finding carries the evidence it was derived from and a suggestion. It is the first
// opinion the Copilot and MCP agents build on, and works offline for everyone.
package analyze

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Severity of a finding.
const (
	Critical = "critical"
	High     = "high"
	Medium   = "medium"
	Low      = "low"
	Info     = "info"
)

// Ref identifies the affected object.
type Ref struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Finding is one diagnosed problem.
type Finding struct {
	ID          string   `json:"id"`       // rule id, e.g. "image-pull"
	Severity    string   `json:"severity"` // critical | high | medium | low | info
	Object      Ref      `json:"object"`
	Title       string   `json:"title"`       // short, Portuguese (UI language)
	Explanation string   `json:"explanation"` // what is wrong and why it matters
	Evidence    []string `json:"evidence"`    // status fields / event messages it was derived from
	Suggestion  string   `json:"suggestion"`  // what to do
	Fixable     bool     `json:"fixable"`     // a patch to the object itself can fix it (Copilot can propose)
}

var sevRank = map[string]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}

func sortFindings(f []Finding) []Finding {
	if f == nil {
		return []Finding{}
	}
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if sevRank[a.Severity] != sevRank[b.Severity] {
			return sevRank[a.Severity] < sevRank[b.Severity]
		}
		if a.Object.Namespace != b.Object.Namespace {
			return a.Object.Namespace < b.Object.Namespace
		}
		if a.Object.Kind != b.Object.Kind {
			return a.Object.Kind < b.Object.Kind
		}
		if a.Object.Name != b.Object.Name {
			return a.Object.Name < b.Object.Name
		}
		return a.ID < b.ID
	})
	return f
}

// Object diagnoses one object and what directly belongs to it (e.g. a Deployment and its pods).
func Object(ctx context.Context, s *cluster.Session, ref Ref) ([]Finding, error) {
	c := newScope(ctx, s, ref.Namespace)
	res, ok := s.LookupKind(ref.Group, ref.Kind)
	if !ok {
		return []Finding{}, nil
	}
	objs, err := c.list(ref.Group, res.Resource)
	if err != nil {
		return nil, err
	}
	for _, u := range objs {
		if u.GetName() != ref.Name || (res.Namespaced && u.GetNamespace() != ref.Namespace) {
			continue
		}
		return sortFindings(c.diagnose(u, true)), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Group: ref.Group, Resource: res.Resource}, ref.Name)
}

// Namespace diagnoses every object of a namespace ("" = whole cluster). Pods are required; the
// other kinds that cannot be listed (RBAC, timeout) are skipped and returned in errs
// ("group/resource" -> message), so "no findings" is never claimed for what was not seen.
func Namespace(ctx context.Context, s *cluster.Session, namespace string) (findings []Finding, errs map[string]string, err error) {
	c := newScope(ctx, s, namespace)
	var out []Finding
	pods, err := c.list("", "pods")
	if err != nil {
		return nil, nil, fmt.Errorf("list pods: %w", err)
	}
	for _, u := range pods {
		out = append(out, c.diagnose(u, false)...)
	}
	kinds := [][2]string{
		{"apps", "deployments"}, {"apps", "statefulsets"}, {"apps", "daemonsets"}, {"apps.openshift.io", "deploymentconfigs"},
		{"", "services"}, {"networking.k8s.io", "ingresses"}, {"route.openshift.io", "routes"},
		{"", "persistentvolumeclaims"}, {"autoscaling", "horizontalpodautoscalers"},
		{"batch", "jobs"}, {"batch", "cronjobs"},
	}
	if namespace == "" {
		kinds = append(kinds, [2]string{"", "nodes"})
	}
	for _, k := range kinds {
		objs, err := c.list(k[0], k[1])
		if err != nil {
			continue // missing permission on one kind must not hide the rest; reported in errs
		}
		for _, u := range objs {
			out = append(out, c.diagnose(u, false)...)
		}
	}
	return sortFindings(out), c.errors(), nil
}
