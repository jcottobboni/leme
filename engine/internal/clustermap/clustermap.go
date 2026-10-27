// Package clustermap builds the "Mapa do cluster" model: namespaces holding apps holding workloads,
// each with its pod health, services and PVCs, from the objects already in the session caches.
package clustermap

import (
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/project"
)

// MaxWorkloads caps the response size; the rest is dropped and Truncated is set.
const MaxWorkloads = 25000

// CompactAbove is the workload count from which the response is compacted: the optional per-workload
// fields only shown in the tooltip (status text and service names) are omitted and Compact is set.
// It matches the default WebGL threshold of the UI.
const CompactAbove = 1500

// HealthCount counts pods per health (neutral ones only add to Total).
type HealthCount struct {
	Total int `json:"total"`
	OK    int `json:"ok"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
}

func (h *HealthCount) add(health string) {
	h.Total++
	switch health {
	case "ok":
		h.OK++
	case "warn":
		h.Warn++
	case "error":
		h.Error++
	}
}

// Workload is one tile of the map.
type Workload struct {
	Group    string      `json:"group"`
	Kind     string      `json:"kind"`
	Name     string      `json:"name"`
	Health   string      `json:"health"`
	Status   string      `json:"status,omitempty"`
	Ready    string      `json:"ready,omitempty"`
	Pods     HealthCount `json:"pods"`
	Services []string    `json:"services,omitempty"`
	PVCs     int         `json:"pvcs,omitempty"`
	Restarts int         `json:"restarts,omitempty"` // container restarts summed over the pods
	Hosts    []string    `json:"hosts,omitempty"`    // Ingress hosts routing to one of its services
}

// App groups workloads of a namespace.
type App struct {
	Name      string     `json:"name"`
	Workloads []Workload `json:"workloads"`
}

// Namespace is one island.
type Namespace struct {
	Name      string      `json:"name"`
	Status    string      `json:"status,omitempty"`
	Pods      HealthCount `json:"pods"`
	Apps      []App       `json:"apps"`
	Services  int         `json:"services"`
	Ingresses int         `json:"ingresses"`
	PVCs      int         `json:"pvcs"`
}

// ClusterMap is the endpoint payload.
type ClusterMap struct {
	Scope      string            `json:"scope"` // "cluster" or the namespace
	Namespaces []Namespace       `json:"namespaces"`
	Truncated  bool              `json:"truncated,omitempty"`
	Compact    bool              `json:"compact,omitempty"` // optional workload fields omitted (see CompactAbove)
	Errors     map[string]string `json:"errors,omitempty"`
}

// Input is everything Build reads. Any list may be empty.
type Input struct {
	Scope                                                              string
	Namespaces, Pods, ReplicaSets, ReplicationControllers              []*unstructured.Unstructured
	Deployments, StatefulSets, DaemonSets, Jobs, CronJobs, DeployConfs []*unstructured.Unstructured
	Services, Ingresses, PVCs                                          []*unstructured.Unstructured
	Errors                                                             map[string]string
}

type wkey struct{ ns, kind, name string }

type wl struct {
	w        Workload
	obj      *unstructured.Unstructured
	labels   map[string]string // pod template labels (or first pod's)
	claims   map[string]bool
	appLabel map[string]string
}

func ownerOf(u *unstructured.Unstructured) (kind, name, apiVersion string) {
	for _, o := range u.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller {
			return o.Kind, o.Name, o.APIVersion
		}
	}
	return "", "", ""
}

func groupOf(apiVersion string) string {
	gv, _ := schema.ParseGroupVersion(apiVersion)
	return gv.Group
}

func templateLabels(u *unstructured.Unstructured) map[string]string {
	if u.GetKind() == "CronJob" {
		m, _, _ := unstructured.NestedStringMap(u.Object, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
		return m
	}
	m, _, _ := unstructured.NestedStringMap(u.Object, "spec", "template", "metadata", "labels")
	return m
}

func volumeClaims(vols []any, into map[string]bool) {
	for _, v := range vols {
		vm, _ := v.(map[string]any)
		if pvc, ok := vm["persistentVolumeClaim"].(map[string]any); ok {
			if n, _ := pvc["claimName"].(string); n != "" {
				into[n] = true
			}
		}
	}
}

func selects(sel, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func str(row project.Row, key string) string {
	s, _ := row.Cells[key].(string)
	return s
}

// Build assembles the map. Pods resolve to their top controller (ReplicaSet -> Deployment, Job -> CronJob,
// ReplicationController -> DeploymentConfig); pods without a known controller become Pod workloads.
func Build(in Input) *ClusterMap {
	out := &ClusterMap{Scope: in.Scope, Errors: in.Errors}
	if out.Scope == "" {
		out.Scope = "cluster"
	}
	works := map[wkey]*wl{}
	order := []wkey{}
	add := func(group string, u *unstructured.Unstructured, kind string) *wl {
		k := wkey{u.GetNamespace(), kind, u.GetName()}
		if w, ok := works[k]; ok {
			return w
		}
		row := project.Project(u)
		w := &wl{obj: u, claims: map[string]bool{}, labels: templateLabels(u), appLabel: u.GetLabels(),
			w: Workload{Group: group, Kind: kind, Name: u.GetName(), Health: row.Health, Status: str(row, "status"), Ready: str(row, "ready")}}
		if w.w.Ready == "" {
			w.w.Ready = str(row, "completions")
		}
		if t, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "volumes"); t != nil {
			volumeClaims(t, w.claims)
		}
		works[k] = w
		order = append(order, k)
		return w
	}
	for _, g := range []struct {
		group, kind string
		objs        []*unstructured.Unstructured
	}{{"apps", "Deployment", in.Deployments}, {"apps", "StatefulSet", in.StatefulSets}, {"apps", "DaemonSet", in.DaemonSets},
		{"batch", "CronJob", in.CronJobs}, {"apps.openshift.io", "DeploymentConfig", in.DeployConfs}} {
		for _, u := range g.objs {
			add(g.group, u, g.kind)
		}
	}
	// Owner lookups for the intermediate controllers.
	rsOwner := map[wkey]wkey{}
	for _, u := range in.ReplicaSets {
		if k, n, _ := ownerOf(u); k == "Deployment" {
			rsOwner[wkey{u.GetNamespace(), "ReplicaSet", u.GetName()}] = wkey{u.GetNamespace(), k, n}
		} else {
			add("apps", u, "ReplicaSet") // standalone ReplicaSet
		}
	}
	for _, u := range in.ReplicationControllers {
		if k, n, _ := ownerOf(u); k == "DeploymentConfig" {
			rsOwner[wkey{u.GetNamespace(), "ReplicationController", u.GetName()}] = wkey{u.GetNamespace(), k, n}
		} else {
			add("", u, "ReplicationController")
		}
	}
	for _, u := range in.Jobs {
		if k, n, _ := ownerOf(u); k == "CronJob" {
			rsOwner[wkey{u.GetNamespace(), "Job", u.GetName()}] = wkey{u.GetNamespace(), k, n}
		} else {
			add("batch", u, "Job")
		}
	}

	nsPods := map[string]*HealthCount{}
	for _, p := range in.Pods {
		ns := p.GetNamespace()
		row := project.Project(p)
		hc := nsPods[ns]
		if hc == nil {
			hc = &HealthCount{}
			nsPods[ns] = hc
		}
		hc.add(row.Health)

		var w *wl
		if kind, name, av := ownerOf(p); kind != "" && kind != "Node" {
			k := wkey{ns, kind, name}
			if top, ok := rsOwner[k]; ok {
				k = top
			}
			w = works[k]
			if w == nil { // controller unknown (CRD operator, cache gap): synthesize it from the owner reference
				w = &wl{claims: map[string]bool{}, appLabel: p.GetLabels(), w: Workload{Group: groupOf(av), Kind: k.kind, Name: k.name, Health: "neutral"}}
				works[k] = w
				order = append(order, k)
			}
		} else {
			w = add("", p, "Pod")
			w.w.Pods = HealthCount{} // the pod itself is its only pod, counted below
		}
		w.w.Pods.add(row.Health)
		if w.labels == nil {
			w.labels = p.GetLabels()
		}
		if w.w.Kind == "Pod" {
			w.w.Ready = str(row, "ready")
		}
		w.w.Restarts += restartsOf(p)
		if v, _, _ := unstructured.NestedSlice(p.Object, "spec", "volumes"); v != nil {
			volumeClaims(v, w.claims)
		}
	}

	// Services selecting each workload.
	svcByNS := map[string][]*unstructured.Unstructured{}
	for _, s := range in.Services {
		svcByNS[s.GetNamespace()] = append(svcByNS[s.GetNamespace()], s)
	}
	type nsAgg struct {
		apps map[string][]Workload
		n    Namespace
	}
	agg := map[string]*nsAgg{}
	get := func(name string) *nsAgg {
		a := agg[name]
		if a == nil {
			a = &nsAgg{apps: map[string][]Workload{}, n: Namespace{Name: name}}
			agg[name] = a
		}
		return a
	}
	for _, u := range in.Namespaces {
		a := get(u.GetName())
		a.n.Status, _, _ = unstructured.NestedString(u.Object, "status", "phase")
	}
	hostsBySvc := ingressHosts(in.Ingresses)
	for _, k := range order {
		w := works[k]
		hosts := map[string]bool{}
		for _, s := range svcByNS[k.ns] {
			sel, _, _ := unstructured.NestedStringMap(s.Object, "spec", "selector")
			if selects(sel, w.labels) {
				w.w.Services = append(w.w.Services, s.GetName())
				for _, h := range hostsBySvc[k.ns+"/"+s.GetName()] {
					hosts[h] = true
				}
			}
		}
		sort.Strings(w.w.Services)
		for h := range hosts {
			w.w.Hosts = append(w.w.Hosts, h)
		}
		sort.Strings(w.w.Hosts)
		w.w.PVCs = len(w.claims)
		app := project.AppOf(w.appLabel, annotationsOf(w.obj))
		if app == "" {
			app = w.w.Name
		}
		a := get(k.ns)
		a.apps[app] = append(a.apps[app], w.w)
	}
	for ns, hc := range nsPods {
		get(ns).n.Pods = *hc
	}
	for _, s := range in.Services {
		get(s.GetNamespace()).n.Services++
	}
	for _, i := range in.Ingresses {
		get(i.GetNamespace()).n.Ingresses++
	}
	for _, p := range in.PVCs {
		get(p.GetNamespace()).n.PVCs++
	}

	for _, a := range agg {
		for name, ws := range a.apps {
			sort.Slice(ws, func(i, j int) bool {
				if ws[i].Kind != ws[j].Kind {
					return ws[i].Kind < ws[j].Kind
				}
				return ws[i].Name < ws[j].Name
			})
			a.n.Apps = append(a.n.Apps, App{Name: name, Workloads: ws})
		}
		sort.Slice(a.n.Apps, func(i, j int) bool {
			if li, lj := len(a.n.Apps[i].Workloads), len(a.n.Apps[j].Workloads); li != lj {
				return li > lj
			}
			return a.n.Apps[i].Name < a.n.Apps[j].Name
		})
		if a.n.Apps == nil {
			a.n.Apps = []App{}
		}
		out.Namespaces = append(out.Namespaces, a.n)
	}
	sort.Slice(out.Namespaces, func(i, j int) bool {
		if pi, pj := out.Namespaces[i].Pods.Total, out.Namespaces[j].Pods.Total; pi != pj {
			return pi > pj
		}
		return out.Namespaces[i].Name < out.Namespaces[j].Name
	})
	if out.Namespaces == nil {
		out.Namespaces = []Namespace{}
	}
	compact(out, truncate(out))
	return out
}

func restartsOf(p *unstructured.Unstructured) int {
	n := 0
	for _, f := range []string{"initContainerStatuses", "containerStatuses"} {
		cs, _, _ := unstructured.NestedSlice(p.Object, "status", f)
		for _, c := range cs {
			if m, ok := c.(map[string]any); ok {
				if r, ok := m["restartCount"].(int64); ok {
					n += int(r)
				} else if r, ok := m["restartCount"].(float64); ok {
					n += int(r)
				}
			}
		}
	}
	return n
}

// ingressHosts maps "namespace/service" to the hosts of the Ingress rules (and default backend) routing to it;
// a rule without a host counts as "*".
func ingressHosts(ings []*unstructured.Unstructured) map[string][]string {
	out := map[string][]string{}
	add := func(ns string, backend map[string]any, host string) {
		name, _, _ := unstructured.NestedString(backend, "service", "name")
		if name == "" {
			name, _, _ = unstructured.NestedString(backend, "serviceName") // extensions/v1beta1
		}
		if name == "" {
			return
		}
		if host == "" {
			host = "*"
		}
		k := ns + "/" + name
		for _, h := range out[k] {
			if h == host {
				return
			}
		}
		out[k] = append(out[k], host)
	}
	for _, ing := range ings {
		ns := ing.GetNamespace()
		if b, ok, _ := unstructured.NestedMap(ing.Object, "spec", "defaultBackend"); ok {
			add(ns, b, "")
		}
		rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			host, _ := rm["host"].(string)
			paths, _, _ := unstructured.NestedSlice(rm, "http", "paths")
			for _, pth := range paths {
				pm, _ := pth.(map[string]any)
				if b, ok := pm["backend"].(map[string]any); ok {
					add(ns, b, host)
				}
			}
		}
	}
	return out
}

func annotationsOf(u *unstructured.Unstructured) map[string]string {
	if u == nil {
		return nil
	}
	return u.GetAnnotations()
}

// compact drops the optional tooltip-only fields when the map has more than CompactAbove workloads.
func compact(m *ClusterMap, n int) {
	if n <= CompactAbove {
		return
	}
	m.Compact = true
	for i := range m.Namespaces {
		for j := range m.Namespaces[i].Apps {
			ws := m.Namespaces[i].Apps[j].Workloads
			for k := range ws {
				ws[k].Status = ""
				ws[k].Services = nil
				ws[k].Hosts = nil
			}
		}
	}
}

// truncate drops workloads past MaxWorkloads, namespace by namespace in order, and returns how many are kept.
func truncate(m *ClusterMap) int {
	left := MaxWorkloads
	for i := range m.Namespaces {
		apps := m.Namespaces[i].Apps
		kept := apps[:0:0]
		for _, a := range apps {
			if left <= 0 {
				m.Truncated = true
				break
			}
			if len(a.Workloads) > left {
				a.Workloads = a.Workloads[:left]
				m.Truncated = true
			}
			left -= len(a.Workloads)
			kept = append(kept, a)
		}
		m.Namespaces[i].Apps = kept
	}
	return MaxWorkloads - left
}

// IsSystem reports whether a namespace is platform-owned (used by tests and callers wanting the same rule as the UI).
func IsSystem(ns string) bool {
	return strings.HasPrefix(ns, "kube-") || strings.HasPrefix(ns, "openshift-") || ns == "openshift"
}
