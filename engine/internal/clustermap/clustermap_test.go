package clustermap

import (
	"encoding/json"
	"fmt"
	"strings"
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
	if m.Compact || m.Scope != "cluster" || len(m.Namespaces) != 2 || m.Namespaces[0].Name != "demo" {
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

// barePods builds n Running bare pods in namespace demo without YAML parsing (fast enough for 25k).
func barePods(n int, labels map[string]any) []*unstructured.Unstructured {
	out := make([]*unstructured.Unstructured, n)
	for i := range out {
		out[i] = &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": fmt.Sprintf("p%d", i), "namespace": "demo", "uid": fmt.Sprintf("u%d", i), "labels": labels},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "c"}}},
			"status": map[string]any{"phase": "Running", "containerStatuses": []any{
				map[string]any{"name": "c", "ready": true, "restartCount": int64(0), "state": map[string]any{"running": map[string]any{}}},
			}},
		}}
	}
	return out
}

func count(m *ClusterMap) int {
	n := 0
	for _, ns := range m.Namespaces {
		for _, a := range ns.Apps {
			n += len(a.Workloads)
		}
	}
	return n
}

func TestTruncate(t *testing.T) {
	if MaxWorkloads < 25000 {
		t.Fatalf("MaxWorkloads = %d, the WebGL map needs at least 25000", MaxWorkloads)
	}
	m := Build(Input{Pods: barePods(MaxWorkloads+10, nil)})
	if n := count(m); !m.Truncated || n != MaxWorkloads {
		t.Fatalf("truncated=%v n=%d", m.Truncated, n)
	}
	if !m.Compact {
		t.Fatal("a truncated map is above CompactAbove and must be compact")
	}
}

func TestCompact(t *testing.T) {
	svc := []*unstructured.Unstructured{obj(t, `{kind: Service, metadata: {name: web-svc, namespace: demo}, spec: {selector: {app: web}}}`)}
	small := Build(Input{Pods: barePods(CompactAbove, map[string]any{"app": "web"}), Services: svc})
	if small.Compact || count(small) != CompactAbove {
		t.Fatalf("small map: compact=%v n=%d", small.Compact, count(small))
	}
	w := small.Namespaces[0].Apps[0].Workloads[0]
	if w.Status == "" || len(w.Services) != 1 || w.Ready != "1/1" {
		t.Fatalf("small map keeps the optional fields: %+v", w)
	}

	big := Build(Input{Pods: barePods(CompactAbove+1, map[string]any{"app": "web"}), Services: svc})
	if !big.Compact || big.Truncated || count(big) != CompactAbove+1 {
		t.Fatalf("big map: compact=%v truncated=%v n=%d", big.Compact, big.Truncated, count(big))
	}
	b, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if strings.Contains(js, `"status"`) || strings.Contains(js, `"services":[`) || !strings.Contains(js, `"compact":true`) {
		t.Fatalf("compact JSON still has optional fields: %.300s", js)
	}
	w = big.Namespaces[0].Apps[0].Workloads[0]
	if w.Ready != "1/1" || w.Health != "ok" || w.Pods.Total != 1 || w.Kind != "Pod" {
		t.Fatalf("compact keeps the required fields: %+v", w)
	}
	if big.Namespaces[0].Services != 1 {
		t.Fatalf("namespace counts stay: %+v", big.Namespaces[0])
	}
}
