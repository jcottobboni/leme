package timeline

import (
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

func TestFromCoreTimestamps(t *testing.T) {
	t0 := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	old := &corev1.Event{ObjectMeta: metav1.ObjectMeta{UID: "1", Namespace: "demo"}, Type: "Warning", Reason: "BackOff", Count: 7,
		FirstTimestamp: metav1.NewTime(t0), LastTimestamp: metav1.NewTime(t0.Add(time.Minute)),
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-1", Namespace: "demo"}}
	e := FromCore(old)
	if e.First != t0.UnixMilli() || e.Last != t0.Add(time.Minute).UnixMilli() || e.Count != 7 {
		t.Fatalf("old style: %+v", e)
	}
	newer := &corev1.Event{ObjectMeta: metav1.ObjectMeta{UID: "2", Namespace: "demo"}, Type: "Normal", Reason: "Pulled",
		EventTime: metav1.NewMicroTime(t0), Series: &corev1.EventSeries{Count: 3, LastObservedTime: metav1.NewMicroTime(t0.Add(30 * time.Second))},
		ReportingController: "kubelet", InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-1"}}
	e = FromCore(newer)
	if e.First != t0.UnixMilli() || e.Last != t0.Add(30*time.Second).UnixMilli() || e.Count != 3 || e.Source != "kubelet" || e.Namespace != "demo" {
		t.Fatalf("series style: %+v", e)
	}
}

func TestPath(t *testing.T) {
	pods := cache.NewStore(cache.MetaNamespaceKeyFunc)
	rss := cache.NewStore(cache.MetaNamespaceKeyFunc)
	jobs := cache.NewStore(cache.MetaNamespaceKeyFunc)
	ctrl := true
	_ = pods.Add(&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "web-5d8f7c9b4-x2x2x",
		OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-5d8f7c9b4", Controller: &ctrl}}}})
	_ = rss.Add(&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "web-5d8f7c9b4",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "web", Controller: &ctrl}}}})
	_ = pods.Add(&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "tick-123-abcde",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: "tick-123", Controller: &ctrl}}}})
	_ = jobs.Add(&metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "tick-123",
		OwnerReferences: []metav1.OwnerReference{{Kind: "CronJob", Name: "tick", Controller: &ctrl}}}})
	o := &owners{stores: map[string]cache.Store{"Pod": pods, "ReplicaSet": rss, "Job": jobs}}

	cases := []struct {
		e        Event
		want     []string
		inferred bool
	}{
		{Event{Namespace: "demo", Kind: "Pod", Name: "web-5d8f7c9b4-x2x2x"}, []string{"demo", "Deployment/web", "Pod/web-5d8f7c9b4-x2x2x"}, false},
		{Event{Namespace: "demo", Kind: "Pod", Name: "tick-123-abcde"}, []string{"demo", "CronJob/tick", "Job/tick-123", "Pod/tick-123-abcde"}, false},
		{Event{Namespace: "demo", Kind: "Deployment", Name: "web"}, []string{"demo", "Deployment/web"}, false},
		// Pod already deleted: guessed from its generated name.
		{Event{Namespace: "demo", Kind: "Pod", Name: "api-7f9c6d5b8-q1w2e"}, []string{"demo", "Deployment/api", "Pod/api-7f9c6d5b8-q1w2e"}, true},
		{Event{Kind: "Node", Name: "n1"}, []string{"(cluster)", "Node/n1"}, false},
		// Deleted CronJob pod: the numeric suffix is the job, not a ReplicaSet hash.
		{Event{Namespace: "demo", Kind: "Pod", Name: "tick-29882961-abcde"}, []string{"demo", "CronJob/tick", "Job/tick-29882961", "Pod/tick-29882961-abcde"}, true},
		{Event{Namespace: "demo", Kind: "Job", Name: "tick-29882961"}, []string{"demo", "CronJob/tick", "Job/tick-29882961"}, true},
		// OpenShift DeploymentConfig pod already gone.
		{Event{Namespace: "demo", Kind: "Pod", Name: "api-12-x9z8q"}, []string{"demo", "DeploymentConfig/api", "Pod/api-12-x9z8q"}, true},
	}
	for _, c := range cases {
		got, inf := o.path(&c.e)
		if !reflect.DeepEqual(got, c.want) || inf != c.inferred {
			t.Errorf("%s: got %v (inferred %v), want %v (%v)", c.e.Name, got, inf, c.want, c.inferred)
		}
	}
}
