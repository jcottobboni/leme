package ops

import (
	"context"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/project"
)

// Resources sums CPU (millicores) and memory (bytes).
type Resources struct {
	CPU    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
	Pods   int64 `json:"pods,omitempty"`
}

// NodeSummary is one node row of the overview.
type NodeSummary struct {
	Name        string    `json:"name"`
	Health      string    `json:"health"`
	Status      string    `json:"status"`
	Roles       string    `json:"roles"`
	Allocatable Resources `json:"allocatable"`
	Requests    Resources `json:"requests"`
	Limits      Resources `json:"limits"`
	Usage       *Usage    `json:"usage,omitempty"`
	Pods        int       `json:"pods"`
}

// Warning is a recent Warning event.
type Warning struct {
	Namespace string    `json:"namespace"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Count     int32     `json:"count"`
	Last      time.Time `json:"last"`
}

// NamespaceStat is one namespace row of the overview: pods by phase, requests/limits of live pods, usage when known.
type NamespaceStat struct {
	Name     string         `json:"name"`
	Pods     map[string]int `json:"pods"`
	Requests Resources      `json:"requests"`
	Limits   Resources      `json:"limits"`
	Usage    *Usage         `json:"usage,omitempty"`
}

// Overview summarizes a cluster (or one namespace when cluster-wide lists are forbidden).
type Overview struct {
	Scope          string                         `json:"scope"` // "cluster" or the namespace
	Nodes          []NodeSummary                  `json:"nodes"`
	Capacity       Resources                      `json:"capacity"`
	Allocatable    Resources                      `json:"allocatable"`
	Requests       Resources                      `json:"requests"`
	Limits         Resources                      `json:"limits"`
	Usage          *Usage                         `json:"usage,omitempty"`
	Pods           map[string]int                 `json:"pods"` // by phase
	Workloads      map[string]project.HealthCount `json:"workloads"`
	Namespaces     int                            `json:"namespaces"`
	Warnings       []Warning                      `json:"warnings"`
	NamespaceStats []NamespaceStat                `json:"namespaceStats"`
	Errors         map[string]string              `json:"errors,omitempty"`
}

func podResources(p *corev1.Pod) (req, lim Resources) {
	for _, c := range p.Spec.Containers {
		req.CPU += c.Resources.Requests.Cpu().MilliValue()
		req.Memory += c.Resources.Requests.Memory().Value()
		lim.CPU += c.Resources.Limits.Cpu().MilliValue()
		lim.Memory += c.Resources.Limits.Memory().Value()
	}
	// Init containers run before the app containers; the effective request is the larger of the two.
	for _, c := range p.Spec.InitContainers {
		if v := c.Resources.Requests.Cpu().MilliValue(); v > req.CPU {
			req.CPU = v
		}
		if v := c.Resources.Requests.Memory().Value(); v > req.Memory {
			req.Memory = v
		}
	}
	return req, lim
}

// aggregateNamespaces groups pods per namespace. usage (keyed "namespace/name", may be nil) is summed per namespace.
// Succeeded/Failed pods count in the phase tally but not in requests/limits, like the cluster totals.
func aggregateNamespaces(pods []*corev1.Pod, usage map[string]Usage) []NamespaceStat {
	by := map[string]*NamespaceStat{}
	for _, p := range pods {
		st := by[p.Namespace]
		if st == nil {
			st = &NamespaceStat{Name: p.Namespace, Pods: map[string]int{}}
			if usage != nil {
				st.Usage = &Usage{}
			}
			by[p.Namespace] = st
		}
		st.Pods[string(p.Status.Phase)]++
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		req, lim := podResources(p)
		st.Requests.CPU += req.CPU
		st.Requests.Memory += req.Memory
		st.Requests.Pods++
		st.Limits.CPU += lim.CPU
		st.Limits.Memory += lim.Memory
		if u, ok := usage[p.Namespace+"/"+p.Name]; ok && st.Usage != nil {
			st.Usage.CPU += u.CPU
			st.Usage.Memory += u.Memory
		}
	}
	out := make([]NamespaceStat, 0, len(by))
	for _, st := range by {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// newOverview starts with empty (not nil) lists: the UI reads their length, and a nil slice would be
// sent as null when a namespace has no warnings or the nodes cannot be listed.
func newOverview() *Overview {
	return &Overview{Scope: "cluster", Nodes: []NodeSummary{}, Pods: map[string]int{}, Workloads: map[string]project.HealthCount{},
		Warnings: []Warning{}, NamespaceStats: []NamespaceStat{}, Errors: map[string]string{}}
}

// GetOverview builds the overview from the informer caches. namespace is only used as a fallback.
func GetOverview(ctx context.Context, s *cluster.Session, fallbackNS string) *Overview {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	o := newOverview()
	list := func(group, resource, ns string) ([]*unstructured.Unstructured, error) {
		r, ok := s.Lookup(group, resource)
		if !ok {
			return nil, nil
		}
		return s.Informers.List(ctx, cluster.Key{GVR: r.GVR(), Namespace: ns})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var nodes, pods []*unstructured.Unstructured
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f()
		}()
	}
	run(func() {
		n, err := list("", "nodes", "")
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			o.Errors["nodes"] = err.Error()
		}
		nodes = n
	})
	run(func() {
		p, err := list("", "pods", "")
		scope := "cluster"
		if err != nil && fallbackNS != "" {
			p, err = list("", "pods", fallbackNS)
			scope = fallbackNS
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			o.Errors["pods"] = err.Error()
		}
		pods, o.Scope = p, scope
	})
	run(func() {
		if ns, err := list("", "namespaces", ""); err == nil {
			mu.Lock()
			o.Namespaces = len(ns)
			mu.Unlock()
		}
	})
	for _, wk := range []struct{ group, resource, kind string }{{"apps", "deployments", "Deployment"}, {"apps", "statefulsets", "StatefulSet"}, {"apps", "daemonsets", "DaemonSet"}, {"batch", "cronjobs", "CronJob"}} {
		run(func() {
			objs, err := list(wk.group, wk.resource, "")
			if err != nil && fallbackNS != "" {
				objs, err = list(wk.group, wk.resource, fallbackNS)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				o.Errors[wk.resource] = err.Error()
				return
			}
			var hc project.HealthCount
			for _, u := range objs {
				hc.Add(project.Project(u).Health)
			}
			o.Workloads[wk.kind] = hc
		})
	}
	var nodeUse, podUse map[string]Usage
	run(func() {
		// Pod usage is keyed per namespace/name; scope fallback is resolved after the pods list returns.
		u, err := PodUsage(ctx, s, "")
		if err != nil && fallbackNS != "" {
			u, err = PodUsage(ctx, s, fallbackNS)
		}
		if err == nil {
			mu.Lock()
			podUse = u
			mu.Unlock()
		}
	})
	run(func() {
		if u, err := NodeUsage(ctx, s); err == nil {
			mu.Lock()
			nodeUse = u
			mu.Unlock()
		}
	})
	run(func() {
		l, err := s.Typed.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 500})
		if err != nil && fallbackNS != "" {
			l, err = s.Typed.CoreV1().Events(fallbackNS).List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 500})
		}
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			o.Errors["events"] = err.Error()
			return
		}
		for _, e := range l.Items {
			last := e.LastTimestamp.Time
			if last.IsZero() {
				last = e.EventTime.Time
			}
			if last.IsZero() {
				last = e.CreationTimestamp.Time
			}
			o.Warnings = append(o.Warnings, Warning{Namespace: e.InvolvedObject.Namespace, Kind: e.InvolvedObject.Kind, Name: e.InvolvedObject.Name,
				Reason: e.Reason, Message: e.Message, Count: max(e.Count, 1), Last: last})
		}
	})
	wg.Wait()

	sort.Slice(o.Warnings, func(i, j int) bool { return o.Warnings[i].Last.After(o.Warnings[j].Last) })
	if len(o.Warnings) > 50 {
		o.Warnings = o.Warnings[:50]
	}

	byNode := map[string]*NodeSummary{}
	for _, u := range nodes {
		var n corev1.Node
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &n) != nil {
			continue
		}
		row := project.Project(u)
		st, _ := row.Cells["status"].(string)
		roles, _ := row.Cells["roles"].(string)
		ns := &NodeSummary{Name: n.Name, Health: row.Health, Status: st, Roles: roles,
			Allocatable: Resources{CPU: n.Status.Allocatable.Cpu().MilliValue(), Memory: n.Status.Allocatable.Memory().Value(), Pods: n.Status.Allocatable.Pods().Value()}}
		if u, ok := nodeUse[n.Name]; ok {
			uu := u
			ns.Usage = &uu
		}
		o.Capacity.CPU += n.Status.Capacity.Cpu().MilliValue()
		o.Capacity.Memory += n.Status.Capacity.Memory().Value()
		o.Capacity.Pods += n.Status.Capacity.Pods().Value()
		o.Allocatable.CPU += ns.Allocatable.CPU
		o.Allocatable.Memory += ns.Allocatable.Memory
		o.Allocatable.Pods += ns.Allocatable.Pods
		byNode[n.Name] = ns
	}
	typed := make([]*corev1.Pod, 0, len(pods))
	for _, u := range pods {
		p := new(corev1.Pod)
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, p) != nil {
			continue
		}
		typed = append(typed, p)
		o.Pods[string(p.Status.Phase)]++
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		req, lim := podResources(p)
		o.Requests.CPU += req.CPU
		o.Requests.Memory += req.Memory
		o.Limits.CPU += lim.CPU
		o.Limits.Memory += lim.Memory
		o.Requests.Pods++
		if n, ok := byNode[p.Spec.NodeName]; ok {
			n.Requests.CPU += req.CPU
			n.Requests.Memory += req.Memory
			n.Limits.CPU += lim.CPU
			n.Limits.Memory += lim.Memory
			n.Pods++
		}
	}
	o.NamespaceStats = aggregateNamespaces(typed, podUse)
	if len(nodeUse) > 0 {
		var total Usage
		for _, u := range nodeUse {
			total.CPU += u.CPU
			total.Memory += u.Memory
		}
		o.Usage = &total
	}
	for _, n := range byNode {
		o.Nodes = append(o.Nodes, *n)
	}
	sort.Slice(o.Nodes, func(i, j int) bool { return o.Nodes[i].Name < o.Nodes[j].Name })
	if len(o.Errors) == 0 {
		o.Errors = nil
	}
	return o
}
