package ops

import (
	"context"
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

var (
	cmRes  = cluster.Resource{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	secRes = cluster.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
	depRes = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
)

func testSession(t *testing.T, readOnly bool) (*cluster.Session, *dynfake.FakeDynamicClient) {
	t.Helper()
	objs := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "app", "namespace": "demo", "resourceVersion": "7"}, "data": map[string]any{"k": "v"}}},
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "db", "namespace": "demo", "resourceVersion": "3"}, "data": map[string]any{"password": "aHVudGVyMg=="}}},
		&unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "web", "namespace": "demo", "resourceVersion": "1"}, "spec": map[string]any{"replicas": int64(1)}}},
	}
	lists := map[schema.GroupVersionResource]string{cmRes.GVR(): "ConfigMapList", secRes.GVR(): "SecretList", depRes.GVR(): "DeploymentList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, objs...)
	s := cluster.NewStaticSession(kube.Context{Name: "test"}, nil, dyn, []cluster.Resource{cmRes, secRes, depRes}, readOnly)
	s.Namespace = "demo"
	return s, dyn
}

func ref(r cluster.Resource, name string) Ref {
	return Ref{Group: r.Group, Version: r.Version, Resource: r.Resource, Namespace: "demo", Name: name}
}

// Every write refuses a read-only (production-guarded or remote) session before touching the cluster.
func TestWritesRefusedReadOnly(t *testing.T) {
	s, dyn := testSession(t, true)
	ctx := context.Background()
	checks := map[string]error{
		"apply": func() error {
			_, err := Apply(ctx, s, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: n}\n", false)
			return err
		}(),
		"delete":  Delete(ctx, s, ref(cmRes, "app")),
		"scale":   Scale(ctx, s, ref(depRes, "web"), 3),
		"restart": Restart(ctx, s, ref(depRes, "web")),
	}
	for name, err := range checks {
		if !errors.Is(err, cluster.ErrReadOnly) {
			t.Errorf("%s: got %v, want ErrReadOnly", name, err)
		}
	}
	for _, a := range dyn.Actions() {
		if a.GetVerb() != "get" && a.GetVerb() != "list" && a.GetVerb() != "watch" {
			t.Errorf("read-only session reached the cluster with %s", a.GetVerb())
		}
	}
}

func TestApplyRequiresResourceVersion(t *testing.T) {
	s, _ := testSession(t, false)
	ctx := context.Background()
	_, err := Apply(ctx, s, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: app, namespace: demo}\ndata: {k: changed}\n", false)
	if !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "resourceVersion") {
		t.Fatalf("update without resourceVersion: %v", err)
	}
	res, err := Apply(ctx, s, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: app, namespace: demo, resourceVersion: \"7\"}\ndata: {k: changed}\n", false)
	if err != nil || res.Created {
		t.Fatalf("update: %+v %v", res, err)
	}
	// A new object needs none; the session namespace is the default.
	res, err = Apply(ctx, s, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: fresh}\n", false)
	if err != nil || !res.Created || !strings.Contains(res.YAML, "namespace: demo") {
		t.Fatalf("create: %+v %v", res, err)
	}
	if _, err := Apply(ctx, s, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a}\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: b}\n", false); err == nil {
		t.Fatal("several documents must be refused")
	}
	if _, err := Apply(ctx, s, "apiVersion: example.io/v1\nkind: Widget\nmetadata: {name: w}\n", false); err == nil {
		t.Fatal("unknown kinds must be refused")
	}
}

// A Secret loaded masked cannot be saved back: the placeholder would replace the real values.
func TestApplySecretHiddenValueGuard(t *testing.T) {
	s, dyn := testSession(t, false)
	ctx := context.Background()
	v, err := Get(ctx, s, ref(secRes, "db"), false)
	if err != nil || !v.Masked || !strings.Contains(v.YAML, redact.Mask) {
		t.Fatalf("masked get: %+v %v", v, err)
	}
	for _, mask := range []string{redact.Mask, "<redacted>"} {
		y := strings.ReplaceAll(v.YAML, redact.Mask, mask)
		if _, err := Apply(ctx, s, y, false); err == nil || !strings.Contains(err.Error(), "hidden values") {
			t.Fatalf("%s: masked Secret applied: %v", mask, err)
		}
	}
	got, _ := dyn.Resource(secRes.GVR()).Namespace("demo").Get(ctx, "db", metav1.GetOptions{})
	if d, _, _ := unstructured.NestedString(got.Object, "data", "password"); d != "aHVudGVyMg==" {
		t.Fatalf("secret value changed: %q", d)
	}
	// Revealed values apply.
	rv, _ := Get(ctx, s, ref(secRes, "db"), true)
	if _, err := Apply(ctx, s, rv.YAML, true); err != nil {
		t.Fatalf("revealed secret dry-run: %v", err)
	}
}

func TestScaleAndRestart(t *testing.T) {
	s, dyn := testSession(t, false)
	ctx := context.Background()
	if err := Scale(ctx, s, ref(depRes, "web"), -1); err == nil {
		t.Fatal("negative replicas")
	}
	if err := Scale(ctx, s, ref(depRes, "web"), 3); err != nil {
		t.Fatal(err)
	}
	if err := Restart(ctx, s, ref(depRes, "web")); err != nil {
		t.Fatal(err)
	}
	got, _ := dyn.Resource(depRes.GVR()).Namespace("demo").Get(ctx, "web", metav1.GetOptions{})
	if r, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas"); r != 3 {
		t.Errorf("replicas = %d", r)
	}
	if a, _, _ := unstructured.NestedString(got.Object, "spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt"); a == "" {
		t.Error("restart annotation missing")
	}
	if err := Delete(ctx, s, ref(cmRes, "app")); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(cmRes.GVR()).Namespace("demo").Get(ctx, "app", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("deleted: %v", err)
	}
}
