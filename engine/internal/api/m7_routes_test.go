package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

var (
	m7DepRes = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m7SecRes = cluster.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
)

func m7Deploy(ns, image string, extra map[string]any) *unstructured.Unstructured {
	md := map[string]any{"name": "web", "namespace": ns, "uid": "uid-" + ns, "resourceVersion": "1" + ns, "creationTimestamp": "2026-10-27T10:00:00Z",
		"annotations": map[string]any{"deployment.kubernetes.io/revision": ns}}
	for k, v := range extra {
		md[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": md,
		"spec":   map[string]any{"replicas": int64(3), "template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "nginx", "image": image}}}}},
		"status": map[string]any{"readyReplicas": int64(3)}}}
}

func m7Secret(ns, pw, user string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "db", "namespace": ns, "resourceVersion": "5"},
		"data":     map[string]any{"password": pw, "user": user}}}
}

func m7Session(id string, objs ...runtime.Object) *cluster.Session {
	gvrs := map[k8sschema.GroupVersionResource]string{m7DepRes.GVR(): "DeploymentList", m7SecRes.GVR(): "SecretList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, objs...)
	return cluster.NewStaticSession(kube.Context{ID: id, Name: id, Server: "https://" + id}, nil, dyn, []cluster.Resource{m7DepRes, m7SecRes}, true)
}

func TestCompareObjectsNormalizesAndMasks(t *testing.T) {
	a := m7Session("a", m7Deploy("demo", "nginx:1.27", nil), m7Deploy("copy", "nginx:1.27", nil), m7Secret("demo", "czNjcjN0", "YWRtaW4="))
	b := m7Session("b", m7Deploy("demo", "nginx:1.28", nil), m7Secret("demo", "b3RoZXI=", "YWRtaW4="))
	get := func(id string) (*cluster.Session, bool) {
		switch id {
		case "a":
			return a, true
		case "b":
			return b, true
		}
		return nil, false
	}
	ctx := context.Background()
	dep := func(c, ns string) diffSide {
		return diffSide{Cluster: c, Group: "apps", Resource: "deployments", Namespace: ns, Name: "web"}
	}
	// Same object in two namespaces: uid, resourceVersion, revision, status and namespace are noise.
	same := compareObjects(ctx, get, dep("a", "demo"), dep("a", "copy"))
	if !same.Equal || !same.NamespaceIgnored || !same.Left.Found || same.Left.YAML != same.Right.YAML {
		t.Fatalf("namespaces: %+v", same)
	}
	for _, noise := range []string{"uid", "resourceVersion", "creationTimestamp", "revision", "status", "namespace"} {
		if strings.Contains(same.Left.YAML, noise) {
			t.Fatalf("%s left in %s", noise, same.Left.YAML)
		}
	}
	// Two clusters: only the real difference.
	diff := compareObjects(ctx, get, dep("a", "demo"), dep("b", "demo"))
	if diff.Equal || diff.NamespaceIgnored || !strings.Contains(diff.Left.YAML, "nginx:1.27") || !strings.Contains(diff.Right.YAML, "nginx:1.28") {
		t.Fatalf("clusters: %+v", diff)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Path != "/spec/template/spec/containers/0/image" {
		t.Fatalf("changes: %+v", diff.Changes)
	}
	// Missing on one side, unknown cluster.
	if r := compareObjects(ctx, get, dep("a", "demo"), dep("b", "nope")); r.Right.Found || r.Equal || r.Right.Error != "" {
		t.Fatalf("missing: %+v", r)
	}
	if r := compareObjects(ctx, get, dep("a", "demo"), dep("zz", "demo")); r.Right.Error == "" {
		t.Fatalf("unknown cluster: %+v", r)
	}
	// Secrets: values never leave, the differing key is named.
	sec := func(c string) diffSide {
		return diffSide{Cluster: c, Resource: "secrets", Namespace: "demo", Name: "db"}
	}
	s := compareObjects(ctx, get, sec("a"), sec("b"))
	for _, v := range []string{"czNjcjN0", "b3RoZXI=", "YWRtaW4="} {
		if strings.Contains(s.Left.YAML+s.Right.YAML, v) {
			t.Fatalf("secret value %s in %+v", v, s)
		}
	}
	if s.Equal || len(s.HiddenDiffer) != 1 || s.HiddenDiffer[0] != "password" || s.Left.YAML != s.Right.YAML {
		t.Fatalf("secrets: %+v", s)
	}
}

func TestHistoryRouteRecordsInformerChanges(t *testing.T) {
	gvrs := map[k8sschema.GroupVersionResource]string{m7DepRes.GVR(): "DeploymentList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, m7Deploy("demo", "nginx:1.27", nil))
	sess := cluster.NewStaticSession(kube.Context{ID: "c1", Name: "kind-test", Server: "https://127.0.0.1:1"}, nil, dyn, []cluster.Resource{m7DepRes}, false)
	sess.Informers = cluster.NewInformerManager(dyn, time.Minute)
	defer sess.Informers.StopAll()
	dir := t.TempDir()
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	s.attachHistory(sess)

	ctx := context.Background()
	key := cluster.Key{GVR: m7DepRes.GVR(), Namespace: "demo"}
	if _, err := sess.Informers.List(ctx, key); err != nil {
		t.Fatal(err)
	}
	cur, _ := dyn.Resource(m7DepRes.GVR()).Namespace("demo").Get(ctx, "web", metav1.GetOptions{})
	_ = unstructured.SetNestedField(cur.Object, []any{map[string]any{"name": "nginx", "image": "nginx:1.28"}}, "spec", "template", "spec", "containers")
	cur.SetResourceVersion("2")
	if _, err := dyn.Resource(m7DepRes.GVR()).Namespace("demo").Update(ctx, cur, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	var page historyPage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		s.getHistory(w, httptest.NewRequest("GET", "/api/clusters/c1/history?group=apps&resource=deployments&namespace=demo&name=web", nil), sess)
		page = historyPage{}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(page.Entries) != 1 || page.RetentionDays != 7 {
		t.Fatalf("page = %+v", page)
	}
	e := page.Entries[0]
	if e.Type != "updated" || len(e.Ops) != 1 || e.Ops[0].Value != "nginx:1.28" || e.Ops[0].Old != "nginx:1.27" {
		t.Fatalf("entry = %+v", e)
	}
	// Persisted: a new server on the same config dir (engine restart) still has it.
	s2 := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", ConfigDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := httptest.NewRecorder()
	s2.getHistory(w, httptest.NewRequest("GET", "/api/clusters/c1/history?namespace=demo", nil), sess)
	if !strings.Contains(w.Body.String(), "nginx:1.28") {
		t.Fatalf("after restart: %s", w.Body.String())
	}
}
