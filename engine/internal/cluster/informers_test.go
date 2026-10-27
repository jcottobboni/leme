package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var cmGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

func cm(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": ns, "uid": ns + "-" + name}}}
}

type recSub struct {
	mu      sync.Mutex
	upserts map[string]bool
	deletes map[string]bool
}

func newRecSub() *recSub { return &recSub{upserts: map[string]bool{}, deletes: map[string]bool{}} }

func (r *recSub) OnUpsert(u *unstructured.Unstructured) {
	r.mu.Lock()
	r.upserts[u.GetName()] = true
	r.mu.Unlock()
}

func (r *recSub) OnDelete(u *unstructured.Unstructured) {
	r.mu.Lock()
	r.deletes[u.GetName()] = true
	r.mu.Unlock()
}

func (r *recSub) OnError(error) {}

func (r *recSub) saw(m func(*recSub) map[string]bool, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return m(r)[name]
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fakeDyn(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{cmGVR: "ConfigMapList"}, objs...)
}

func TestInformerSubscribeAndEvents(t *testing.T) {
	dyn := fakeDyn(cm("demo", "a"), cm("other", "b"))
	m := NewInformerManager(dyn, time.Minute)
	defer m.StopAll()
	sub := newRecSub()
	key := Key{GVR: cmGVR, Namespace: "demo"}
	objs, err := m.Subscribe(context.Background(), key, sub)
	if err != nil || len(objs) != 1 || objs[0].GetName() != "a" {
		t.Fatalf("snapshot: %v %v", objs, err)
	}
	if _, err := dyn.Resource(cmGVR).Namespace("demo").Create(context.Background(), cm("demo", "c"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "upsert of c", func() bool { return sub.saw(func(r *recSub) map[string]bool { return r.upserts }, "c") })
	if err := dyn.Resource(cmGVR).Namespace("demo").Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "delete of a", func() bool { return sub.saw(func(r *recSub) map[string]bool { return r.deletes }, "a") })
	if u, ok := m.Get(key, "demo", "c"); !ok || u.GetName() != "c" {
		t.Fatalf("Get from cache: %v %v", u, ok)
	}
}

func TestInformerIdleTTL(t *testing.T) {
	m := NewInformerManager(fakeDyn(cm("demo", "a")), 30*time.Millisecond)
	defer m.StopAll()
	key := Key{GVR: cmGVR}
	s1, s2 := newRecSub(), newRecSub()
	if _, err := m.Subscribe(context.Background(), key, s1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Subscribe(context.Background(), key, s2); err != nil {
		t.Fatal(err)
	}
	if m.Active() != 1 {
		t.Fatalf("one informer per key, got %d", m.Active())
	}
	m.Unsubscribe(key, s1)
	time.Sleep(60 * time.Millisecond)
	if m.Active() != 1 {
		t.Fatal("an informer with subscribers must keep running")
	}
	m.Unsubscribe(key, s2)
	// A new subscriber before the TTL keeps the same informer.
	if _, err := m.Subscribe(context.Background(), key, s1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if m.Active() != 1 {
		t.Fatal("resubscribing must cancel the idle stop")
	}
	m.Unsubscribe(key, s1)
	eventually(t, "idle informer to stop", func() bool { return m.Active() == 0 })
}

// A namespace List is answered from a synced cluster-wide cache instead of a second informer.
func TestInformerListReusesClusterCache(t *testing.T) {
	m := NewInformerManager(fakeDyn(cm("demo", "a"), cm("other", "b")), time.Minute)
	defer m.StopAll()
	all, err := m.List(context.Background(), Key{GVR: cmGVR})
	if err != nil || len(all) != 2 {
		t.Fatalf("cluster list: %v %v", all, err)
	}
	demo, err := m.List(context.Background(), Key{GVR: cmGVR, Namespace: "demo"})
	if err != nil || len(demo) != 1 || demo[0].GetName() != "a" {
		t.Fatalf("namespace list: %v %v", demo, err)
	}
	if m.Active() != 1 {
		t.Fatalf("namespace list started another informer: %d", m.Active())
	}
}

func TestInformerStopAll(t *testing.T) {
	m := NewInformerManager(fakeDyn(cm("demo", "a")), time.Minute)
	if _, err := m.Subscribe(context.Background(), Key{GVR: cmGVR}, newRecSub()); err != nil {
		t.Fatal(err)
	}
	m.StopAll()
	if m.Active() != 0 {
		t.Fatal("informers left running")
	}
	if _, err := m.Subscribe(context.Background(), Key{GVR: cmGVR}, newRecSub()); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after StopAll: %v", err)
	}
	if _, err := m.List(context.Background(), Key{GVR: cmGVR}); !errors.Is(err, ErrClosed) {
		t.Fatalf("list after StopAll: %v", err)
	}
}

func TestInformerSubscribeHonorsContext(t *testing.T) {
	dyn := fakeDyn()
	// The list never answers: Subscribe must give up with the context and release the informer.
	block := make(chan struct{})
	defer close(block)
	dyn.PrependReactor("list", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		<-block
		return true, nil, errors.New("unreachable")
	})
	m := NewInformerManager(dyn, 10*time.Millisecond)
	defer m.StopAll()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Subscribe(ctx, Key{GVR: cmGVR}, newRecSub()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	eventually(t, "abandoned informer to stop", func() bool { return m.Active() == 0 })
}
