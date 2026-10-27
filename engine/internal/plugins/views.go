package plugins

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"

	"github.com/jcottobboni/leme/engine/internal/project"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

// ColumnPrefix prefixes the cell keys of plugin view columns.
const ColumnPrefix = "pl:"

// View is a compiled CRD view.
type View struct {
	Plugin  string
	Group   string
	Kind    string
	columns []*cell
	fields  []*cell
	health  []healthRule
}

type cell struct {
	name string
	expr *program
	mu   sync.Mutex // a parsed JSONPath keeps evaluation state
	jp   *jsonpath.JSONPath
}

type healthRule struct {
	when   *program
	status string
}

func compileView(plugin string, v ViewSpec) (*View, error) {
	out := &View{Plugin: plugin, Group: v.Group, Kind: v.Kind}
	mk := func(c ViewColumn) (*cell, error) {
		x := &cell{name: c.Name}
		if c.Expr != "" {
			p, err := compile(c.Expr)
			if err != nil {
				return nil, err
			}
			x.expr = p
			return x, nil
		}
		jp := jsonpath.New(c.Name).AllowMissingKeys(true)
		expr := strings.TrimSpace(c.JSONPath)
		if !strings.HasPrefix(expr, "{") {
			expr = "{" + expr + "}"
		}
		if err := jp.Parse(expr); err != nil {
			return nil, fmt.Errorf("JSONPath %q: %w", c.JSONPath, err)
		}
		x.jp = jp
		return x, nil
	}
	for _, c := range v.Columns {
		x, err := mk(c)
		if err != nil {
			return nil, fmt.Errorf("view %s, coluna %s: %w", v.Kind, c.Name, err)
		}
		out.columns = append(out.columns, x)
	}
	for _, c := range v.Fields {
		x, err := mk(c)
		if err != nil {
			return nil, fmt.Errorf("view %s, campo %s: %w", v.Kind, c.Name, err)
		}
		out.fields = append(out.fields, x)
	}
	for _, h := range v.Health {
		p, err := compile(h.When)
		if err != nil {
			return nil, fmt.Errorf("view %s, health: %w", v.Kind, err)
		}
		out.health = append(out.health, healthRule{when: p, status: h.Status})
	}
	return out, nil
}

// value evaluates one cell; errors and missing fields give nil.
func (c *cell) value(obj map[string]any) any {
	if c.expr != nil {
		v, err := c.expr.Value(obj)
		if err != nil {
			return nil
		}
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return evalJP(c.jp, obj)
}

func evalJP(jp *jsonpath.JSONPath, obj map[string]any) (v any) {
	defer func() {
		if recover() != nil {
			v = nil
		}
	}()
	res, err := jp.FindResults(obj)
	if err != nil || len(res) == 0 || len(res[0]) == 0 {
		return nil
	}
	var parts []string
	for _, r := range res[0] {
		if !r.IsValid() || !r.CanInterface() {
			continue
		}
		x := r.Interface()
		switch t := x.(type) {
		case string, int64, float64, bool, int:
			if len(res[0]) == 1 {
				return t
			}
			parts = append(parts, fmt.Sprint(t))
		default:
			parts = append(parts, fmt.Sprint(t))
		}
	}
	return strings.Join(parts, ", ")
}

// safeObject hides Secret values from plugin expressions, as everywhere else in the grid.
func safeObject(u *unstructured.Unstructured) map[string]any {
	if u.GetKind() == "Secret" && (u.GetAPIVersion() == "v1") {
		return redact.Object(u).Object
	}
	return u.Object
}

// Columns returns the extra grid columns.
func (v *View) Columns() []project.Column {
	out := make([]project.Column, 0, len(v.columns))
	for _, c := range v.columns {
		col := project.Column{Key: ColumnPrefix + c.name, Label: c.name, Width: 130}
		switch strings.ToLower(c.name) {
		case "status", "estado", "state", "phase", "fase", "ready", "pronto":
			col.Type, col.Width = "status", 110
		}
		out = append(out, col)
	}
	return out
}

// Apply sets the view cells and, when a health rule matches, the health of a grid row.
func (v *View) Apply(u *unstructured.Unstructured, r *project.Row) {
	obj := safeObject(u)
	if r.Cells == nil {
		r.Cells = map[string]any{}
	}
	for _, c := range v.columns {
		if x := c.value(obj); x != nil {
			r.Cells[ColumnPrefix+c.name] = x
		}
	}
	for _, h := range v.health {
		if ok, err := h.when.Bool(obj); err == nil && ok {
			r.Health = h.status
			break
		}
	}
}

// Field is one evaluated detail field.
type Field struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// Fields evaluates the detail fields of an object.
func (v *View) Fields(u *unstructured.Unstructured) []Field {
	obj := safeObject(u)
	out := make([]Field, 0, len(v.fields))
	for _, c := range v.fields {
		out = append(out, Field{Name: c.name, Value: c.value(obj)})
	}
	return out
}
