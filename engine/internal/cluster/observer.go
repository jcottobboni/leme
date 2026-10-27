package cluster

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// Observer sees every change delivered by the informers a manager runs (the change history uses it).
// It never starts a watch of its own: only resources some screen already opened are observed.
// Callbacks run on the observer's own handler goroutine per informer and must not block for long.
type Observer interface {
	// ObserveAdd is called for each object; initial is true for the objects of the first list (or of
	// a handler added to a running informer), i.e. objects that existed before, not new ones.
	ObserveAdd(gvr schema.GroupVersionResource, obj *unstructured.Unstructured, initial bool)
	ObserveUpdate(gvr schema.GroupVersionResource, old, obj *unstructured.Unstructured)
	ObserveDelete(gvr schema.GroupVersionResource, obj *unstructured.Unstructured)
}

// SetObserver registers o on the running informers and on every informer started later. Only the
// first observer is kept (one session, one history).
func (m *InformerManager) SetObserver(o Observer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.observer != nil || o == nil {
		return
	}
	m.observer = o
	for k, e := range m.entries {
		m.observe(k, e.informer)
	}
}

// observe adds the observer's handler to inf. Called with m.mu held.
func (m *InformerManager) observe(key Key, inf cache.SharedIndexInformer) {
	o := m.observer
	if o == nil {
		return
	}
	gvr := key.GVR
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, initial bool) {
			if u, ok := obj.(*unstructured.Unstructured); ok {
				o.ObserveAdd(gvr, u, initial)
			}
		},
		UpdateFunc: func(old, obj any) {
			a, ok1 := old.(*unstructured.Unstructured)
			b, ok2 := obj.(*unstructured.Unstructured)
			if ok1 && ok2 {
				o.ObserveUpdate(gvr, a, b)
			}
		},
		DeleteFunc: func(obj any) {
			if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = t.Obj
			}
			if u, ok := obj.(*unstructured.Unstructured); ok {
				o.ObserveDelete(gvr, u)
			}
		},
	})
}
