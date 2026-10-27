package timeline

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
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

// Node events are stored in "default" but belong to the cluster.
func TestFromCoreClusterScoped(t *testing.T) {
	ev := &corev1.Event{ObjectMeta: metav1.ObjectMeta{UID: "3", Namespace: "default"}, Type: "Normal", Reason: "NodeReady",
		InvolvedObject: corev1.ObjectReference{Kind: "Node", Name: "n1", APIVersion: "v1"}}
	if e := FromCore(ev); e.Namespace != "" {
		t.Fatalf("node event namespace = %q, want cluster scope", e.Namespace)
	}
	// A CRD named Node (Longhorn) is namespaced: keep the event namespace.
	crd := &corev1.Event{ObjectMeta: metav1.ObjectMeta{UID: "4", Namespace: "longhorn-system"},
		InvolvedObject: corev1.ObjectReference{Kind: "Node", Name: "n1", APIVersion: "longhorn.io/v1beta2"}}
	if e := FromCore(crd); e.Namespace != "longhorn-system" {
		t.Fatalf("crd node namespace = %q", e.Namespace)
	}
}

type meta = metav1.PartialObjectMetadata

func owned(ns, name, apiVersion, kind, owner string) *meta {
	ctrl := true
	m := &meta{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	if kind != "" {
		m.OwnerReferences = []metav1.OwnerReference{{APIVersion: apiVersion, Kind: kind, Name: owner, Controller: &ctrl}}
	}
	return m
}

func stores(objs map[string][]*meta) *owners {
	o := &owners{stores: map[string]*ownerStore{}}
	for k, list := range objs {
		st := cache.NewStore(cache.MetaNamespaceKeyFunc)
		for _, m := range list {
			_ = st.Add(m)
		}
		o.stores[k] = &ownerStore{store: st}
	}
	return o
}

func TestPath(t *testing.T) {
	o := stores(map[string][]*meta{
		storeKey("", "Pod"): {
			owned("demo", "web-5d8f7c9b4-x2x2x", "apps/v1", "ReplicaSet", "web-5d8f7c9b4"),
			owned("demo", "tick-123-abcde", "batch/v1", "Job", "tick-123"),
			owned("kube-system", "kindnet-abcde", "apps/v1", "DaemonSet", "kindnet"),
			owned("kube-system", "kube-proxy-x7k2p", "apps/v1", "DaemonSet", "kube-proxy"),
			owned("kube-system", "etcd-leme-dev-control-plane", "v1", "Node", "leme-dev-control-plane"),
			owned("demo", "canary-7c9b4d5f8-q4w2x", "argoproj.io/v1alpha1", "Rollout", "canary"),
		},
		storeKey("apps", "ReplicaSet"): {owned("demo", "web-5d8f7c9b4", "apps/v1", "Deployment", "web")},
		storeKey("batch", "Job"):       {owned("demo", "tick-123", "batch/v1", "CronJob", "tick")},
		storeKey("apps", "DaemonSet"):  {owned("kube-system", "kindnet", "", "", ""), owned("kube-system", "kube-proxy", "", "", "")},
		storeKey("apps", "Deployment"): {owned("demo", "web", "", "", "")},
	})

	cases := []struct {
		e        Event
		want     []string
		inferred bool
		certain  bool
	}{
		{Event{Namespace: "demo", Kind: "Pod", Name: "web-5d8f7c9b4-x2x2x"}, []string{"demo", "Deployment/web", "Pod/web-5d8f7c9b4-x2x2x"}, false, true},
		{Event{Namespace: "demo", Kind: "Pod", Name: "tick-123-abcde"}, []string{"demo", "CronJob/tick", "Job/tick-123", "Pod/tick-123-abcde"}, false, true},
		{Event{Namespace: "demo", Group: "apps", Kind: "Deployment", Name: "web"}, []string{"demo", "Deployment/web"}, false, true},
		// Resolved by the owner references, not by the name.
		{Event{Namespace: "kube-system", Kind: "Pod", Name: "kindnet-abcde"}, []string{"kube-system", "DaemonSet/kindnet", "Pod/kindnet-abcde"}, false, true},
		{Event{Namespace: "kube-system", Kind: "Pod", Name: "kube-proxy-x7k2p"}, []string{"kube-system", "DaemonSet/kube-proxy", "Pod/kube-proxy-x7k2p"}, false, true},
		// Static pods are mirrored by the kubelet with the Node as owner: no workload above them.
		{Event{Namespace: "kube-system", Kind: "Pod", Name: "etcd-leme-dev-control-plane"}, []string{"kube-system", "Pod/etcd-leme-dev-control-plane"}, false, true},
		// A CRD controller is shown as such.
		{Event{Namespace: "demo", Kind: "Pod", Name: "canary-7c9b4d5f8-q4w2x"}, []string{"demo", "Rollout/canary", "Pod/canary-7c9b4d5f8-q4w2x"}, false, true},
		// Pod already deleted: guessed from its generated name.
		{Event{Namespace: "demo", Kind: "Pod", Name: "api-7f9c6d5b8-q4w2x"}, []string{"demo", "Deployment/api", "Pod/api-7f9c6d5b8-q4w2x"}, true, false},
		// Deleted pods whose names carry no strong hint are not guessed (DaemonSet pods look like Job pods).
		{Event{Namespace: "kube-system", Kind: "Pod", Name: "kindnet-zzzzz"}, []string{"kube-system", "Pod/kindnet-zzzzz"}, false, false},
		{Event{Namespace: "kube-system", Kind: "Pod", Name: "kube-proxy-zzzzz"}, []string{"kube-system", "Pod/kube-proxy-zzzzz"}, false, false},
		{Event{Kind: "Node", Name: "n1"}, []string{ClusterScope, "Node/n1"}, false, true},
		// Deleted CronJob pod: the numeric suffix is the job, not a ReplicaSet hash.
		{Event{Namespace: "demo", Kind: "Pod", Name: "tick-29882961-abcde"}, []string{"demo", "CronJob/tick", "Job/tick-29882961", "Pod/tick-29882961-abcde"}, true, false},
		{Event{Namespace: "demo", Group: "batch", Kind: "Job", Name: "tick-29882961"}, []string{"demo", "CronJob/tick", "Job/tick-29882961"}, true, false},
		// OpenShift DeploymentConfig pod already gone.
		{Event{Namespace: "demo", Kind: "Pod", Name: "api-12-x9z8q"}, []string{"demo", "DeploymentConfig/api", "Pod/api-12-x9z8q"}, true, false},
	}
	for _, c := range cases {
		got, inf, certain := o.path(&c.e)
		if !reflect.DeepEqual(got, c.want) || inf != c.inferred || certain != c.certain {
			t.Errorf("%s: got %v (inferred %v, certain %v), want %v (%v, %v)", c.e.Name, got, inf, certain, c.want, c.inferred, c.certain)
		}
	}
}

// A cache that has not synced yet cannot answer: the path is not certain, so it is not kept.
func TestPathUnsyncedCacheIsNotCertain(t *testing.T) {
	o := stores(map[string][]*meta{storeKey("", "Pod"): {owned("kube-system", "kindnet-abcde", "apps/v1", "DaemonSet", "kindnet")}, storeKey("apps", "DaemonSet"): {owned("kube-system", "kindnet", "", "", "")}})
	o.stores[storeKey("", "Pod")].synced = func() bool { return false }
	c := &Collector{events: map[string]*Event{}, paths: map[string][]string{}, owners: o}
	e := &Event{ID: "e", Namespace: "kube-system", Kind: "Pod", Name: "kindnet-abcde", uid: "p1"}
	if v := c.view(e); len(v.Path) != 2 || len(c.paths) != 0 {
		t.Fatalf("unsynced: path %v, kept %v", v.Path, c.paths)
	}
	o.stores[storeKey("", "Pod")].synced = func() bool { return true }
	if v := c.view(e); v.Path[1] != "DaemonSet/kindnet" || len(c.paths) != 1 {
		t.Fatalf("synced: path %v, kept %v", v.Path, c.paths)
	}
	// The pod is gone: the path resolved while it existed is kept.
	o.stores[storeKey("", "Pod")].store = cache.NewStore(cache.MetaNamespaceKeyFunc)
	if v := c.view(e); v.Path[1] != "DaemonSet/kindnet" || v.Inferred {
		t.Fatalf("after delete: %+v", v)
	}
}

func testSession(t *testing.T) *cluster.Session {
	t.Helper()
	res := []cluster.Resource{{Version: "v1", Resource: "events", Kind: "Event", Namespaced: true}}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{{Version: "v1", Resource: "events"}: "EventList"})
	s := cluster.NewStaticSession(kube.Context{Name: "test"}, nil, dyn, res, true)
	s.Informers = cluster.NewInformerManager(dyn, time.Minute)
	t.Cleanup(s.Informers.StopAll)
	return s
}

// The idle timer may fire while a new viewer is between Manager.Get and Subscribe: the collector
// must not be released under it, and a released collector is never handed out again.
func TestExpireRace(t *testing.T) {
	sess := testSession(t)
	m := NewManager()
	ctx := context.Background()
	c, err := m.Get(ctx, sess, "demo")
	if err != nil {
		t.Fatal(err)
	}
	_, _, cancel := c.Subscribe()
	cancel() // last viewer left: the idle timer is armed
	c.mu.Lock()
	stale := c.idleGen
	c.mu.Unlock()

	again, err := m.Get(ctx, sess, "demo") // a viewer comes back before subscribing
	if err != nil || again != c {
		t.Fatalf("Get must reuse the live collector: %v", err)
	}
	c.expire(stale) // the timer armed before Get fires late
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		t.Fatal("a stale idle timer released a collector that was just handed out")
	}

	c.mu.Lock()
	cur := c.idleGen
	c.mu.Unlock()
	c.expire(cur) // nobody subscribed: now it goes
	c.mu.Lock()
	closed = c.closed
	c.mu.Unlock()
	if !closed {
		t.Fatal("idle collector not released")
	}
	fresh, err := m.Get(ctx, sess, "demo")
	if err != nil || fresh == c {
		t.Fatalf("a released collector was handed out again (err %v)", err)
	}
}

func TestConcurrentGetSubscribeExpire(t *testing.T) {
	old := IdleTTL
	IdleTTL = time.Millisecond
	defer func() { IdleTTL = old }()
	sess := testSession(t)
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c, err := m.Get(context.Background(), sess, "")
				if err != nil {
					t.Error(err)
					return
				}
				_, _, cancel := c.Subscribe()
				c.Snapshot()
				cancel()
			}
		}()
	}
	wg.Wait()
}
