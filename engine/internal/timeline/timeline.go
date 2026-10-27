// Package timeline collects Kubernetes events per session and keeps them after the API server expires them.
package timeline

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kinds"
)

// MaxEvents bounds the retention per collector.
const MaxEvents = 50000

// ClusterScope is the first path element of events about cluster-scoped objects (nodes, PVs…).
const ClusterScope = "(cluster)"

// Event is the timeline view of a Kubernetes event.
type Event struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Reason    string   `json:"reason"`
	Message   string   `json:"message"`
	Count     int32    `json:"count"`
	First     int64    `json:"first"` // unix ms
	Last      int64    `json:"last"`  // unix ms
	Namespace string   `json:"namespace"` // "" for cluster-scoped objects
	Group     string   `json:"group,omitempty"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Source    string   `json:"source,omitempty"`
	Path      []string `json:"path"` // hierarchy, e.g. [demo, Deployment/web, Pod/web-1] or [(cluster), Node/n1]
	Inferred  bool     `json:"inferred,omitempty"`
	uid       string
}

func ms(t metav1.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// clusterScopedKinds are the built-in cluster-scoped kinds, used when discovery cannot tell.
var clusterScopedKinds = map[string]bool{
	"Node": true, "Namespace": true, "PersistentVolume": true, "StorageClass": true, "ClusterRole": true,
	"ClusterRoleBinding": true, "CustomResourceDefinition": true, "APIService": true, "PriorityClass": true,
	"RuntimeClass": true, "IngressClass": true, "CSIDriver": true, "CSINode": true, "VolumeAttachment": true,
	"MutatingWebhookConfiguration": true, "ValidatingWebhookConfiguration": true, "CertificateSigningRequest": true,
}

// FromCore converts a core/v1 Event (old and new style timestamps).
func FromCore(e *corev1.Event) *Event {
	first := ms(e.FirstTimestamp)
	if first == 0 {
		first = e.EventTime.UnixMilli()
		if e.EventTime.IsZero() {
			first = ms(e.CreationTimestamp)
		}
	}
	last := ms(e.LastTimestamp)
	count := e.Count
	if e.Series != nil {
		if t := e.Series.LastObservedTime; !t.IsZero() {
			last = t.UnixMilli()
		}
		if e.Series.Count > count {
			count = e.Series.Count
		}
	}
	if last == 0 || last < first {
		last = first
	}
	if count < 1 {
		count = 1
	}
	gv, _ := schema.ParseGroupVersion(e.InvolvedObject.APIVersion)
	src := e.Source.Component
	if src == "" {
		src = e.ReportingController
	}
	ns := e.InvolvedObject.Namespace
	// Events about cluster-scoped objects are stored in some namespace ("default" for nodes); they
	// belong to the cluster, not to it.
	if ns == "" && !(kinds.Builtin(gv.Group) && clusterScopedKinds[e.InvolvedObject.Kind]) {
		ns = e.Namespace
	}
	return &Event{ID: string(e.UID), Type: e.Type, Reason: e.Reason, Message: e.Message, Count: count, First: first, Last: last,
		Namespace: ns, Group: gv.Group, Kind: e.InvolvedObject.Kind, Name: e.InvolvedObject.Name, Source: src, uid: string(e.InvolvedObject.UID)}
}

var (
	// CronJob jobs are named <cronjob>-<scheduled minute> (all digits).
	podOfCronJob = regexp.MustCompile(`^((.+)-[0-9]{8,})-[a-z0-9]{5}$`)
	jobOfCronJob = regexp.MustCompile(`^(.+)-[0-9]{8,}$`)
	// ReplicaSet pod-template-hashes and pod suffixes use the "safe" alphabet of k8s rand (no vowels,
	// no 0/1/3), which keeps "kube-proxy-x7k2p" or "etcd-<node>-control-plane" from looking like a
	// Deployment pod.
	podOfRS = regexp.MustCompile(`^(.+)-[bcdfghjklmnpqrstvwxz2456789]{6,10}-[bcdfghjklmnpqrstvwxz2456789]{5}$`)
	rsOfDep = regexp.MustCompile(`^(.+)-[bcdfghjklmnpqrstvwxz2456789]{6,10}$`)
	// OpenShift DeploymentConfig pods are <dc>-<revision>-<suffix>; their RC is <dc>-<revision>.
	podOfRC = regexp.MustCompile(`^((.+)-[0-9]{1,6})-[a-z0-9]{5}$`)
	rcOfDC  = regexp.MustCompile(`^(.+)-[0-9]{1,6}$`)
)

// ownerStore is a metadata-only cache of one resource used for owner resolution.
type ownerStore struct {
	store  cache.Store
	synced func() bool
	failed atomic.Bool // RBAC or a missing API: the cache will never fill
}

// owners resolves controller chains from metadata-only caches (ownerReferences only).
type owners struct {
	stores map[string]*ownerStore // "group/Kind" -> store keyed by ns/name
	stop   func()
}

func storeKey(group, kind string) string { return group + "/" + kind }

// ownerResources are the controllers whose owner references are followed.
var ownerResources = map[string]schema.GroupVersionResource{
	storeKey("", "Pod"):                   {Version: "v1", Resource: "pods"},
	storeKey("", "ReplicationController"): {Version: "v1", Resource: "replicationcontrollers"},
	storeKey("apps", "ReplicaSet"):        {Group: "apps", Version: "v1", Resource: "replicasets"},
	storeKey("apps", "Deployment"):        {Group: "apps", Version: "v1", Resource: "deployments"},
	storeKey("apps", "StatefulSet"):       {Group: "apps", Version: "v1", Resource: "statefulsets"},
	storeKey("apps", "DaemonSet"):         {Group: "apps", Version: "v1", Resource: "daemonsets"},
	storeKey("batch", "Job"):              {Group: "batch", Version: "v1", Resource: "jobs"},
}

type ref struct{ group, kind, name string }

// controller looks the object up. known is false when the cache cannot answer yet (not synced, or
// the object is gone); then c is empty. Kinds without a cache (top-level controllers, CRDs) are
// known to have no followed owner.
func (o *owners) controller(group, kind, ns, name string) (c ref, known bool) {
	if o == nil {
		return ref{}, false
	}
	st, ok := o.stores[storeKey(group, kind)]
	if !ok {
		_, followed := ownerResources[storeKey(group, kind)]
		return ref{}, !followed
	}
	if st.synced != nil && !st.synced() {
		return ref{}, false
	}
	obj, exists, _ := st.store.GetByKey(ns + "/" + name)
	if !exists {
		return ref{}, false
	}
	m, ok := obj.(*metav1.PartialObjectMetadata)
	if !ok {
		return ref{}, false
	}
	if oc := metav1.GetControllerOfNoCopy(m); oc != nil {
		return ref{kinds.GroupOf(oc.APIVersion), oc.Kind, oc.Name}, true
	}
	return ref{}, true
}

// guess infers the controller of an object that is already gone from its generated name.
func guess(r ref) ref {
	switch storeKey(r.group, r.kind) {
	case storeKey("", "Pod"):
		if m := podOfCronJob.FindStringSubmatch(r.name); m != nil {
			return ref{"batch", "Job", m[1]}
		} else if m := podOfRS.FindStringSubmatch(r.name); m != nil {
			return ref{"apps", "Deployment", m[1]}
		} else if m := podOfRC.FindStringSubmatch(r.name); m != nil {
			return ref{"", "ReplicationController", m[1]}
		}
	case storeKey("batch", "Job"):
		if m := jobOfCronJob.FindStringSubmatch(r.name); m != nil {
			return ref{"batch", "CronJob", m[1]}
		}
	case storeKey("", "ReplicationController"):
		if m := rcOfDC.FindStringSubmatch(r.name); m != nil {
			return ref{"apps.openshift.io", "DeploymentConfig", m[1]}
		}
	case storeKey("apps", "ReplicaSet"):
		if m := rsOfDep.FindStringSubmatch(r.name); m != nil {
			return ref{"apps", "Deployment", m[1]}
		}
	}
	return ref{}
}

// path builds [namespace, top owner, …, object] from the event's involvedObject and the owner
// references in the caches. Objects already gone fall back to name heuristics (inferred). certain
// reports that every step was answered by a synced cache, so the path can be kept for the object.
func (o *owners) path(e *Event) (chain []string, inferred, certain bool) {
	chain = []string{e.Kind + "/" + e.Name}
	cur := ref{e.Group, e.Kind, e.Name}
	certain = true
	for range 3 {
		next, known := o.controller(cur.group, cur.kind, e.Namespace, cur.name)
		if !known {
			certain = false
			next = guess(cur)
			if next.kind != "" {
				inferred = true
			}
		}
		// Static pods are "owned" by their Node: that is not a workload.
		if next.kind == "" || next.group == "" && next.kind == "Node" {
			break
		}
		// Skip the ReplicaSet/ReplicationController level: users think in Deployment(Config) → Pod.
		if next.group == "apps" && next.kind == "ReplicaSet" || next.group == "" && next.kind == "ReplicationController" {
			up, ok := o.controller(next.group, next.kind, e.Namespace, next.name)
			if !ok {
				certain = false
				up = guess(next)
				if up.kind != "" {
					inferred = true
				}
			}
			if up.kind != "" {
				next = up
			}
		}
		chain = append([]string{next.kind + "/" + next.name}, chain...)
		cur = next
	}
	if e.Namespace != "" {
		chain = append([]string{e.Namespace}, chain...)
	} else {
		chain = append([]string{ClusterScope}, chain...)
	}
	return chain, inferred, certain
}

type subscriber struct {
	mu      sync.Mutex
	pending map[string]struct{}
	wake    chan struct{}
}

// Collector keeps the events of one scope ("" = all namespaces).
type Collector struct {
	sess    *cluster.Session
	ns      string
	key     cluster.Key
	manager *Manager

	mu     sync.Mutex
	events map[string]*Event
	paths  map[string][]string // resolved (not inferred) paths by involvedObject uid: they outlive the object
	subs   map[*subscriber]struct{}
	owners *owners
	errMsg string
	idle   *time.Timer
	// idleGen invalidates an idle timer that fired after a viewer came back.
	idleGen uint64
	// closed is set (under mu) once the collector was released; Manager.Get never returns it again.
	closed bool
}

// IdleTTL is how long a collector keeps retaining events after its last viewer closes the timeline.
var IdleTTL = 10 * time.Minute

func (c *Collector) OnUpsert(u *unstructured.Unstructured) {
	var e corev1.Event
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &e) != nil {
		return
	}
	ev := FromCore(&e)
	if e.InvolvedObject.Namespace == "" && ev.Namespace != "" && c.sess != nil {
		// Discovery knows custom cluster-scoped kinds too.
		if res, ok := c.sess.LookupKind(ev.Group, ev.Kind); ok && !res.Namespaced {
			ev.Namespace = ""
		}
	}
	c.mu.Lock()
	c.events[ev.ID] = ev
	if len(c.events) > MaxEvents {
		c.evict()
	}
	subs := make([]*subscriber, 0, len(c.subs))
	for s := range c.subs {
		subs = append(subs, s)
	}
	c.mu.Unlock()
	for _, s := range subs {
		s.mu.Lock()
		s.pending[ev.ID] = struct{}{}
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// OnDelete keeps the event: retention beyond the API server TTL is the point of the timeline.
func (c *Collector) OnDelete(*unstructured.Unstructured) {}

func (c *Collector) OnError(err error) {
	c.mu.Lock()
	c.errMsg = err.Error()
	c.mu.Unlock()
}

// evict drops the oldest 10% (by last seen). Caller holds c.mu.
func (c *Collector) evict() {
	all := make([]*Event, 0, len(c.events))
	for _, e := range c.events {
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Last < all[j].Last })
	for _, e := range all[:len(all)/10] {
		delete(c.events, e.ID)
	}
	if len(c.paths) > MaxEvents {
		c.paths = map[string][]string{}
	}
}

// view resolves the path of an event. Caller holds c.mu.
func (c *Collector) view(e *Event) Event {
	out := *e
	if p, ok := c.paths[e.uid]; ok && e.uid != "" {
		out.Path = p
		return out
	}
	var certain bool
	out.Path, out.Inferred, certain = c.owners.path(e)
	if certain && e.uid != "" && c.paths != nil {
		c.paths[e.uid] = out.Path
	}
	return out
}

// Snapshot returns every retained event.
func (c *Collector) Snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, c.view(e))
	}
	return out
}

// Subscribe registers for changes; drain returns changed events since the last call.
func (c *Collector) Subscribe() (drain func() []Event, wake <-chan struct{}, cancel func()) {
	s := &subscriber{pending: map[string]struct{}{}, wake: make(chan struct{}, 1)}
	c.mu.Lock()
	c.subs[s] = struct{}{}
	c.disarm()
	c.mu.Unlock()
	drain = func() []Event {
		s.mu.Lock()
		ids := s.pending
		s.pending = map[string]struct{}{}
		s.mu.Unlock()
		c.mu.Lock()
		defer c.mu.Unlock()
		out := make([]Event, 0, len(ids))
		for id := range ids {
			if e, ok := c.events[id]; ok {
				out = append(out, c.view(e))
			}
		}
		return out
	}
	cancel = func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.subs, s)
		if len(c.subs) == 0 && c.idle == nil {
			c.arm()
		}
	}
	return drain, s.wake, cancel
}

// arm starts the idle timer. Caller holds c.mu.
func (c *Collector) arm() {
	c.idleGen++
	gen := c.idleGen
	c.idle = time.AfterFunc(IdleTTL, func() { c.expire(gen) })
}

// disarm cancels a pending idle timer, including one that already fired but has not run yet. Caller holds c.mu.
func (c *Collector) disarm() {
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	c.idleGen++
}

// expire releases an unwatched collector: events informer subscription, owner informers and retention.
func (c *Collector) expire(gen uint64) {
	m := c.manager
	m.mu.Lock()
	c.mu.Lock()
	if c.closed || gen != c.idleGen || len(c.subs) > 0 {
		c.mu.Unlock()
		m.mu.Unlock()
		return
	}
	c.closed, c.idle = true, nil
	c.mu.Unlock()
	if m.m[c.sess][c.ns] == c {
		delete(m.m[c.sess], c.ns)
	}
	m.mu.Unlock()
	c.close()
}

func (c *Collector) close() {
	c.sess.Informers.Unsubscribe(c.key, c)
	if c.owners != nil && c.owners.stop != nil {
		c.owners.stop()
	}
}

// Manager owns collectors; each lives until its session disconnects or it idles out.
type Manager struct {
	mu sync.Mutex
	m  map[*cluster.Session]map[string]*Collector
}

// NewManager creates a manager.
func NewManager() *Manager { return &Manager{m: map[*cluster.Session]map[string]*Collector{}} }

// lookup returns a live registered collector and keeps it alive for the caller: a pending idle
// timer is re-armed so it cannot release the collector between Get and Subscribe. Caller holds m.mu.
func (m *Manager) lookup(sess *cluster.Session, ns string) (*Collector, bool) {
	c, ok := m.m[sess][ns]
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		delete(m.m[sess], ns)
		return nil, false
	}
	if len(c.subs) == 0 {
		c.disarm()
		c.arm()
	}
	return c, true
}

// ownersSyncWait bounds how long the first snapshot waits for the owner caches.
var ownersSyncWait = 5 * time.Second

// Get returns (starting if needed) the collector for a namespace ("" = cluster-wide).
func (m *Manager) Get(ctx context.Context, sess *cluster.Session, ns string) (*Collector, error) {
	m.mu.Lock()
	if c, ok := m.lookup(sess, ns); ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	res, ok := sess.Lookup("", "events")
	if !ok {
		return nil, errNoEvents
	}
	key := cluster.Key{GVR: res.GVR(), Namespace: ns}
	c := &Collector{sess: sess, ns: ns, key: key, manager: m, events: map[string]*Event{}, paths: map[string][]string{}, subs: map[*subscriber]struct{}{}}
	snapCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	objs, err := sess.Informers.Subscribe(snapCtx, key, c)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if existing, ok := m.lookup(sess, ns); ok {
		m.mu.Unlock()
		// Lost a race with another viewer: drop our subscription, use theirs.
		sess.Informers.Unsubscribe(key, c)
		return existing, nil
	}
	if m.m[sess] == nil {
		m.m[sess] = map[string]*Collector{}
		go func() {
			<-sess.Life().Done()
			m.mu.Lock()
			cols := m.m[sess]
			delete(m.m, sess)
			m.mu.Unlock()
			for _, col := range cols {
				col.mu.Lock()
				col.closed = true
				col.disarm()
				col.mu.Unlock()
				col.close()
			}
		}()
	}
	// Owner informers only start once this collector is the registered one.
	own := startOwners(sess, ns)
	c.mu.Lock()
	c.owners = own
	c.mu.Unlock()
	for _, o := range objs {
		c.OnUpsert(o)
	}
	m.m[sess][ns] = c
	m.mu.Unlock()
	// Paths resolved before the owner caches fill would fall back to name guesses.
	waitCtx, wcancel := context.WithTimeout(ctx, ownersSyncWait)
	defer wcancel()
	own.wait(waitCtx)
	return c, nil
}

type timelineError string

func (e timelineError) Error() string { return string(e) }

const errNoEvents = timelineError("events API not available")

// startOwners runs metadata informers (no spec, just ownerReferences) for owner resolution. They stop
// with the collector; failures (RBAC) just leave resolution to the name heuristics.
func startOwners(sess *cluster.Session, ns string) *owners {
	o := &owners{stores: map[string]*ownerStore{}}
	if sess.Meta == nil {
		return o
	}
	f := metadatainformer.NewFilteredSharedInformerFactory(sess.Meta, 0, ns, nil)
	for k, gvr := range ownerResources {
		if _, ok := sess.Lookup(gvr.Group, gvr.Resource); !ok {
			continue
		}
		inf := f.ForResource(gvr).Informer()
		st := &ownerStore{store: inf.GetStore(), synced: inf.HasSynced}
		_ = inf.SetWatchErrorHandler(func(_ *cache.Reflector, err error) {
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsNotFound(err) {
				st.failed.Store(true)
			}
		})
		o.stores[k] = st
	}
	stop := make(chan struct{})
	var once sync.Once
	o.stop = func() { once.Do(func() { close(stop) }) }
	f.Start(stop)
	return o
}

// wait blocks until every owner cache synced or failed, or ctx ends.
func (o *owners) wait(ctx context.Context) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		ready := true
		for _, st := range o.stores {
			if !st.failed.Load() && st.synced != nil && !st.synced() {
				ready = false
				break
			}
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
