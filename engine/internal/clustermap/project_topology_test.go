package clustermap

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func tobj(kind, name string, fields map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"name": name, "namespace": "shop"}}
	for k, v := range fields {
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func TestBuildProject(t *testing.T) {
	yes := true
	sel := map[string]any{"matchLabels": map[string]any{"app": "web"}}
	tmpl := map[string]any{"metadata": map[string]any{"labels": map[string]any{"app": "web"}}}
	web := tobj("Deployment", "web", map[string]any{"spec": map[string]any{"replicas": int64(2), "selector": sel, "template": tmpl}})
	web.SetLabels(map[string]string{"app": "web", "app.kubernetes.io/part-of": "shop", "app.openshift.io/runtime": "nodejs"})
	web.SetAnnotations(map[string]string{"app.openshift.io/connects-to": `[{"apiVersion":"apps/v1","kind":"Deployment","name":"db"}]`, "app.openshift.io/vcs-uri": "https://git.example/web"})
	db := tobj("Deployment", "db", map[string]any{"spec": map[string]any{"replicas": int64(1)}})
	db.SetAnnotations(map[string]string{"meta.helm.sh/release-name": "pg"})
	rs := tobj("ReplicaSet", "web-1", nil)
	rs.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", Controller: &yes}})
	pod := func(name, phase string, ready bool, waiting string) *unstructured.Unstructured {
		cs := map[string]any{"ready": ready}
		if waiting != "" {
			cs["state"] = map[string]any{"waiting": map[string]any{"reason": waiting}}
		}
		p := tobj("Pod", name, map[string]any{"status": map[string]any{"phase": phase, "containerStatuses": []any{cs}}})
		p.SetLabels(map[string]string{"app": "web"})
		p.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-1", Controller: &yes}})
		return p
	}
	svc := tobj("Service", "web", map[string]any{"spec": map[string]any{"selector": map[string]any{"app": "web"}}})
	route := tobj("Route", "web", map[string]any{"spec": map[string]any{"host": "web.apps.example", "to": map[string]any{"name": "web"}, "tls": map[string]any{"termination": "edge"}}})
	bc := tobj("BuildConfig", "web", nil)
	b1, b2 := tobj("Build", "web-1", map[string]any{"status": map[string]any{"phase": "Failed"}}), tobj("Build", "web-2", map[string]any{"status": map[string]any{"phase": "Complete"}})
	b1.SetLabels(map[string]string{"openshift.io/build-config.name": "web"})
	b1.SetAnnotations(map[string]string{"openshift.io/build.number": "1"})
	b2.SetLabels(map[string]string{"openshift.io/build-config.name": "web"})
	b2.SetAnnotations(map[string]string{"openshift.io/build.number": "2"})

	got := BuildProject(TopoInput{
		Namespace: "shop", Deployments: []*unstructured.Unstructured{web, db}, ReplicaSets: []*unstructured.Unstructured{rs},
		Pods:     []*unstructured.Unstructured{pod("web-1-a", "Running", true, ""), pod("web-1-b", "Running", false, "CrashLoopBackOff"), pod("web-1-c", "Pending", false, "")},
		Services: []*unstructured.Unstructured{svc}, Routes: []*unstructured.Unstructured{route},
		BuildConfigs: []*unstructured.Unstructured{bc}, Builds: []*unstructured.Unstructured{b1, b2},
	})
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes: %+v", got.Nodes)
	}
	var w, d TopoNode
	for _, n := range got.Nodes {
		if n.Name == "web" {
			w = n
		} else {
			d = n
		}
	}
	if w.App != "shop" || w.Runtime != "nodejs" || w.VCS == "" || w.Desired == nil || *w.Desired != 2 {
		t.Fatalf("web metadata: %+v", w)
	}
	if w.Phases.Running != 1 || w.Phases.Failed != 1 || w.Phases.Pending != 1 || len(w.Pods) != 3 {
		t.Fatalf("phases: %+v", w.Phases)
	}
	if w.URL != "https://web.apps.example" || len(w.Services) != 1 {
		t.Fatalf("url/services: %q %v", w.URL, w.Services)
	}
	if w.Build == nil || w.Build.Name != "web-2" || w.Build.Phase != "Complete" {
		t.Fatalf("build: %+v", w.Build)
	}
	if d.Release != "pg" || len(d.Pods) != 0 {
		t.Fatalf("db: %+v", d)
	}
	if len(got.Edges) != 1 || got.Edges[0] != (TopoEdge{Source: "Deployment/web", Target: "Deployment/db"}) {
		t.Fatalf("edges: %+v", got.Edges)
	}
}

func TestConnectsTo(t *testing.T) {
	if got := connectsTo(`["db","cache"]`); len(got) != 2 || got[1][1] != "cache" {
		t.Fatalf("names: %v", got)
	}
	if got := connectsTo(`not json`); len(got) != 1 {
		t.Fatalf("fallback: %v", got)
	}
}
