package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"

	"github.com/jcottobboni/leme/engine/internal/project"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

// JSONPath columns (M7 RF05): the user adds columns per resource type in the grid; the watch
// receives the expressions in `cols` (a JSON array) and the engine evaluates them on the cached
// objects. Guards: few and short expressions, a cap on the values and text per cell, a time budget
// per evaluation (a slow expression is switched off), and Secrets are evaluated on a masked copy so
// their values never reach the grid.
const (
	jpPrefix     = "jp:"
	jpMaxCols    = 12
	jpMaxExpr    = 256
	jpMaxValues  = 50
	jpMaxCell    = 256
	jpSlowSingle = 50 * time.Millisecond
)

type jpCol struct {
	key  string
	path *jsonpath.JSONPath
	slow bool
}

// jpCols evaluates the custom columns of one watch. JSONPath values are not safe for concurrent use.
type jpCols struct {
	mu     sync.Mutex
	cols   []*jpCol
	errs   map[string]string
	secret bool
}

// normalizeJP accepts ".spec.replicas", "spec.replicas" or "{.spec.replicas}" and returns the
// braced template form that is also used in the cell key.
func normalizeJP(expr string) string {
	e := strings.TrimSpace(expr)
	if e == "" {
		return ""
	}
	if !strings.Contains(e, "{") {
		if !strings.HasPrefix(e, ".") && !strings.HasPrefix(e, "[") {
			e = "." + e
		}
		e = "{" + e + "}"
	}
	return e
}

// parseJPCols reads the `cols` parameter. Invalid expressions are reported per key and skipped;
// the others still work. secret is true for core Secrets.
func parseJPCols(raw string, secret bool) (*jpCols, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var exprs []string
	if err := json.Unmarshal([]byte(raw), &exprs); err != nil {
		return nil, errors.New("cols must be a JSON array of strings")
	}
	if len(exprs) > jpMaxCols {
		exprs = exprs[:jpMaxCols]
	}
	j := &jpCols{errs: map[string]string{}, secret: secret}
	seen := map[string]bool{}
	for _, x := range exprs {
		e := normalizeJP(x)
		if e == "" {
			continue
		}
		key := jpPrefix + e
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(e) > jpMaxExpr {
			j.errs[key] = fmt.Sprintf("expressão longa demais (máximo %d caracteres)", jpMaxExpr)
			continue
		}
		p := jsonpath.New(key).AllowMissingKeys(true)
		if err := p.Parse(e); err != nil {
			j.errs[key] = "expressão JSONPath inválida: " + err.Error()
			continue
		}
		j.cols = append(j.cols, &jpCol{key: key, path: p})
	}
	if len(j.cols) == 0 && len(j.errs) == 0 {
		return nil, nil
	}
	return j, nil
}

// Columns returns the grid columns of the valid expressions (label = the expression; the UI shows
// the name the user gave it).
func (j *jpCols) Columns() []project.Column {
	out := make([]project.Column, 0, len(j.cols))
	for _, c := range j.cols {
		out = append(out, project.Column{Key: c.key, Label: strings.TrimSuffix(strings.TrimPrefix(c.key[len(jpPrefix):], "{"), "}"), Width: 140})
	}
	return out
}

// Errors returns the errors per column key (invalid or switched-off expressions), or nil.
func (j *jpCols) Errors() map[string]string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(j.errs))
	for k, v := range j.errs {
		out[k] = v
	}
	return out
}

// Apply sets the JSONPath cells of a row.
func (j *jpCols) Apply(u *unstructured.Unstructured, r *project.Row) {
	obj := u.Object
	if j.secret || isSecret(u) {
		// Secret values (data, stringData, annotations that embed them) never reach the grid.
		obj = redact.Object(u).Object
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if r.Cells == nil {
		r.Cells = map[string]any{}
	}
	for _, c := range j.cols {
		if c.slow {
			continue
		}
		t0 := time.Now()
		v, ok := evalJP(c.path, obj)
		if time.Since(t0) > jpSlowSingle {
			c.slow = true
			j.errs[c.key] = "expressão lenta demais: desligada nesta lista"
		}
		if ok {
			r.Cells[c.key] = v
		}
	}
}

// isSecret: a core Secret (a CRD named Secret in another group is not masked by this rule).
func isSecret(u *unstructured.Unstructured) bool {
	return u.GetKind() == "Secret" && (u.GetAPIVersion() == "v1" || u.GetAPIVersion() == "")
}

// evalJP returns one value (a number stays a number) or the values joined by commas, capped.
func evalJP(p *jsonpath.JSONPath, obj map[string]any) (any, bool) {
	res, err := p.FindResults(obj)
	if err != nil {
		return nil, false
	}
	var vals []any
	for _, set := range res {
		for _, v := range set {
			if x := plainValue(v); x != nil {
				vals = append(vals, x)
				if len(vals) >= jpMaxValues {
					break
				}
			}
		}
		if len(vals) >= jpMaxValues {
			break
		}
	}
	switch len(vals) {
	case 0:
		return nil, false
	case 1:
		switch n := vals[0].(type) {
		case int64, int, float64, bool:
			return n, true
		}
	}
	var b strings.Builder
	for i, v := range vals {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jpText(v))
		if b.Len() > jpMaxCell {
			break
		}
	}
	s := b.String()
	if len(s) > jpMaxCell {
		s = s[:jpMaxCell] + "…"
	}
	return s, true
}

func plainValue(v reflect.Value) any {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() || !v.CanInterface() {
		return nil
	}
	return v.Interface()
}

func jpText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		if len(b) > jpMaxCell {
			return string(b[:jpMaxCell]) + "…"
		}
		return string(b)
	default:
		return fmt.Sprint(t)
	}
}
