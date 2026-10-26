package clustermap

import (
	"fmt"
	"testing"

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

func pod(t *testing.T, name, owner, ownerKind, phase string, extra string) *unstructured.Unstructured {
	own := ""
	if owner != "" {
		own = fmt.Sprintf("ownerReferences: [{apiVersion: apps/v1, kind: %s, name: %s, controller: true}]", ownerKind, owner)
	}
	return obj(t, fmt.Sprintf(`{apiVersion: v1, kind: Pod, metadata: {name: %s, namespace: demo, uid: %s, labels: {app: web}, %s}, spec: {containers: [{name: c}], %s}, status: {phase: %s, containerStatuses: [{name: c, ready: %v, restartCount: 0, state: {running: {}}}]}}`,
		name, name, own, extra, phase, phase == "Running"))
}

func TestBuild(t *testing.T) {
	in := Input{
		Namespaces: []*unstructured.Unstructured{obj(t, `{kind: Namespace, metadata: {name: demo}, status: {phase: Active}}`), obj(t, `{kind: Namespace, metadata: {name: empty}}`)},
		Deployments: []*unstructured.Unstructured{
			obj(t, `{kind: Deployment, metadata: {name: web, namespace: demo, uid: d1, labels: {app: shop}}, spec: {replicas: 2, template: {metadata: {labels: {app: web}}, spec: {volumes: [{name: v, persistentVolumeClaim: {claimName: data}}]}}}, status: {readyReplicas: 2, updatedReplicas: 2, availableReplicas: 2}}`),
			obj(t, `{kind: Deployment, metadata: {name: solo, namespace: demo, uid: d2}, spec: {replicas: 0, template: {metadata: {labels: {app: solo}}}}}`),
		},
		ReplicaSets: []*unstructured.Unstructured{obj(t, `{kind: ReplicaSet, metadata: {name: web-abc, namespace: demo, uid: r1, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, controller: true}]}}`)},
		CronJobs:    []*unstructured.Unstructured{obj(t, `{kind: CronJob, metadata: {name: nightly, namespace: demo, uid: c1, labels: {app: shop}}, spec: {schedule: "0 1 * * *"}}`)},
		Jobs:        []*unstructured.Unstructured{obj(t, `{kind: Job, metadata: {name: nightly-1, namespace: demo, uid: j1, ownerReferences: [{apiVersion: batch/v1, kind: CronJob, name: nightly, controller: true}]}}`)},
		Pods: []*unstructured.Unstructured{
			pod(t, "web-abc-1", "web-abc", "ReplicaSet", "Running", ""),
			pod(t, "web-abc-2", "web-abc", "ReplicaSet", "Pending", ""),
			pod(t, "nightly-1-x", "nightly-1", "Job", "Running", ""),
			pod(t, "bare", "", "", "Running", "volumes: [{name: v, persistentVolumeClaim: {claimName: other}}]"),
		},
		Services: []*unstructured.Unstructured{obj(t, `{kind: Service, metadata: {name: web-svc, namespace: demo}, spec: {selector: {app: web}}}`)},
		PVCs:     []*unstructured.Unstructured{obj(t, `{kind: PersistentVolumeClaim, metadata: {name: data, namespace: demo}}`)},
	}
	m := Build(in)
	if m.Scope != "cluster" || len(m.Namespaces) != 2 || m.Namespaces[0].Name != "demo" {
		t.Fatalf("namespaces: %+v", m.Namespaces)
	}
	d := m.Namespaces[0]
	if d.Pods.Total != 4 || d.Pods.OK != 3 || d.Pods.Warn != 1 || d.Services != 1 || d.PVCs != 1 || d.Status != "Active" {
		t.Fatalf("ns counts: %+v", d)
	}
	var shop *App
	for i := range d.Apps {
		if d.Apps[i].Name == "shop" {
			shop = &d.Apps[i]
		}
	}
	if shop == nil || len(shop.Workloads) != 2 {
		t.Fatalf("apps: %+v", d.Apps)
	}
	var web *Workload
	for i := range shop.Workloads {
		if shop.Workloads[i].Name == "web" {
			web = &shop.Workloads[i]
		}
	}
	if web == nil || web.Pods.Total != 2 || web.Pods.Warn != 1 || web.PVCs != 1 || len(web.Services) != 1 || web.Ready != "2/2" {
		t.Fatalf("web: %+v", web)
	}
	found := map[string]Workload{}
	for _, a := range d.Apps {
		for _, w := range a.Workloads {
			found[w.Kind+"/"+w.Name] = w
		}
	}
	if w := found["CronJob/nightly"]; w.Pods.Total != 1 {
		t.Fatalf("cronjob pods: %+v", w)
	}
	if w := found["Pod/bare"]; w.Pods.Total != 1 || w.PVCs != 1 {
		t.Fatalf("bare pod: %+v", w)
	}
	if _, ok := found["Deployment/solo"]; !ok {
		t.Fatal("empty deployment missing")
	}
	if len(m.Namespaces[1].Apps) != 0 {
		t.Fatal("empty ns should have no apps")
	}
}

func TestTruncate(t *testing.T) {
	var pods []*unstructured.Unstructured
	for i := 0; i < MaxWorkloads+10; i++ {
		pods = append(pods, pod(t, fmt.Sprintf("p%d", i), "", "", "Running", ""))
	}
	m := Build(Input{Pods: pods})
	n := 0
	for _, a := range m.Namespaces[0].Apps {
		n += len(a.Workloads)
	}
	if !m.Truncated || n != MaxWorkloads {
		t.Fatalf("truncated=%v n=%d", m.Truncated, n)
	}
}
