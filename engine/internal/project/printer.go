package project

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/jsonpath"
)

// PrinterColumn is one additionalPrinterColumns entry of a CRD version.
type PrinterColumn struct {
	Name     string
	Type     string // integer | number | string | boolean | date
	JSONPath string
	Priority int64
}

// PrinterKeyPrefix prefixes the cell keys of CRD printer columns so they never collide with ours.
const PrinterKeyPrefix = "pc:"

// Printer projects the printer columns of a custom resource into extra grid cells.
type Printer struct {
	columns []Column
	cells   []printerCell
	mu      sync.Mutex // a parsed JSONPath keeps evaluation state: one evaluation at a time
}

type printerCell struct {
	key  string
	typ  string
	path *jsonpath.JSONPath
}

// PrinterColumns reads the additionalPrinterColumns of the served version from a CRD object.
func PrinterColumns(crd *unstructured.Unstructured, version string) []PrinterColumn {
	var out []PrinterColumn
	for _, v := range slice(crd, "spec", "versions") {
		vm, _ := v.(map[string]any)
		if strOr(vm["name"], "") != version {
			continue
		}
		for _, c := range asSlice(vm["additionalPrinterColumns"]) {
			cm, _ := c.(map[string]any)
			out = append(out, PrinterColumn{Name: strOr(cm["name"], ""), Type: strOr(cm["type"], "string"), JSONPath: strOr(cm["jsonPath"], ""), Priority: toInt(cm["priority"])})
		}
	}
	return out
}

// NewPrinter builds the grid columns of a custom resource: name (and namespace), the printer
// columns kubectl shows by default (priority 0) and age. Columns repeating the age, or whose
// JSONPath does not parse, are skipped. It returns nil when nothing would be added.
func NewPrinter(pcs []PrinterColumn, namespaced bool) *Printer {
	p := &Printer{columns: []Column{base[0]}}
	if namespaced {
		p.columns = append(p.columns, base[1])
	}
	seen := map[string]bool{}
	for _, pc := range pcs {
		if pc.Priority > 0 || pc.Name == "" || pc.JSONPath == "" || strings.TrimSpace(pc.JSONPath) == ".metadata.creationTimestamp" {
			continue
		}
		key := PrinterKeyPrefix + pc.Name
		if seen[key] {
			continue
		}
		jp := jsonpath.New(pc.Name).AllowMissingKeys(true)
		if err := jp.Parse("{" + pc.JSONPath + "}"); err != nil {
			continue
		}
		seen[key] = true
		col := Column{Key: key, Label: pc.Name, Width: 140}
		switch {
		case pc.Type == "integer" || pc.Type == "number":
			col.Type, col.Width = "number", 90
		case pc.Type == "date":
			col.Type, col.Width = "age", 90
		case strings.EqualFold(pc.Name, "status") || strings.EqualFold(pc.Name, "phase") || strings.EqualFold(pc.Name, "state") || strings.EqualFold(pc.Name, "ready"):
			col.Type, col.Width = "status", 110
		}
		p.columns = append(p.columns, col)
		p.cells = append(p.cells, printerCell{key: key, typ: pc.Type, path: jp})
	}
	if len(p.cells) == 0 {
		return nil
	}
	p.columns = append(p.columns, ageCol)
	return p
}

// Columns returns the grid columns.
func (p *Printer) Columns() []Column { return p.columns }

// Apply sets the printer cells of a row. Missing fields leave the cell empty.
func (p *Printer) Apply(u *unstructured.Unstructured, r *Row) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.cells {
		res, err := c.path.FindResults(u.Object)
		if err != nil {
			continue
		}
		var vals []any
		for _, set := range res {
			for _, v := range set {
				if x := plain(v); x != nil {
					vals = append(vals, x)
				}
			}
		}
		if len(vals) == 0 {
			continue
		}
		if len(vals) == 1 && (c.typ == "integer" || c.typ == "number") {
			if n, ok := number(vals[0]); ok {
				r.Cells[c.key] = n
				continue
			}
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = cellText(v)
		}
		r.Cells[c.key] = strings.Join(parts, ",")
	}
}

func plain(v reflect.Value) any {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil
	}
	return v.Interface()
}

func number(v any) (any, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
		return n, true
	}
	return nil, false
}

func cellText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any, []any:
		// kubectl prints JSON for composite values; keep it short and readable.
		b, _ := json.Marshal(t)
		s := string(b)
		if len(s) > 200 {
			s = s[:200] + "…"
		}
		return s
	default:
		return fmt.Sprint(t)
	}
}
