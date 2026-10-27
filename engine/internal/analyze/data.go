package analyze

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

var now = time.Now

// scope loads and memoizes the objects one analysis needs.
type scope struct {
	ctx context.Context
	s   *cluster.Session
	ns  string

	mu     sync.Mutex
	lists  map[string][]*unstructured.Unstructured
	errs   map[string]error // failed lists by "group/resource", kept so they are reported and not retried
	events map[evKey][]event
	loaded bool
	owned  map[string][]*unstructured.Unstructured
	labels map[*unstructured.Unstructured]labels.Set
}

type evKey struct{ kind, ns, name string }

type event struct {
	Reason, Message, Type string
	Count                 int64
	At                    time.Time
}

func newScope(ctx context.Context, s *cluster.Session, ns string) *scope {
	return &scope{ctx: ctx, s: s, ns: ns, lists: map[string][]*unstructured.Unstructured{}, errs: map[string]error{}}
}

// errors returns the failed lists ("group/resource" -> message), nil when none failed.
func (c *scope) errors() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(c.errs))
	for k, err := range c.errs {
		out[k] = err.Error()
	}
	return out
}

// list returns the objects of a resource in the scope; a resource the cluster does not serve is empty.
func (c *scope) list(group, resource string) ([]*unstructured.Unstructured, error) {
	key := group + "/" + resource
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.errs[key]; ok {
		return nil, err
	}
	if v, ok := c.lists[key]; ok {
		return v, nil
	}
	res, ok := c.s.Lookup(group, resource)
	if !ok {
		c.lists[key] = nil
		return nil, nil
	}
	ns := ""
	if res.Namespaced {
		ns = c.ns
	}
	var out []*unstructured.Unstructured
	if c.s.Informers != nil {
		var err error
		out, err = c.s.Informers.List(c.ctx, cluster.Key{GVR: res.GVR(), Namespace: ns})
		if err != nil {
			c.errs[key] = err
			return nil, err
		}
	} else {
		l, err := c.s.Dynamic.Resource(res.GVR()).Namespace(ns).List(c.ctx, metav1.ListOptions{})
		if err != nil {
			c.errs[key] = err
			return nil, err
		}
		for i := range l.Items {
			out = append(out, &l.Items[i])
		}
	}
	c.lists[key] = out
	return out, nil
}

func (c *scope) mustList(group, resource string) []*unstructured.Unstructured {
	l, _ := c.list(group, resource)
	return l
}

// eventsFor returns the warning events (and PVC binding notices) about an object, oldest first.
func (c *scope) eventsFor(kind, ns, name string) []event {
	c.mu.Lock()
	loaded := c.loaded
	c.mu.Unlock()
	if !loaded {
		raw := c.mustList("", "events")
		idx := map[evKey][]event{}
		for _, u := range raw {
			typ := str(u.Object, "type")
			reason := str(u.Object, "reason")
			if typ != "Warning" && reason != "WaitForFirstConsumer" {
				continue
			}
			k := evKey{str(u.Object, "involvedObject", "kind"), str(u.Object, "involvedObject", "namespace"), str(u.Object, "involvedObject", "name")}
			e := event{Reason: reason, Message: str(u.Object, "message"), Type: typ, Count: num(u.Object, "count")}
			for _, f := range []string{"lastTimestamp", "eventTime", "firstTimestamp"} {
				if t, err := time.Parse(time.RFC3339, str(u.Object, f)); err == nil {
					e.At = t
					break
				}
			}
			if e.At.IsZero() {
				e.At = u.GetCreationTimestamp().Time
			}
			idx[k] = append(idx[k], e)
		}
		c.mu.Lock()
		c.events, c.loaded = idx, true
		c.mu.Unlock()
	}
	return c.events[evKey{kind, ns, name}]
}

// lastEvent returns the most recent event with one of the reasons.
func lastEvent(evs []event, reasons ...string) (event, bool) {
	var best event
	found := false
	for _, e := range evs {
		for _, r := range reasons {
			if e.Reason == r && (!found || e.At.After(best.At)) {
				best, found = e, true
			}
		}
	}
	return best, found
}

// podLabels returns the labels of every pod in scope, computed once.
func (c *scope) podLabels() map[*unstructured.Unstructured]labels.Set {
	c.mu.Lock()
	if c.labels != nil {
		defer c.mu.Unlock()
		return c.labels
	}
	c.mu.Unlock()
	pods := c.mustList("", "pods")
	m := make(map[*unstructured.Unstructured]labels.Set, len(pods))
	for _, p := range pods {
		l, _, _ := unstructured.NestedStringMap(p.Object, "metadata", "labels")
		m[p] = l
	}
	c.mu.Lock()
	c.labels = m
	c.mu.Unlock()
	return m
}

// children indexes pods, ReplicaSets, ReplicationControllers and Jobs by owner UID.
func (c *scope) children() map[string][]*unstructured.Unstructured {
	c.mu.Lock()
	if c.owned != nil {
		defer c.mu.Unlock()
		return c.owned
	}
	c.mu.Unlock()
	m := map[string][]*unstructured.Unstructured{}
	for _, k := range [][2]string{{"", "pods"}, {"apps", "replicasets"}, {"", "replicationcontrollers"}, {"batch", "jobs"}} {
		for _, u := range c.mustList(k[0], k[1]) {
			for _, o := range u.GetOwnerReferences() {
				m[string(o.UID)] = append(m[string(o.UID)], u)
			}
		}
	}
	c.mu.Lock()
	c.owned = m
	c.mu.Unlock()
	return m
}

// podsOf returns the pods that descend from the object (through ReplicaSets, controllers and Jobs).
func (c *scope) podsOf(u *unstructured.Unstructured) []*unstructured.Unstructured {
	idx := c.children()
	var pods []*unstructured.Unstructured
	seen := map[string]bool{}
	var walk func(uid string)
	walk = func(uid string) {
		if uid == "" || seen[uid] {
			return
		}
		seen[uid] = true
		for _, ch := range idx[uid] {
			if ch.GetKind() == "Pod" {
				pods = append(pods, ch)
			} else {
				walk(string(ch.GetUID()))
			}
		}
	}
	walk(string(u.GetUID()))
	return pods
}

func convert[T any](u *unstructured.Unstructured) (*T, error) {
	var t T
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &t); err != nil {
		return nil, fmt.Errorf("convert %s/%s: %w", u.GetKind(), u.GetName(), err)
	}
	return &t, nil
}

func str(o map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedString(o, path...)
	return v
}

func num(o map[string]any, path ...string) int64 {
	v, _, _ := unstructured.NestedFieldNoCopy(o, path...)
	switch n := v.(type) {
	case int64:
		return n
	case int32:
		return int64(n)
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func has(o map[string]any, path ...string) bool {
	_, ok, _ := unstructured.NestedFieldNoCopy(o, path...)
	return ok
}

// cond returns the status condition of the given type from status.conditions.
func cond(o map[string]any, typ string) (map[string]any, bool) {
	v, _, _ := unstructured.NestedFieldNoCopy(o, "status", "conditions")
	list, _ := v.([]any)
	for _, it := range list {
		if m, ok := it.(map[string]any); ok && m["type"] == typ {
			return m, true
		}
	}
	return nil, false
}

func condStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
