// Package timeline collects Kubernetes events per session and keeps them after the API server expires them.
package timeline

import (
	"context"
	"regexp"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/tools/cache"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// MaxEvents bounds the retention per collector.
const MaxEvents = 50000

// Event is the timeline view of a Kubernetes event.
type Event struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Reason    string   `json:"reason"`
	Message   string   `json:"message"`
	Count     int32    `json:"count"`
	First     int64    `json:"first"` // unix ms
	Last      int64    `json:"last"`  // unix ms
	Namespace string   `json:"namespace"`
	Group     string   `json:"group,omitempty"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Source    string   `json:"source,omitempty"`
	Path      []string `json:"path"` // hierarchy, e.g. [demo, Deployment/web, Pod/web-1]
	Inferred  bool     `json:"inferred,omitempty"`
	uid       string
}

func ms(t metav1.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
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
	if ns == "" {
		ns = e.Namespace
	}
	return &Event{ID: string(e.UID), Type: e.Type, Reason: e.Reason, Message: e.Message, Count: count, First: first, Last: last,
		Namespace: ns, Group: gv.Group, Kind: e.InvolvedObject.Kind, Name: e.InvolvedObject.Name, Source: src, uid: string(e.InvolvedObject.UID)}
}

var (
	// CronJob jobs are named <cronjob>-<scheduled minute> (all digits); ReplicaSet hashes contain letters.
	podOfCronJob = regexp.MustCompile(`^((.+)-[0-9]{8,})-[a-z0-9]{5}$`)
	jobOfCronJob = regexp.MustCompile(`^(.+)-[0-9]{8,}$`)
	podOfRS      = regexp.MustCompile(`^(.+)-[a-z0-9]*[a-z][a-z0-9]*-[a-z0-9]{5}$`)
	rsOfDep      = regexp.MustCompile(`^(.+)-[a-z0-9]*[a-z][a-z0-9]{5,9}$`)
	podOfJob     = regexp.MustCompile(`^(.+)-[a-z0-9]{5}$`)
	// OpenShift DeploymentConfig pods are <dc>-<revision>-<suffix>; their RC is <dc>-<revision>.
	podOfRC = regexp.MustCompile(`^((.+)-[0-9]{1,6})-[a-z0-9]{5}$`)
	rcOfDC  = regexp.MustCompile(`^(.+)-[0-9]{1,6}$`)
)

// owners resolves controller chains from metadata-only caches of pods, replicasets and jobs.
type owners struct {
	stores map[string]cache.Store // kind -> store keyed by ns/name
	stop   func()
}

func (o *owners) controller(kind, ns, name string) (string, string, bool) {
	st, ok := o.stores[kind]
	if !ok {
		return "", "", false
	}
	obj, exists, _ := st.GetByKey(ns + "/" + name)
	if !exists {
		return "", "", false
	}
	m, ok := obj.(*metav1.PartialObjectMetadata)
	if !ok {
		return "", "", false
	}
	if c := metav1.GetControllerOfNoCopy(m); c != nil {
		return c.Kind, c.Name, true
	}
	return "", "", true
}

// path builds [namespace, top owner, …, object]. Deleted pods fall back to name heuristics (Inferred).
func (o *owners) path(e *Event) ([]string, bool) {
	chain := []string{e.Kind + "/" + e.Name}
	kind, name := e.Kind, e.Name
	inferred := false
	for range 3 {
		ok, found := "", false
		var okName string
		if o != nil {
			ok, okName, found = o.controller(kind, e.Namespace, name)
		}
		if !found {
			// Object already gone: guess the controller from generated names.
			switch kind {
			case "Pod":
				if m := podOfCronJob.FindStringSubmatch(name); m != nil {
					ok, okName = "Job", m[1]
				} else if m := podOfRS.FindStringSubmatch(name); m != nil {
					ok, okName = "Deployment", m[1]
				} else if m := podOfRC.FindStringSubmatch(name); m != nil {
					ok, okName = "ReplicationController", m[1]
				} else if m := podOfJob.FindStringSubmatch(name); m != nil {
					ok, okName = "Job", m[1]
				}
			case "Job":
				if m := jobOfCronJob.FindStringSubmatch(name); m != nil {
					ok, okName = "CronJob", m[1]
				}
			case "ReplicationController":
				if m := rcOfDC.FindStringSubmatch(name); m != nil {
					ok, okName = "DeploymentConfig", m[1]
				}
			case "ReplicaSet":
				if m := rsOfDep.FindStringSubmatch(name); m != nil {
					ok, okName = "Deployment", m[1]
				}
			}
			if ok != "" {
				inferred = true
			}
		}
		if ok == "" {
			break
		}
		// Skip the ReplicaSet/ReplicationController level: users think in Deployment(Config) → Pod.
		if ok == "ReplicationController" {
			if dk, dn, f := o.controllerOrGuessRC(e.Namespace, okName); f {
				ok, okName = dk, dn
			}
		}
		if ok == "ReplicaSet" {
			if dk, dn, f := o.controllerOrGuess(e.Namespace, okName); f {
				ok, okName = dk, dn
			}
		}
		chain = append([]string{ok + "/" + okName}, chain...)
		kind, name = ok, okName
	}
	if e.Namespace != "" {
		chain = append([]string{e.Namespace}, chain...)
	} else {
		chain = append([]string{"(cluster)"}, chain...)
	}
	return chain, inferred
}

func (o *owners) controllerOrGuessRC(ns, rc string) (string, string, bool) {
	if o != nil {
		if k, n, found := o.controller("ReplicationController", ns, rc); found {
			return k, n, k != ""
		}
	}
	if m := rcOfDC.FindStringSubmatch(rc); m != nil {
		return "DeploymentConfig", m[1], true
	}
	return "", "", false
}

func (o *owners) controllerOrGuess(ns, rs string) (string, string, bool) {
	if o != nil {
		if k, n, found := o.controller("ReplicaSet", ns, rs); found && k != "" {
			return k, n, true
		}
	}
	if m := rsOfDep.FindStringSubmatch(rs); m != nil {
		return "Deployment", m[1], true
	}
	return "", "", false
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
	subs   map[*subscriber]struct{}
	owners *owners
	errMsg string
	idle   *time.Timer
}

// IdleTTL is how long a collector keeps retaining events after its last viewer closes the timeline.
var IdleTTL = 10 * time.Minute

func (c *Collector) OnUpsert(u *unstructured.Unstructured) {
	var e corev1.Event
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &e) != nil {
		return
	}
	ev := FromCore(&e)
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
}

func (c *Collector) view(e *Event) Event {
	out := *e
	out.Path, out.Inferred = c.owners.path(e)
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
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
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
			c.idle = time.AfterFunc(IdleTTL, c.expire)
		}
	}
	return drain, s.wake, cancel
}

// expire releases an unwatched collector: events informer subscription, owner informers and retention.
func (c *Collector) expire() {
	c.mu.Lock()
	if len(c.subs) > 0 {
		c.idle = nil
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.manager.remove(c)
	c.close()
}

func (c *Collector) close() {
	c.sess.Informers.Unsubscribe(c.key, c)
	if c.owners != nil && c.owners.stop != nil {
		c.owners.stop()
	}
}

// Manager owns collectors; each lives until its session disconnects.
type Manager struct {
	mu sync.Mutex
	m  map[*cluster.Session]map[string]*Collector
}

// NewManager creates a manager.
func NewManager() *Manager { return &Manager{m: map[*cluster.Session]map[string]*Collector{}} }

// Get returns (starting if needed) the collector for a namespace ("" = cluster-wide).
func (m *Manager) Get(ctx context.Context, sess *cluster.Session, ns string) (*Collector, error) {
	m.mu.Lock()
	if c, ok := m.m[sess][ns]; ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	res, ok := sess.Lookup("", "events")
	if !ok {
		return nil, errNoEvents
	}
	key := cluster.Key{GVR: res.GVR(), Namespace: ns}
	c := &Collector{sess: sess, ns: ns, key: key, manager: m, events: map[string]*Event{}, subs: map[*subscriber]struct{}{}}
	snapCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	objs, err := sess.Informers.Subscribe(snapCtx, key, c)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.m[sess][ns]; ok {
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
	return c, nil
}

func (m *Manager) remove(c *Collector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m[c.sess][c.ns] == c {
		delete(m.m[c.sess], c.ns)
	}
}

type timelineError string

func (e timelineError) Error() string { return string(e) }

const errNoEvents = timelineError("events API not available")

// startOwners runs metadata informers (no spec, just ownerReferences) for owner resolution. They stop
// with the session; failures (RBAC) just leave resolution to the name heuristics.
func startOwners(sess *cluster.Session, ns string) *owners {
	o := &owners{stores: map[string]cache.Store{}}
	f := metadatainformer.NewFilteredSharedInformerFactory(sess.Meta, 0, ns, nil)
	for kind, gvr := range map[string]schema.GroupVersionResource{
		"Pod":                   {Version: "v1", Resource: "pods"},
		"ReplicaSet":            {Group: "apps", Version: "v1", Resource: "replicasets"},
		"Job":                   {Group: "batch", Version: "v1", Resource: "jobs"},
		"ReplicationController": {Version: "v1", Resource: "replicationcontrollers"},
	} {
		inf := f.ForResource(gvr).Informer()
		_ = inf.SetWatchErrorHandler(func(*cache.Reflector, error) {})
		o.stores[kind] = inf.GetStore()
	}
	stop := make(chan struct{})
	var once sync.Once
	o.stop = func() { once.Do(func() { close(stop) }) }
	f.Start(stop)
	return o
}
