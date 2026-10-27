package api

import (
	"net/url"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/project"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

func obj(kind, name string, lbl map[string]string, extra map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": kind}}
	if kind == "Deployment" {
		u.SetAPIVersion("apps/v1")
	}
	u.SetName(name)
	u.SetNamespace("demo")
	u.SetUID(k8stypes.UID("uid-" + name))
	u.SetResourceVersion("1")
	u.SetLabels(lbl)
	for k, v := range extra {
		u.Object[k] = v
	}
	return u
}

func TestGridFilterLabelSelector(t *testing.T) {
	res := cluster.Resource{Group: "apps", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	sel, jp, err := gridFilter(url.Values{"labelSelector": {"app=web,tier!=db"}}, res)
	if err != nil || jp != nil || sel == nil {
		t.Fatalf("sel=%v jp=%v err=%v", sel, jp, err)
	}
	g := newGridSub(nil)
	g.sel = sel
	web := obj("Deployment", "web", map[string]string{"app": "web"}, nil)
	api := obj("Deployment", "api", map[string]string{"app": "api"}, nil)
	g.OnUpsert(web)
	g.OnUpsert(api)
	ups, dels, _ := g.drain()
	if len(ups) != 1 || ups[0].Name != "web" || len(dels) != 0 {
		t.Fatalf("only web should pass: %+v %v", ups, dels)
	}
	// Relabeled out of the selector: the client must drop it.
	moved := web.DeepCopy()
	moved.SetLabels(map[string]string{"app": "web", "tier": "db"})
	moved.SetResourceVersion("2")
	g.OnUpsert(moved)
	ups, dels, _ = g.drain()
	if len(ups) != 0 || len(dels) != 1 || dels[0] != string(web.GetUID()) {
		t.Fatalf("relabeled object must become a delete: %+v %v", ups, dels)
	}
	// An object that never matched produces nothing.
	api2 := api.DeepCopy()
	api2.SetResourceVersion("2")
	g.OnUpsert(api2)
	if ups, dels, _ = g.drain(); len(ups)+len(dels) != 0 {
		t.Fatalf("non-matching object leaked: %+v %v", ups, dels)
	}

	if _, _, err := gridFilter(url.Values{"labelSelector": {"app in (web"}}, res); err == nil || !strings.Contains(err.Error(), "seletor") {
		t.Fatalf("invalid selector must fail with a user message, got %v", err)
	}
	if sel, _, err := gridFilter(url.Values{"labelSelector": {""}}, res); err != nil || sel != nil {
		t.Fatalf("empty selector: %v %v", sel, err)
	}
}

func TestJSONPathColumns(t *testing.T) {
	jp, err := parseJPCols(`[".spec.replicas","{.spec.template.spec.containers[*].image}","metadata.labels.app","{.spec.replicas","{.spec.replicas}"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	cols := jp.Columns()
	if len(cols) != 3 {
		t.Fatalf("want 3 valid columns (dedup + bad one skipped), got %+v", cols)
	}
	if cols[0].Key != "jp:{.spec.replicas}" || cols[0].Label != ".spec.replicas" {
		t.Fatalf("key/label: %+v", cols[0])
	}
	errs := jp.Errors()
	if len(errs) != 1 || !strings.Contains(errs["jp:{.spec.replicas"], "inválida") {
		t.Fatalf("bad expression must be reported: %v", errs)
	}
	u := obj("Deployment", "web", map[string]string{"app": "web"}, map[string]any{"spec": map[string]any{
		"replicas": int64(3),
		"template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": "a", "image": "nginx:1"}, map[string]any{"name": "b", "image": "busybox"},
		}}},
	}})
	r := project.Row{Cells: map[string]any{}}
	jp.Apply(u, &r)
	if r.Cells["jp:{.spec.replicas}"] != int64(3) {
		t.Fatalf("number stays a number: %#v", r.Cells)
	}
	if r.Cells["jp:{.spec.template.spec.containers[*].image}"] != "nginx:1,busybox" {
		t.Fatalf("list joined: %#v", r.Cells)
	}
	if r.Cells["jp:{.metadata.labels.app}"] != "web" {
		t.Fatalf("dotless form: %#v", r.Cells)
	}
	// Missing fields leave the cell empty.
	r2 := project.Row{Cells: map[string]any{}}
	jp.Apply(obj("Deployment", "x", nil, nil), &r2)
	if len(r2.Cells) != 0 {
		t.Fatalf("missing fields must stay empty: %#v", r2.Cells)
	}
}

func TestJSONPathColumnsSecretMasked(t *testing.T) {
	jp, err := parseJPCols(`["{.data.password}","{.data}","{.stringData.token}","{.metadata.annotations}","{.type}"]`, true)
	if err != nil {
		t.Fatal(err)
	}
	s := obj("Secret", "db", nil, map[string]any{
		"type":       "Opaque",
		"data":       map[string]any{"password": "czNjcjN0"},
		"stringData": map[string]any{"token": "plain-token"},
	})
	s.SetAnnotations(map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"czNjcjN0"}}`, "note": "x"})
	r := project.Row{Cells: map[string]any{}}
	jp.Apply(s, &r)
	for k, v := range r.Cells {
		str, _ := v.(string)
		if strings.Contains(str, "czNjcjN0") || strings.Contains(str, "plain-token") {
			t.Fatalf("secret value leaked in %s: %v", k, v)
		}
	}
	if r.Cells["jp:{.data.password}"] != redact.Mask {
		t.Fatalf("masked value expected: %#v", r.Cells)
	}
	if r.Cells["jp:{.type}"] != "Opaque" {
		t.Fatalf("non-secret fields still work: %#v", r.Cells)
	}
	// The object kind alone also triggers masking (e.g. a Secret in a list of another resource).
	jp2, _ := parseJPCols(`["{.data.password}"]`, false)
	r2 := project.Row{Cells: map[string]any{}}
	jp2.Apply(s, &r2)
	if r2.Cells["jp:{.data.password}"] != redact.Mask {
		t.Fatalf("secret by kind must be masked: %#v", r2.Cells)
	}
}

func TestJSONPathColumnsLimits(t *testing.T) {
	if jp, err := parseJPCols(`not json`, false); err == nil || jp != nil {
		t.Fatal("cols must be a JSON array")
	}
	if jp, err := parseJPCols(``, false); err != nil || jp != nil {
		t.Fatal("empty cols means none")
	}
	long := `["{.` + strings.Repeat("a", 300) + `}"]`
	jp, _ := parseJPCols(long, false)
	if len(jp.Columns()) != 0 || len(jp.Errors()) != 1 {
		t.Fatal("too long expression must be rejected")
	}
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, `"{.f`+string(rune('a'+i))+`}"`)
	}
	jp, _ = parseJPCols("["+strings.Join(many, ",")+"]", false)
	if len(jp.Columns()) != jpMaxCols {
		t.Fatalf("columns capped at %d, got %d", jpMaxCols, len(jp.Columns()))
	}
	// Huge values are cut.
	big := make([]any, 500)
	for i := range big {
		big[i] = map[string]any{"v": strings.Repeat("x", 100)}
	}
	jp, _ = parseJPCols(`["{.items[*].v}","{.items}"]`, false)
	r := project.Row{Cells: map[string]any{}}
	jp.Apply(obj("ConfigMap", "c", nil, map[string]any{"items": big}), &r)
	for k, v := range r.Cells {
		if s, _ := v.(string); len(s) > jpMaxCell+len("…") {
			t.Fatalf("%s not capped: %d", k, len(s))
		}
	}
}

func TestWithJPColumnsBeforeAge(t *testing.T) {
	cols := []project.Column{{Key: "name"}, {Key: "status"}, {Key: "age"}}
	out := withJPColumns(cols, []project.Column{{Key: "jp:{.a}"}})
	if len(out) != 4 || out[2].Key != "jp:{.a}" || out[3].Key != "age" {
		t.Fatalf("%+v", out)
	}
	if len(cols) != 3 || cols[2].Key != "age" {
		t.Fatal("input must not be modified")
	}
}

// Chained recursive descents grow exponentially and cannot be interrupted: refused up front.
func TestJSONPathRecursionLimited(t *testing.T) {
	j, err := parseJPCols(`["{..c..c..c..c}", "{..metadata.name}", "{..items[?(@.x)]}"]`, false)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	errs := j.Errors()
	if _, bad := errs["jp:{..c..c..c..c}"]; !bad {
		t.Fatalf("chained recursion accepted: %v", errs)
	}
	if _, bad := errs["jp:{..items[?(@.x)]}"]; !bad {
		t.Fatalf("recursion with filter accepted: %v", errs)
	}
	if len(j.Columns()) != 1 {
		t.Fatalf("a single recursion must still work: %+v", j.Columns())
	}
}
