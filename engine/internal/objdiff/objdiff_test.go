package objdiff

import (
	"encoding/json"
	"reflect"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func TestNormalizeDropsBookkeeping(t *testing.T) {
	u := obj(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: demo
  uid: abc
  resourceVersion: "42"
  generation: 3
  creationTimestamp: "2026-01-01T00:00:00Z"
  selfLink: /x
  managedFields: [{manager: kubectl}]
  ownerReferences: [{kind: X, name: y, uid: z, apiVersion: v1}]
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: "{}"
    deployment.kubernetes.io/revision: "7"
  labels: {}
spec: {replicas: 3}
status: {readyReplicas: 3}
`)
	n := Normalize(u, NormalizeOptions{DropNamespace: true})
	want := obj(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  ownerReferences: [{kind: X, name: y, apiVersion: v1}]
spec: {replicas: 3}
`)
	if !reflect.DeepEqual(n.Object, want.Object) {
		t.Fatalf("normalized = %v\nwant %v", n.Object, want.Object)
	}
	if u.GetUID() != "abc" || u.Object["status"] == nil {
		t.Fatal("Normalize must not modify its input")
	}
	if Normalize(u, NormalizeOptions{}).GetNamespace() != "demo" {
		t.Fatal("namespace kept without DropNamespace")
	}
}

func TestDiffAppliesAsJSONPatch(t *testing.T) {
	cases := []struct{ a, b string }{
		{`{"spec":{"image":"a","x/y":1,"t~":2}}`, `{"spec":{"image":"b","x/y":1}}`},
		{`{"c":[{"image":"a"},{"image":"b"}]}`, `{"c":[{"image":"a2"}]}`},
		{`{"c":[1]}`, `{"c":[1,2,3]}`},
		{`{"c":[1,2,3]}`, `{"c":[]}`},
		{`{"a":{"b":1}}`, `{"a":"str"}`},
		{`{}`, `{"l":{"app":"web"}}`},
	}
	for _, c := range cases {
		var a, b any
		_ = json.Unmarshal([]byte(c.a), &a)
		_ = json.Unmarshal([]byte(c.b), &b)
		ops := Diff(a, b)
		if len(ops) == 0 {
			t.Fatalf("%s -> %s: no ops", c.a, c.b)
		}
		raw, _ := json.Marshal(ops)
		p, err := jsonpatch.DecodePatch(raw)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Apply([]byte(c.a))
		if err != nil {
			t.Fatalf("%s: apply %s: %v", c.a, raw, err)
		}
		if !jsonpatch.Equal(got, []byte(c.b)) {
			t.Fatalf("%s + %s = %s, want %s", c.a, raw, got, c.b)
		}
	}
	if ops := Diff(map[string]any{"a": 1.0}, map[string]any{"a": 1.0}); len(ops) != 0 {
		t.Fatalf("equal docs: %v", ops)
	}
}

func TestDiffImageChange(t *testing.T) {
	a := obj(t, `{spec: {template: {spec: {containers: [{name: app, image: "busybox:1.36"}]}}}}`)
	b := obj(t, `{spec: {template: {spec: {containers: [{name: app, image: "busybox:latest"}]}}}}`)
	ops := Diff(a.Object, b.Object)
	if len(ops) != 1 || ops[0].Op != "replace" || ops[0].Path != "/spec/template/spec/containers/0/image" || ops[0].Old != "busybox:1.36" || ops[0].Value != "busybox:latest" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestLookup(t *testing.T) {
	doc := map[string]any{"a": []any{map[string]any{"x/y": "v"}}}
	if v, ok := Lookup(doc, "/a/0/x~1y"); !ok || v != "v" {
		t.Fatalf("lookup = %v %v", v, ok)
	}
	for _, p := range []string{"/a/1", "/b", "/a/0/x~1y/z", "a"} {
		if _, ok := Lookup(doc, p); ok {
			t.Fatalf("%s should not resolve", p)
		}
	}
}
