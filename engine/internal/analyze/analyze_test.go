package analyze

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	kfake "k8s.io/client-go/kubernetes/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

type m = map[string]any

var testResources = []cluster.Resource{
	{Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
	{Group: "", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true},
	{Group: "", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true},
	{Group: "", Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
	{Group: "", Version: "v1", Resource: "nodes", Kind: "Node"},
	{Group: "", Version: "v1", Resource: "replicationcontrollers", Kind: "ReplicationController", Namespaced: true},
	{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
	{Group: "apps", Version: "v1", Resource: "replicasets", Kind: "ReplicaSet", Namespaced: true},
	{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespaced: true},
	{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet", Namespaced: true},
	{Group: "apps.openshift.io", Version: "v1", Resource: "deploymentconfigs", Kind: "DeploymentConfig", Namespaced: true},
	{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
	{Group: "route.openshift.io", Version: "v1", Resource: "routes", Kind: "Route", Namespaced: true},
	{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
	{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true},
	{Group: "batch", Version: "v1", Resource: "cronjobs", Kind: "CronJob", Namespaced: true},
}

func o(apiVersion, kind, ns, name string, extra m) *unstructured.Unstructured {
	meta := m{"name": name, "uid": kind + "-" + name}
	if ns != "" {
		meta["namespace"] = ns
	}
	obj := m{"apiVersion": apiVersion, "kind": kind, "metadata": meta}
	for k, v := range extra {
		if k == "metadata" {
			for mk, mv := range v.(m) {
				meta[mk] = mv
			}
			continue
		}
		obj[k] = v
	}
	return &unstructured.Unstructured{Object: obj}
}

func session(objs ...*unstructured.Unstructured) *cluster.Session {
	lists := map[schema.GroupVersionResource]string{}
	for _, r := range testResources {
		lists[r.GVR()] = r.Kind + "List"
	}
	var rt []runtime.Object
	for _, u := range objs {
		rt = append(rt, u)
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, rt...)
	return cluster.NewStaticSession(kube.Context{Name: "test"}, kfake.NewSimpleClientset(), dyn, testResources, false)
}

func mkEvent(ns, name, kind, podName, typ, reason, msg string) *unstructured.Unstructured {
	return o("v1", "Event", ns, name, m{"type": typ, "reason": reason, "message": msg, "count": int64(3),
		"involvedObject": m{"kind": kind, "namespace": ns, "name": podName}, "lastTimestamp": "2026-01-01T10:00:00Z"})
}

func pod(name string, status m, extra m) *unstructured.Unstructured {
	e := m{"status": status, "spec": m{"containers": []any{m{"name": "app", "image": "nginx:bad"}}}}
	for k, v := range extra {
		e[k] = v
	}
	return o("v1", "Pod", "demo", name, e)
}

func waiting(reason, msg string) m {
	return m{"phase": "Pending", "conditions": []any{m{"type": "Ready", "status": "False"}},
		"containerStatuses": []any{m{"name": "app", "image": "nginx:bad", "ready": false, "restartCount": int64(0), "state": m{"waiting": m{"reason": reason, "message": msg}}}}}
}

func find(fs []Finding, id string) *Finding {
	for i := range fs {
		if fs[i].ID == id {
			return &fs[i]
		}
	}
	return nil
}

func ids(fs []Finding) string {
	var s []string
	for _, f := range fs {
		s = append(s, f.ID)
	}
	return strings.Join(s, ",")
}

func TestRules(t *testing.T) {
	ready := m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "True"}}}
	oom := m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "False"}},
		"containerStatuses": []any{m{"name": "app", "image": "x", "ready": false, "restartCount": int64(5),
			"state":     m{"waiting": m{"reason": "CrashLoopBackOff"}},
			"lastState": m{"terminated": m{"reason": "OOMKilled", "exitCode": int64(137)}}}}}
	crash := m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "False"}},
		"containerStatuses": []any{m{"name": "app", "image": "x", "ready": false, "restartCount": int64(5),
			"state":     m{"waiting": m{"reason": "CrashLoopBackOff"}},
			"lastState": m{"terminated": m{"reason": "Error", "exitCode": int64(1)}}}}}
	pending := m{"phase": "Pending", "conditions": []any{m{"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": "0/3 nodes are available: 3 Insufficient cpu."}}}
	notReady := m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "False"}},
		"containerStatuses": []any{m{"name": "app", "image": "x", "ready": false, "restartCount": int64(0), "state": m{"running": m{}}}}}

	cases := []struct {
		name    string
		objs    []*unstructured.Unstructured
		ref     Ref
		want    string
		sev     string
		fixable bool
		evid    string
	}{
		{"image pull", []*unstructured.Unstructured{pod("p", waiting("ImagePullBackOff", "Back-off pulling image"), nil)}, Ref{"", "Pod", "demo", "p"}, "image-pull", High, true, "ImagePullBackOff"},
		{"err image pull", []*unstructured.Unstructured{pod("p", waiting("ErrImagePull", "manifest unknown"), nil)}, Ref{"", "Pod", "demo", "p"}, "image-pull", High, true, "manifest unknown"},
		{"oom crash loop", []*unstructured.Unstructured{pod("p", oom, nil)}, Ref{"", "Pod", "demo", "p"}, "crash-loop", High, true, "OOMKilled"},
		{"crash loop exit code", []*unstructured.Unstructured{pod("p", crash, nil)}, Ref{"", "Pod", "demo", "p"}, "crash-loop", High, false, "exitCode=1"},
		{"config error", []*unstructured.Unstructured{pod("p", waiting("CreateContainerConfigError", "secret \"s\" not found"), nil)}, Ref{"", "Pod", "demo", "p"}, "container-config", High, false, "not found"},
		{"unschedulable", []*unstructured.Unstructured{pod("p", pending, nil), mkEvent("demo", "e1", "Pod", "p", "Warning", "FailedScheduling", "0/3 nodes: 1 node(s) had untolerated taint")}, Ref{"", "Pod", "demo", "p"}, "unschedulable", High, true, "Insufficient cpu"},
		{"probe", []*unstructured.Unstructured{pod("p", notReady, nil), mkEvent("demo", "e1", "Pod", "p", "Warning", "Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 503")}, Ref{"", "Pod", "demo", "p"}, "probe-failed", Medium, true, "503"},
		{"not ready", []*unstructured.Unstructured{pod("p", notReady, nil)}, Ref{"", "Pod", "demo", "p"}, "not-ready", Medium, false, "ready=false"},
		{"volume mount", []*unstructured.Unstructured{pod("p", m{"phase": "Pending"}, nil), mkEvent("demo", "e1", "Pod", "p", "Warning", "FailedMount", "MountVolume.SetUp failed for volume data")}, Ref{"", "Pod", "demo", "p"}, "volume-mount", Medium, false, "MountVolume"},
		{"evicted", []*unstructured.Unstructured{pod("p", m{"phase": "Failed", "reason": "Evicted", "message": "low on memory"}, nil)}, Ref{"", "Pod", "demo", "p"}, "pod-evicted", Medium, false, "Evicted"},
		{"healthy pod", []*unstructured.Unstructured{pod("p", ready, nil)}, Ref{"", "Pod", "demo", "p"}, "", "", false, ""},

		{"deployment unavailable + owned pod", []*unstructured.Unstructured{
			o("apps/v1", "Deployment", "demo", "web", m{"spec": m{"replicas": int64(2)}, "status": m{"availableReplicas": int64(0)}}),
			o("apps/v1", "ReplicaSet", "demo", "web-1", m{"metadata": m{"ownerReferences": []any{m{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web", "uid": "Deployment-web"}}}}),
			pod("web-1-a", waiting("ImagePullBackOff", "x"), m{"metadata": m{"ownerReferences": []any{m{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "web-1", "uid": "ReplicaSet-web-1"}}}}),
		}, Ref{"apps", "Deployment", "demo", "web"}, "image-pull", High, true, "ImagePullBackOff"},
		{"deployment stuck", []*unstructured.Unstructured{o("apps/v1", "Deployment", "demo", "web", m{"spec": m{"replicas": int64(1)}, "status": m{"availableReplicas": int64(1),
			"conditions": []any{m{"type": "Progressing", "status": "False", "reason": "ProgressDeadlineExceeded", "message": "ReplicaSet has timed out progressing."}}}})},
			Ref{"apps", "Deployment", "demo", "web"}, "rollout-stuck", High, false, "timed out"},
		{"deployment unavailable", []*unstructured.Unstructured{o("apps/v1", "Deployment", "demo", "web", m{"spec": m{"replicas": int64(3)}, "status": m{"availableReplicas": int64(1)}})},
			Ref{"apps", "Deployment", "demo", "web"}, "replicas-unavailable", Medium, false, "desejadas=3"},
		{"deployment healthy", []*unstructured.Unstructured{o("apps/v1", "Deployment", "demo", "web", m{"spec": m{"replicas": int64(3)}, "status": m{"availableReplicas": int64(3), "readyReplicas": int64(3)}})},
			Ref{"apps", "Deployment", "demo", "web"}, "", "", false, ""},
		{"statefulset", []*unstructured.Unstructured{o("apps/v1", "StatefulSet", "demo", "db", m{"spec": m{"replicas": int64(2)}, "status": m{"readyReplicas": int64(1)}})},
			Ref{"apps", "StatefulSet", "demo", "db"}, "replicas-unavailable", Medium, false, "prontas=1"},
		{"daemonset", []*unstructured.Unstructured{o("apps/v1", "DaemonSet", "demo", "ds", m{"status": m{"desiredNumberScheduled": int64(3), "numberReady": int64(2), "numberAvailable": int64(2)}})},
			Ref{"apps", "DaemonSet", "demo", "ds"}, "replicas-unavailable", Medium, false, "desejadas=3"},
		{"deploymentconfig", []*unstructured.Unstructured{o("apps.openshift.io/v1", "DeploymentConfig", "demo", "dc", m{"spec": m{"replicas": int64(1)}, "status": m{"availableReplicas": int64(0)}})},
			Ref{"apps.openshift.io", "DeploymentConfig", "demo", "dc"}, "replicas-unavailable", High, false, "disponíveis=0"},

		{"service no pods", []*unstructured.Unstructured{o("v1", "Service", "demo", "svc", m{"spec": m{"selector": m{"app": "x"}}})},
			Ref{"", "Service", "demo", "svc"}, "service-no-pods", High, true, "app=x"},
		{"service no ready endpoints", []*unstructured.Unstructured{o("v1", "Service", "demo", "svc", m{"spec": m{"selector": m{"app": "x"}}}),
			pod("p", notReady, m{"metadata": m{"labels": m{"app": "x"}}})},
			Ref{"", "Service", "demo", "svc"}, "service-no-endpoints", High, false, "prontos: 0"},
		{"service ok", []*unstructured.Unstructured{o("v1", "Service", "demo", "svc", m{"spec": m{"selector": m{"app": "x"}}}),
			pod("p", ready, m{"metadata": m{"labels": m{"app": "x"}}})},
			Ref{"", "Service", "demo", "svc"}, "", "", false, ""},
		{"ingress missing service", []*unstructured.Unstructured{o("networking.k8s.io/v1", "Ingress", "demo", "ing", m{"spec": m{"rules": []any{m{"http": m{"paths": []any{
			m{"backend": m{"service": m{"name": "ghost", "port": m{"number": int64(80)}}}}}}}}}})},
			Ref{"networking.k8s.io", "Ingress", "demo", "ing"}, "ingress-missing-service", High, true, "ghost"},
		{"ingress missing port", []*unstructured.Unstructured{
			o("v1", "Service", "demo", "svc", m{"spec": m{"ports": []any{m{"name": "http", "port": int64(80), "targetPort": int64(8080)}}}}),
			o("networking.k8s.io/v1", "Ingress", "demo", "ing", m{"spec": m{"rules": []any{m{"http": m{"paths": []any{
				m{"backend": m{"service": m{"name": "svc", "port": m{"number": int64(9999)}}}}}}}}}})},
			Ref{"networking.k8s.io", "Ingress", "demo", "ing"}, "ingress-missing-port", High, true, "9999"},
		{"ingress ok", []*unstructured.Unstructured{
			o("v1", "Service", "demo", "svc", m{"spec": m{"ports": []any{m{"name": "http", "port": int64(80), "targetPort": int64(8080)}}}}),
			o("networking.k8s.io/v1", "Ingress", "demo", "ing", m{"spec": m{"rules": []any{m{"http": m{"paths": []any{
				m{"backend": m{"service": m{"name": "svc", "port": m{"name": "http"}}}}}}}}}})},
			Ref{"networking.k8s.io", "Ingress", "demo", "ing"}, "", "", false, ""},
		{"route missing service", []*unstructured.Unstructured{o("route.openshift.io/v1", "Route", "demo", "r", m{"spec": m{"to": m{"kind": "Service", "name": "ghost"}}})},
			Ref{"route.openshift.io", "Route", "demo", "r"}, "route-missing-service", High, true, "ghost"},
		{"route missing port", []*unstructured.Unstructured{
			o("v1", "Service", "demo", "svc", m{"spec": m{"ports": []any{m{"name": "http", "port": int64(80), "targetPort": int64(8080)}}}}),
			o("route.openshift.io/v1", "Route", "demo", "r", m{"spec": m{"to": m{"kind": "Service", "name": "svc"}, "port": m{"targetPort": "metrics"}}})},
			Ref{"route.openshift.io", "Route", "demo", "r"}, "route-missing-port", High, true, "metrics"},

		{"pvc pending", []*unstructured.Unstructured{o("v1", "PersistentVolumeClaim", "demo", "data", m{"spec": m{"storageClassName": "fast"}, "status": m{"phase": "Pending"}}),
			mkEvent("demo", "e1", "PersistentVolumeClaim", "data", "Warning", "ProvisioningFailed", "storageclass \"fast\" not found")},
			Ref{"", "PersistentVolumeClaim", "demo", "data"}, "pvc-pending", High, false, "not found"},
		{"pvc bound", []*unstructured.Unstructured{o("v1", "PersistentVolumeClaim", "demo", "data", m{"status": m{"phase": "Bound"}})},
			Ref{"", "PersistentVolumeClaim", "demo", "data"}, "", "", false, ""},
		{"node not ready", []*unstructured.Unstructured{o("v1", "Node", "", "n1", m{"status": m{"conditions": []any{m{"type": "Ready", "status": "Unknown", "reason": "NodeStatusUnknown"}}}})},
			Ref{"", "Node", "", "n1"}, "node-not-ready", Critical, false, "NodeStatusUnknown"},
		{"node disk pressure", []*unstructured.Unstructured{o("v1", "Node", "", "n1", m{"status": m{"conditions": []any{m{"type": "Ready", "status": "True"}, m{"type": "DiskPressure", "status": "True", "reason": "KubeletHasDiskPressure"}}}})},
			Ref{"", "Node", "", "n1"}, "node-diskpressure", High, false, "KubeletHasDiskPressure"},
		{"hpa no metrics", []*unstructured.Unstructured{o("autoscaling/v2", "HorizontalPodAutoscaler", "demo", "h", m{"spec": m{"maxReplicas": int64(5)}, "status": m{"currentReplicas": int64(1), "desiredReplicas": int64(1),
			"conditions": []any{m{"type": "ScalingActive", "status": "False", "reason": "FailedGetResourceMetric", "message": "no metrics"}}}})},
			Ref{"autoscaling", "HorizontalPodAutoscaler", "demo", "h"}, "hpa-no-metrics", Medium, false, "no metrics"},
		{"hpa at max", []*unstructured.Unstructured{o("autoscaling/v2", "HorizontalPodAutoscaler", "demo", "h", m{"spec": m{"maxReplicas": int64(5)}, "status": m{"currentReplicas": int64(5), "desiredReplicas": int64(5)}})},
			Ref{"autoscaling", "HorizontalPodAutoscaler", "demo", "h"}, "hpa-at-max", Low, true, "maxReplicas=5"},
		{"job failed", []*unstructured.Unstructured{o("batch/v1", "Job", "demo", "j", m{"status": m{"failed": int64(4), "conditions": []any{m{"type": "Failed", "status": "True", "reason": "BackoffLimitExceeded"}}}})},
			Ref{"batch", "Job", "demo", "j"}, "job-failed", High, false, "BackoffLimitExceeded"},
		{"cronjob suspended", []*unstructured.Unstructured{o("batch/v1", "CronJob", "demo", "c", m{"spec": m{"suspend": true}})},
			Ref{"batch", "CronJob", "demo", "c"}, "cronjob-suspended", Low, true, "suspend"},
		{"cronjob stale", []*unstructured.Unstructured{o("batch/v1", "CronJob", "demo", "c", m{"status": m{"lastSuccessfulTime": "2025-12-01T00:00:00Z"}})},
			Ref{"batch", "CronJob", "demo", "c"}, "cronjob-stale", Medium, false, "lastSuccessfulTime"},
	}
	now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }
	defer func() { now = time.Now }()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs, err := Object(context.Background(), session(tc.objs...), tc.ref)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(fs) != 0 {
					t.Fatalf("expected no findings, got %s", ids(fs))
				}
				return
			}
			f := find(fs, tc.want)
			if f == nil {
				t.Fatalf("finding %s not found in [%s]", tc.want, ids(fs))
			}
			if f.Severity != tc.sev || f.Fixable != tc.fixable {
				t.Errorf("severity/fixable = %s/%v, want %s/%v", f.Severity, f.Fixable, tc.sev, tc.fixable)
			}
			if f.Title == "" || f.Explanation == "" || f.Suggestion == "" || f.Object.Name == "" {
				t.Errorf("incomplete finding: %+v", f)
			}
			if !strings.Contains(strings.Join(f.Evidence, "\n"), tc.evid) {
				t.Errorf("evidence %q lacks %q", f.Evidence, tc.evid)
			}
		})
	}
}

func TestOOMSuggestion(t *testing.T) {
	p := pod("p", m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "False"}},
		"containerStatuses": []any{m{"name": "app", "ready": false, "restartCount": int64(2), "state": m{"waiting": m{"reason": "CrashLoopBackOff"}},
			"lastState": m{"terminated": m{"reason": "OOMKilled", "exitCode": int64(137)}}}}}, nil)
	fs, _ := Object(context.Background(), session(p), Ref{"", "Pod", "demo", "p"})
	if f := find(fs, "crash-loop"); f == nil || !strings.Contains(f.Suggestion, "limits.memory") {
		t.Fatalf("OOM suggestion missing: %+v", fs)
	}
}

func TestNamespaceAndOrder(t *testing.T) {
	objs := []*unstructured.Unstructured{
		pod("bad", waiting("ImagePullBackOff", "x"), nil),
		o("v1", "Node", "", "n1", m{"status": m{"conditions": []any{m{"type": "Ready", "status": "False"}}}}),
		o("batch/v1", "CronJob", "demo", "c", m{"spec": m{"suspend": true}}),
		o("v1", "Pod", "other", "other-bad", m{"status": waiting("ErrImagePull", "x")}),
	}
	s := session(objs...)
	fs, err := Namespace(context.Background(), s, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if find(fs, "node-not-ready") != nil {
		t.Error("nodes only belong to the cluster scope")
	}
	for _, f := range fs {
		if f.Object.Namespace == "other" {
			t.Errorf("leaked finding from another namespace: %+v", f)
		}
	}
	all, _ := Namespace(context.Background(), s, "")
	if len(all) < 4 || all[0].ID != "node-not-ready" {
		t.Fatalf("cluster scope must be ordered by severity, got %s", ids(all))
	}
	for i := 1; i < len(all); i++ {
		if sevRank[all[i-1].Severity] > sevRank[all[i].Severity] {
			t.Fatalf("not sorted: %s", ids(all))
		}
	}
}

func TestObjectNotFound(t *testing.T) {
	if _, err := Object(context.Background(), session(), Ref{"", "Pod", "demo", "nope"}); err == nil {
		t.Fatal("expected not found")
	}
	fs, err := Object(context.Background(), session(), Ref{"", "Widget", "demo", "x"})
	if err != nil || len(fs) != 0 {
		t.Fatalf("unknown kind must be empty: %v %v", fs, err)
	}
}

func TestNamespacePerformance(t *testing.T) {
	var objs []*unstructured.Unstructured
	ready := m{"phase": "Running", "conditions": []any{m{"type": "Ready", "status": "True"}}}
	for i := 0; i < 5000; i++ {
		objs = append(objs, pod(fmt.Sprintf("p-%d", i), ready, m{"metadata": m{"labels": m{"app": "x"}}}))
	}
	for i := 0; i < 20; i++ {
		objs = append(objs, pod(fmt.Sprintf("bad-%d", i), waiting("ImagePullBackOff", "x"), nil))
	}
	objs = append(objs, o("v1", "Service", "demo", "svc", m{"spec": m{"selector": m{"app": "x"}}}))
	s := session(objs...)
	start := time.Now()
	fs, err := Namespace(context.Background(), s, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 20 {
		t.Fatalf("got %d findings", len(fs))
	}
	t.Logf("5020 pods analyzed in %s", time.Since(start))
	if d := time.Since(start); d > time.Second {
		t.Errorf("too slow: %s", d)
	}
}
