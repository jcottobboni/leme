package ops

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testPod(ns, name string, phase corev1.PodPhase, cpu, mem string) *corev1.Pod {
	rl := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem)}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: rl, Limits: rl}}}},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func TestAggregateNamespaces(t *testing.T) {
	pods := []*corev1.Pod{
		testPod("b", "p1", corev1.PodRunning, "100m", "64Mi"),
		testPod("a", "p2", corev1.PodRunning, "250m", "128Mi"),
		testPod("a", "p3", corev1.PodPending, "250m", "128Mi"),
		testPod("a", "job", corev1.PodSucceeded, "1", "1Gi"),
	}
	use := map[string]Usage{"a/p2": {CPU: 10, Memory: 1000}, "a/p3": {CPU: 5, Memory: 500}, "b/p1": {CPU: 1, Memory: 1}}
	got := aggregateNamespaces(pods, use)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("order: %+v", got)
	}
	a := got[0]
	if a.Pods["Running"] != 1 || a.Pods["Pending"] != 1 || a.Pods["Succeeded"] != 1 {
		t.Fatalf("phases: %v", a.Pods)
	}
	if a.Requests.CPU != 500 || a.Requests.Memory != 256<<20 || a.Requests.Pods != 2 || a.Limits.CPU != 500 {
		t.Fatalf("finished pods must not count: %+v %+v", a.Requests, a.Limits)
	}
	if a.Usage == nil || a.Usage.CPU != 15 || a.Usage.Memory != 1500 {
		t.Fatalf("usage: %+v", a.Usage)
	}
	if none := aggregateNamespaces(pods, nil); none[0].Usage != nil {
		t.Fatal("usage must be omitted without metrics-server")
	}
	if len(aggregateNamespaces(nil, nil)) != 0 {
		t.Fatal("empty input")
	}
}
