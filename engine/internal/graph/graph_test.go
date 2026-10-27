package graph

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func objs(t *testing.T, docs ...string) []*unstructured.Unstructured {
	t.Helper()
	var out []*unstructured.Unstructured
	for _, d := range docs {
		// Decode like the API machinery does (integers stay int64).
		j, err := yaml.YAMLToJSON([]byte(d))
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{}
		if err := u.UnmarshalJSON(j); err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

func pod(name, owner string, labels string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: demo
  labels: {%s}
  ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: %s, uid: u-%s}]
spec:
  nodeName: n1
  serviceAccountName: default
  volumes:
    - name: cfg
      configMap: {name: app-config}
    - name: data
      persistentVolumeClaim: {claimName: data}
  containers:
    - name: c
      image: nginx
      envFrom: [{secretRef: {name: missing-secret}}]
status:
  phase: Running
  containerStatuses: [{name: c, ready: true, restartCount: 0, state: {running: {}}}]
`, name, labels, owner, owner)
}

func fixture(t *testing.T, replicas int) Input {
	podDocs := []string{}
	for i := 0; i < replicas; i++ {
		podDocs = append(podDocs, pod(fmt.Sprintf("web-abc-%d", i), "web-abc", "app: web"))
	}
	podDocs = append(podDocs, pod("other-1", "other-x", "app: other"))
	return Input{
		Namespace:  "demo",
		ConfigMaps: map[string]bool{"app-config": true},
		Secrets:    map[string]bool{},
		Objects: map[string][]*unstructured.Unstructured{
			"/pods": objs(t, podDocs...),
			"apps/replicasets": objs(t, `apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: web-abc, namespace: demo, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d1}]}
spec: {replicas: 3}
status: {replicas: 3}`, `apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: web-old, namespace: demo, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d1}]}
spec: {replicas: 0}
status: {replicas: 0}`, `apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: other-x, namespace: demo}
spec: {replicas: 1}
status: {replicas: 1}`),
			"apps/deployments": objs(t, `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: demo}
spec: {replicas: 3}
status: {replicas: 3, readyReplicas: 3, updatedReplicas: 3, availableReplicas: 3}`),
			"/services": objs(t, `apiVersion: v1
kind: Service
metadata: {name: web, namespace: demo}
spec: {selector: {app: web}}`),
			"networking.k8s.io/ingresses": objs(t, `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: web, namespace: demo}
spec:
  rules: [{http: {paths: [{path: /, backend: {service: {name: web, port: {number: 80}}}}]}}]`),
			"/persistentvolumeclaims": objs(t, `apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data, namespace: demo}
spec: {volumeName: pv-1, storageClassName: standard}
status: {phase: Bound}`),
			"/serviceaccounts": objs(t, `apiVersion: v1
kind: ServiceAccount
metadata: {name: default, namespace: demo}`),
		},
	}
}

func has(g *Graph, from, to, rel string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Rel == rel {
			return true
		}
	}
	return false
}

func node(g *Graph, id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func TestBuildRelations(t *testing.T) {
	g := Build(fixture(t, 3))
	for _, e := range [][3]string{
		{"Deployment/demo/web", "ReplicaSet/demo/web-abc", "owns"},
		{"ReplicaSet/demo/web-abc", "Pod/demo/web-abc-0", "owns"},
		{"Service/demo/web", "Pod/demo/web-abc-1", "selects"},
		{"Ingress/demo/web", "Service/demo/web", "routes"},
		{"Pod/demo/web-abc-0", "ConfigMap/demo/app-config", "mounts"},
		{"Pod/demo/web-abc-0", "PersistentVolumeClaim/demo/data", "mounts"},
		{"PersistentVolumeClaim/demo/data", "PersistentVolume//pv-1", "bound"},
		{"PersistentVolumeClaim/demo/data", "StorageClass//standard", "class"},
		{"Pod/demo/web-abc-0", "Node//n1", "runs on"},
		{"Pod/demo/web-abc-0", "ServiceAccount/demo/default", "uses"},
		{"Pod/demo/web-abc-0", "Secret/demo/missing-secret", "env"},
	} {
		if !has(g, e[0], e[1], e[2]) {
			t.Errorf("missing edge %v", e)
		}
	}
	if has(g, "Service/demo/web", "Pod/demo/other-1", "selects") {
		t.Error("service must not select pods with other labels")
	}
	if node(g, "ReplicaSet/demo/web-old") != nil {
		t.Error("idle old ReplicaSets are hidden")
	}
	if s := node(g, "Secret/demo/missing-secret"); s == nil || !s.Missing || s.Health != "error" {
		t.Errorf("missing secret must be flagged: %+v", s)
	}
	if c := node(g, "ConfigMap/demo/app-config"); c == nil || c.Missing {
		t.Errorf("existing configmap flagged as missing: %+v", c)
	}
}

func TestNeighborhoodStopsAtSharedNodes(t *testing.T) {
	g := Neighborhood(Build(fixture(t, 3)), "Deployment/demo/web", 6)
	if node(g, "Ingress/demo/web") == nil || node(g, "ConfigMap/demo/app-config") == nil || node(g, "StorageClass//standard") == nil {
		t.Fatal("deployment topology must reach the ingress and its config")
	}
	// other-1 shares the ConfigMap, PVC and Node but is otherwise unrelated.
	if node(g, "Pod/demo/other-1") != nil {
		t.Error("traversal must not continue through shared nodes")
	}
}

func TestAggregate(t *testing.T) {
	g := Aggregate(Build(fixture(t, 10)), "")
	agg := node(g, "Pods/ReplicaSet/demo/web-abc")
	if agg == nil || agg.Count != 10 || agg.Pods["ok"] != 10 {
		t.Fatalf("aggregate: %+v", agg)
	}
	if node(g, "Pod/demo/web-abc-3") != nil {
		t.Error("aggregated pods must be removed")
	}
	if !has(g, "Service/demo/web", "Pods/ReplicaSet/demo/web-abc", "selects") || !has(g, "Pods/ReplicaSet/demo/web-abc", "ConfigMap/demo/app-config", "mounts") {
		t.Error("edges must be rewired to the aggregate")
	}
	kept := Aggregate(Build(fixture(t, 10)), "Pod/demo/web-abc-3")
	if node(kept, "Pod/demo/web-abc-3") == nil {
		t.Error("the root pod must stay visible")
	}
}

// Pods of a controller from a CRD (Kruise CloneSet) aggregate under it, the aggregate names its owner
// by group and kind, and a CRD named like a built-in kind is a different node.
func TestCRDOwnerAndHomonymNodes(t *testing.T) {
	var docs []string
	for i := 0; i < AggregateAbove+2; i++ {
		docs = append(docs, fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata: {name: cs-%d, namespace: demo, ownerReferences: [{apiVersion: apps.kruise.io/v1alpha1, kind: CloneSet, name: cs, uid: cs, controller: true}]}
status: {phase: Running, containerStatuses: [{name: c, ready: true, state: {running: {}}}]}`, i))
	}
	in := Input{Namespace: "demo", Objects: map[string][]*unstructured.Unstructured{
		"/pods": objs(t, docs...),
		"apps/deployments": objs(t, `apiVersion: apps/v1
kind: Deployment
metadata: {name: cs, namespace: demo}`),
	}}
	g := Build(in)
	root := gid("apps.kruise.io", "CloneSet", "demo", "cs")
	if root != "CloneSet.apps.kruise.io/demo/cs" || node(g, root) == nil || !has(g, root, "Pod/demo/cs-0", "owns") {
		t.Fatalf("CRD owner missing: %s %+v", root, g.Edges)
	}
	if has(g, "Deployment/demo/cs", "Pod/demo/cs-0", "owns") {
		t.Error("the built-in Deployment with the same name must not own the CRD's pods")
	}
	agg := node(Aggregate(Neighborhood(g, root, 8), root), "Pods/"+root)
	if agg == nil || agg.Owner == nil || *agg.Owner != (Owner{Group: "apps.kruise.io", Kind: "CloneSet", Name: "cs"}) {
		t.Fatalf("aggregate owner: %+v", agg)
	}
	if gid("", "Deployment", "demo", "cs") != "Deployment/demo/cs" || gid("apps", "Deployment", "demo", "cs") != "Deployment/demo/cs" {
		t.Error("built-in ids must keep the Kind/namespace/name format")
	}
}

func TestAppOnNodes(t *testing.T) {
	g := Build(fixture(t, 3))
	if p := node(g, "Pod/demo/web-abc-0"); p == nil || p.App != "web" {
		t.Errorf("pod app: %+v", p)
	}
	if p := node(g, "Pod/demo/other-1"); p == nil || p.App != "other" {
		t.Errorf("other pod app: %+v", p)
	}
	if c := node(g, "Node//n1"); c != nil && c.App != "" {
		t.Errorf("referenced nodes have no app: %+v", c)
	}
	agg := node(Aggregate(Build(fixture(t, 10)), ""), "Pods/ReplicaSet/demo/web-abc")
	if agg == nil || agg.App != "web" {
		t.Errorf("aggregate must carry the pods' app: %+v", agg)
	}
}
