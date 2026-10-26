package cluster

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Subscriber receives cache events. Callbacks must not block.
type Subscriber interface {
	OnUpsert(obj *unstructured.Unstructured)
	OnDelete(obj *unstructured.Unstructured)
	OnError(err error)
}

// Key identifies a shared informer.
type Key struct {
	GVR       schema.GroupVersionResource
	Namespace string // "" = all namespaces
}

type entry struct {
	informer cache.SharedIndexInformer
	stop     chan struct{}
	subs     map[Subscriber]struct{}
	idle     *time.Timer

	errMu   sync.Mutex
	lastErr error
}

func (e *entry) err() error {
	e.errMu.Lock()
	defer e.errMu.Unlock()
	return e.lastErr
}

// InformerManager starts informers on demand and stops them after an idle TTL.
type InformerManager struct {
	client  dynamic.Interface
	idleTTL time.Duration

	mu      sync.Mutex
	entries map[Key]*entry
	closed  bool // after StopAll no informer may start (late requests of a disconnected session)
}

// ErrClosed is returned when the session's caches were already stopped.
var ErrClosed = errors.New("cluster session closed")

// NewInformerManager creates a manager.
func NewInformerManager(client dynamic.Interface, idleTTL time.Duration) *InformerManager {
	return &InformerManager{client: client, idleTTL: idleTTL, entries: map[Key]*entry{}}
}

// Subscribe registers sub and returns the current objects once the cache is synced.
func (m *InformerManager) Subscribe(ctx context.Context, key Key, sub Subscriber) ([]*unstructured.Unstructured, error) {
	e := m.acquire(key, sub)
	if e == nil {
		return nil, ErrClosed
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for !e.informer.HasSynced() {
		// Permanent errors (RBAC, missing API) abort; transient ones keep waiting for the reflector to recover.
		if err := e.err(); err != nil && (apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err)) {
			m.Unsubscribe(key, sub)
			return nil, err
		}
		select {
		case <-ctx.Done():
			m.Unsubscribe(key, sub)
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
	e.errMu.Lock()
	e.lastErr = nil
	e.errMu.Unlock()
	items := e.informer.GetStore().List()
	out := make([]*unstructured.Unstructured, 0, len(items))
	for _, it := range items {
		if u, ok := it.(*unstructured.Unstructured); ok {
			out = append(out, u)
		}
	}
	return out, nil
}

// Get returns a cached object if an informer for the key is running and synced.
func (m *InformerManager) Get(key Key, namespace, name string) (*unstructured.Unstructured, bool) {
	m.mu.Lock()
	e, ok := m.entries[key]
	m.mu.Unlock()
	if !ok || !e.informer.HasSynced() {
		return nil, false
	}
	k := name
	if namespace != "" {
		k = namespace + "/" + name
	}
	obj, exists, err := e.informer.GetStore().GetByKey(k)
	if err != nil || !exists {
		return nil, false
	}
	u, ok := obj.(*unstructured.Unstructured)
	return u, ok
}

func (m *InformerManager) acquire(key Key, sub Subscriber) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	e, ok := m.entries[key]
	if !ok {
		e = m.start(key)
		m.entries[key] = e
	}
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	e.subs[sub] = struct{}{}
	if err := e.err(); err != nil {
		go sub.OnError(err)
	}
	return e
}

func (m *InformerManager) start(key Key) *entry {
	inf := dynamicinformer.NewFilteredDynamicInformer(m.client, key.GVR, key.Namespace, 0, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil).Informer()
	e := &entry{informer: inf, stop: make(chan struct{}), subs: map[Subscriber]struct{}{}}
	_ = inf.SetTransform(stripManagedFields)
	_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
		e.errMu.Lock()
		e.lastErr = err
		e.errMu.Unlock()
		for _, s := range m.subscribers(key) {
			s.OnError(err)
		}
	})
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			e.errMu.Lock()
			e.lastErr = nil
			e.errMu.Unlock()
			if u, ok := obj.(*unstructured.Unstructured); ok {
				for _, s := range m.subscribers(key) {
					s.OnUpsert(u)
				}
			}
		},
		UpdateFunc: func(_, obj any) {
			e.errMu.Lock()
			e.lastErr = nil
			e.errMu.Unlock()
			if u, ok := obj.(*unstructured.Unstructured); ok {
				for _, s := range m.subscribers(key) {
					s.OnUpsert(u)
				}
			}
		},
		DeleteFunc: func(obj any) {
			if t, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = t.Obj
			}
			if u, ok := obj.(*unstructured.Unstructured); ok {
				for _, s := range m.subscribers(key) {
					s.OnDelete(u)
				}
			}
		},
	})
	go inf.Run(e.stop)
	return e
}

func (m *InformerManager) subscribers(key Key) []Subscriber {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return nil
	}
	out := make([]Subscriber, 0, len(e.subs))
	for s := range e.subs {
		out = append(out, s)
	}
	return out
}

// Unsubscribe removes sub; the informer stops after the idle TTL with no subscribers.
func (m *InformerManager) Unsubscribe(key Key, sub Subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return
	}
	delete(e.subs, sub)
	if len(e.subs) > 0 || e.idle != nil {
		return
	}
	e.idle = time.AfterFunc(m.idleTTL, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if cur, ok := m.entries[key]; ok && cur == e && len(e.subs) == 0 {
			close(e.stop)
			delete(m.entries, key)
		}
	})
}

// Active returns the number of running informers.
func (m *InformerManager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// StopAll stops every informer.
func (m *InformerManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for k, e := range m.entries {
		if e.idle != nil {
			e.idle.Stop()
		}
		close(e.stop)
		delete(m.entries, k)
	}
}

func stripManagedFields(obj any) (any, error) {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		u.SetManagedFields(nil)
		ann := u.GetAnnotations()
		if _, ok := ann["kubectl.kubernetes.io/last-applied-configuration"]; ok {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			u.SetAnnotations(ann)
		}
	}
	return obj, nil
}

// nopSub holds an informer open for the duration of a one-off List. Non-zero size keeps pointers distinct.
type nopSub struct{ _ byte }

func (*nopSub) OnUpsert(*unstructured.Unstructured) {}
func (*nopSub) OnDelete(*unstructured.Unstructured) {}
func (*nopSub) OnError(error)                       {}

// List returns the cached objects for key. It starts an informer if needed, which then idles out after the TTL,
// so repeated calls (graph refreshes) are served from memory.
func (m *InformerManager) List(ctx context.Context, key Key) ([]*unstructured.Unstructured, error) {
	// A synced cluster-wide cache already holds the namespace: answer from it instead of starting a
	// second informer (and a second copy of every object in memory).
	if key.Namespace != "" {
		m.mu.Lock()
		all, ok := m.entries[Key{GVR: key.GVR}]
		m.mu.Unlock()
		if ok && all.informer.HasSynced() {
			items, err := all.informer.GetIndexer().ByIndex(cache.NamespaceIndex, key.Namespace)
			if err == nil {
				out := make([]*unstructured.Unstructured, 0, len(items))
				for _, it := range items {
					if u, ok := it.(*unstructured.Unstructured); ok {
						out = append(out, u)
					}
				}
				return out, nil
			}
		}
	}
	sub := &nopSub{}
	objs, err := m.Subscribe(ctx, key, sub)
	if err != nil {
		return nil, err
	}
	m.Unsubscribe(key, sub)
	return objs, nil
}
