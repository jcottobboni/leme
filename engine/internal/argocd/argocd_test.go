package argocd

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

var (
	appRes  = cluster.Resource{Group: Group, Version: "v1alpha1", Resource: "applications", Kind: "Application", Namespaced: true}
	projRes = cluster.Resource{Group: Group, Version: "v1alpha1", Resource: "appprojects", Kind: "AppProject", Namespaced: true}
	depRes  = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	svcRes  = cluster.Resource{Group: "", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true}
)

func application(name string, mutate func(o map[string]any)) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": "argocd", "resourceVersion": "5", "uid": "a1"},
		"spec": map[string]any{
			"project":     "default",
			"source":      map[string]any{"repoURL": "https://example.com/apps.git", "path": "guestbook", "targetRevision": "main"},
			"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": "guestbook"},
		},
		"status": map[string]any{
			"sync":   map[string]any{"status": "OutOfSync", "revision": "abc123"},
			"health": map[string]any{"status": "Healthy"},
			"resources": []any{
				map[string]any{"group": "apps", "version": "v1", "kind": "Deployment", "namespace": "guestbook", "name": "web", "status": "OutOfSync", "health": map[string]any{"status": "Healthy"}},
				map[string]any{"version": "v1", "kind": "Service", "namespace": "guestbook", "name": "web", "status": "Synced", "health": map[string]any{"status": "Healthy"}},
				map[string]any{"version": "v1", "kind": "Service", "namespace": "guestbook", "name": "gone", "status": "OutOfSync"},
			},
			"conditions": []any{map[string]any{"type": "SyncError", "message": "boom", "lastTransitionTime": "2026-10-01T00:00:00Z"}},
			"history": []any{
				map[string]any{"id": int64(1), "revision": "old111", "deployedAt": "2026-09-01T00:00:00Z"},
				map[string]any{"id": int64(2), "revision": "abc000", "deployedAt": "2026-10-01T00:00:00Z", "source": map[string]any{"repoURL": "https://example.com/apps.git", "path": "guestbook"}},
			},
			"operationState": map[string]any{"phase": "Succeeded", "message": "ok", "operation": map[string]any{"sync": map[string]any{"revision": "abc000"}}},
		},
	}
	if mutate != nil {
		mutate(o)
	}
	return &unstructured.Unstructured{Object: o}
}

func project() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "AppProject",
		"metadata": map[string]any{"name": "default", "namespace": "argocd"},
		"spec":     map[string]any{"description": "padrão", "sourceRepos": []any{"*"}, "destinations": []any{map[string]any{"server": "*", "namespace": "*"}}},
	}}
}

func obj(apiVersion, kind, ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": apiVersion, "kind": kind, "metadata": map[string]any{"name": name, "namespace": ns}}}
}

func newSession(t *testing.T, resources []cluster.Resource, objs ...runtime.Object) *cluster.Session {
	t.Helper()
	gvrs := map[schema.GroupVersionResource]string{
		appRes.GVR(): "ApplicationList", projRes.GVR(): "AppProjectList", depRes.GVR(): "DeploymentList", svcRes.GVR(): "ServiceList",
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, objs...)
	return cluster.NewStaticSession(kube.Context{ID: "c1", Name: "kind"}, fake.NewSimpleClientset(), dyn, resources, false)
}

func TestListWithoutCRDs(t *testing.T) {
	s := newSession(t, []cluster.Resource{depRes})
	ov, err := List(context.Background(), s, "")
	if err != nil {
		t.Fatal(err)
	}
	if ov.Installed || ov.Hint == "" || ov.Apps == nil || len(ov.Apps) != 0 {
		t.Fatalf("expected not installed with hint: %+v", ov)
	}
	if _, err := Describe(context.Background(), s, "argocd", "x"); err == nil {
		t.Fatal("detail without CRDs must fail")
	}
}

func TestListParsesApps(t *testing.T) {
	multi := application("multi", func(o map[string]any) {
		spec := o["spec"].(map[string]any)
		delete(spec, "source")
		spec["sources"] = []any{
			map[string]any{"repoURL": "https://charts.example.com", "chart": "redis", "targetRevision": "18.1.0"},
			map[string]any{"repoURL": "https://example.com/values.git", "targetRevision": "main"},
		}
		spec["destination"] = map[string]any{"name": "prod-eu", "namespace": "cache"}
		spec["syncPolicy"] = map[string]any{"automated": map[string]any{"prune": true}}
		st := o["status"].(map[string]any)
		st["sync"] = map[string]any{"status": "Synced", "revisions": []any{"18.1.0", "def456"}}
		delete(st, "health")
		st["operationState"] = map[string]any{"phase": "Running"}
	})
	s := newSession(t, []cluster.Resource{appRes, projRes}, application("guestbook", nil), multi, project())
	ov, err := List(context.Background(), s, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Installed || len(ov.Apps) != 2 || len(ov.Projects) != 1 {
		t.Fatalf("unexpected overview: %+v", ov)
	}
	g := ov.Apps[0]
	if g.Name != "guestbook" || g.Sync != "OutOfSync" || g.Health != "Healthy" || g.Project != "default" || g.Revision != "abc123" ||
		g.Source == nil || g.Source.Path != "guestbook" || g.Source.TargetRevision != "main" || !g.Local || g.OperationPhase != "Succeeded" ||
		g.Destination.Namespace != "guestbook" || g.Automated {
		t.Fatalf("guestbook: %+v", g)
	}
	m := ov.Apps[1]
	if m.Source != nil || len(m.Sources) != 2 || m.Sources[0].Chart != "redis" || m.Revision != "18.1.0" || m.Health != "Unknown" ||
		m.Local || m.Destination.Name != "prod-eu" || !m.Automated || m.OperationPhase != "Running" {
		t.Fatalf("multi: %+v", m)
	}
	if p := ov.Projects[0]; p.Description != "padrão" || len(p.SourceRepos) != 1 || len(p.Destinations) != 1 {
		t.Fatalf("project: %+v", p)
	}
	// Namespace filter.
	ov, _ = List(context.Background(), s, "other")
	if len(ov.Apps) != 0 {
		t.Fatalf("namespace filter: %+v", ov.Apps)
	}
}

func TestDescribeChecksLocalResources(t *testing.T) {
	s := newSession(t, []cluster.Resource{appRes, projRes, depRes, svcRes}, application("guestbook", nil),
		obj("apps/v1", "Deployment", "guestbook", "web"), obj("v1", "Service", "guestbook", "web"))
	d, err := Describe(context.Background(), s, "argocd", "guestbook")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Checked || len(d.Resources) != 3 || len(d.Conditions) != 1 || len(d.History) != 2 || d.OperationState == nil {
		t.Fatalf("detail: %+v", d)
	}
	got := map[string]*bool{}
	for _, r := range d.Resources {
		got[r.Kind+"/"+r.Name] = r.Exists
	}
	if got["Deployment/web"] == nil || !*got["Deployment/web"] || got["Service/web"] == nil || !*got["Service/web"] || got["Service/gone"] == nil || *got["Service/gone"] {
		t.Fatalf("exists: %+v", d.Resources)
	}
	if d.History[0].ID != 2 || d.History[0].Source == nil || d.History[1].Revision != "old111" {
		t.Fatalf("history must be newest first: %+v", d.History)
	}
	if d.OperationState.Phase != "Succeeded" || d.OperationState.Revision != "abc000" || d.Conditions[0].Type != "SyncError" {
		t.Fatalf("state: %+v %+v", d.OperationState, d.Conditions)
	}

	// Remote destination: nothing is looked up in the connected cluster.
	remote := application("remote", func(o map[string]any) {
		o["spec"].(map[string]any)["destination"] = map[string]any{"server": "https://10.0.0.1:6443", "namespace": "guestbook"}
	})
	s = newSession(t, []cluster.Resource{appRes, projRes, depRes, svcRes}, remote)
	d, err = Describe(context.Background(), s, "argocd", "remote")
	if err != nil {
		t.Fatal(err)
	}
	if d.Checked || d.Resources[0].Exists != nil {
		t.Fatalf("remote destination must not be checked: %+v", d)
	}
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncPatchShape(t *testing.T) {
	p, err := SyncPatch(application("g", nil), false)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation":{"initiatedBy":{"username":"leme"},"sync":{"prune":false,"revision":"abc123"}}}`
	if got := asJSON(t, p); got != want {
		t.Fatalf("sync patch:\n got %s\nwant %s", got, want)
	}
	// Without status.sync.revision, the source's targetRevision is used.
	noRev := application("g", func(o map[string]any) { delete(o["status"].(map[string]any)["sync"].(map[string]any), "revision") })
	p, _ = SyncPatch(noRev, true)
	want = `{"operation":{"initiatedBy":{"username":"leme"},"sync":{"prune":true,"revision":"main"}}}`
	if got := asJSON(t, p); got != want {
		t.Fatalf("sync patch fallback:\n got %s\nwant %s", got, want)
	}
	// Multi-source: one revision per source.
	multi := application("m", func(o map[string]any) {
		spec := o["spec"].(map[string]any)
		delete(spec, "source")
		spec["sources"] = []any{map[string]any{"repoURL": "a", "targetRevision": "1.0"}, map[string]any{"repoURL": "b"}}
	})
	p, _ = SyncPatch(multi, false)
	if got := p["operation"].(map[string]any)["sync"].(map[string]any)["revisions"]; !reflect.DeepEqual(got, []any{"1.0", "HEAD"}) {
		t.Fatalf("multi-source revisions: %v", got)
	}
}

func TestSyncRefusedWhileRunningOrPending(t *testing.T) {
	running := application("g", func(o map[string]any) {
		o["status"].(map[string]any)["operationState"] = map[string]any{"phase": "Running"}
	})
	if _, err := SyncPatch(running, false); !errors.Is(err, ErrOperationRunning) {
		t.Fatalf("running must be refused: %v", err)
	}
	pending := application("g", func(o map[string]any) {
		o["operation"] = map[string]any{"sync": map[string]any{"revision": "x"}}
	})
	if _, err := SyncPatch(pending, false); !errors.Is(err, ErrOperationPending) {
		t.Fatalf("pending must be refused: %v", err)
	}
}

func TestRefreshPatchShape(t *testing.T) {
	if got := asJSON(t, RefreshPatch(false)); got != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"normal"}}}` {
		t.Fatal(got)
	}
	if got := asJSON(t, RefreshPatch(true)); got != `{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}` {
		t.Fatal(got)
	}
}

func TestAllowOnlyApplication(t *testing.T) {
	if err := Allow(Group, "Application"); err != nil {
		t.Fatal(err)
	}
	for _, gk := range [][2]string{{Group, "AppProject"}, {Group, "ApplicationSet"}, {"apps", "Deployment"}, {"", "Secret"}, {"", "Application"}, {"rbac.authorization.k8s.io", "ClusterRole"}} {
		if Allow(gk[0], gk[1]) == nil {
			t.Fatalf("%s/%s must be refused", gk[0], gk[1])
		}
	}
}
