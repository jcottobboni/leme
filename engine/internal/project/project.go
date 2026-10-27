// Package project turns Kubernetes objects into compact grid rows.
package project

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/kinds"
)

// Column describes a grid column.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"` // text | number | age | status | containers
	Width int    `json:"width,omitempty"`
}

// Row is the compact projection sent to the UI.
type Row struct {
	UID       string            `json:"uid"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Created   string            `json:"created,omitempty"`
	Health    string            `json:"health"` // ok | warn | error | neutral
	Labels    map[string]string `json:"labels,omitempty"`
	Cells     map[string]any    `json:"cells"`
}

// HealthCount counts objects (or pods) per health; neutral ones only add to Total. The overview
// and the cluster map share it.
type HealthCount struct {
	Total int `json:"total"`
	OK    int `json:"ok"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
}

// Add counts one object of the given health.
func (h *HealthCount) Add(health string) {
	h.Total++
	switch health {
	case "ok":
		h.OK++
	case "warn":
		h.Warn++
	case "error":
		h.Error++
	}
}

type projector struct {
	columns []Column
	fn      func(u *unstructured.Unstructured, r *Row)
}

// Columns returns the columns for a kind; namespaced controls the generic fallback. The per-kind
// columns and health rules only apply to built-in groups (see kinds.Builtin): a CRD may reuse a
// built-in kind name and must not be shown as, or projected like, the built-in one.
func Columns(group, kind string, namespaced bool) []Column {
	if p, ok := projectors[kind]; ok && kinds.Builtin(group) {
		return p.columns
	}
	if namespaced {
		return []Column{base[0], base[1], ageCol}
	}
	return []Column{base[0], ageCol}
}

// Project builds a row for any object.
func Project(u *unstructured.Unstructured) Row {
	r := Row{UID: string(u.GetUID()), Name: u.GetName(), Namespace: u.GetNamespace(), Health: "neutral", Labels: u.GetLabels(), Cells: map[string]any{}}
	if ts := u.GetCreationTimestamp(); !ts.IsZero() {
		r.Created = ts.UTC().Format(time.RFC3339)
	}
	if r.UID == "" {
		r.UID = r.Namespace + "/" + r.Name
	}
	if p, ok := projectors[u.GetKind()]; ok && kinds.Builtin(kinds.GroupOf(u.GetAPIVersion())) {
		p.fn(u, &r)
	} else {
		genericConditions(u, &r)
	}
	if u.GetDeletionTimestamp() != nil {
		r.Cells["status"] = "Terminating"
		r.Health = "warn"
	}
	return r
}

func str(u *unstructured.Unstructured, path ...string) string {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

func num(u *unstructured.Unstructured, path ...string) int64 {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case int:
		return int64(t)
	}
	return 0
}

func slice(u *unstructured.Unstructured, path ...string) []any {
	v, _, _ := unstructured.NestedFieldNoCopy(u.Object, path...)
	s, _ := v.([]any)
	return s
}

func mapAt(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func genericConditions(u *unstructured.Unstructured, r *Row) {
	for _, c := range slice(u, "status", "conditions") {
		cm, _ := c.(map[string]any)
		if cm["type"] == "Ready" || cm["type"] == "Available" {
			if cm["status"] == "True" {
				r.Health = "ok"
			} else {
				r.Health = "warn"
			}
		}
	}
}

func toInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case int:
		return int64(t)
	}
	return 0
}

func strOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

func joinMap(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}
