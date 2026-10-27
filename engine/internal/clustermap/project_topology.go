package clustermap

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/project"
)

// ProjectTopology is the topology of one namespace as the OpenShift console draws it: workloads with
// their pod phases, grouped by application (app.kubernetes.io/part-of) and Helm release, with the
// route or ingress URL, the latest build and the app.openshift.io/connects-to links.
type ProjectTopology struct {
	Namespace string            `json:"namespace"`
	Nodes     []TopoNode        `json:"nodes"`
	Edges     []TopoEdge        `json:"edges"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// PodPhases counts pods the way the console colors the ring around a workload.
type PodPhases struct {
	Running     int `json:"running"`
	NotReady    int `json:"notReady"`
	Pending     int `json:"pending"`
	Failed      int `json:"failed"`
	Succeeded   int `json:"succeeded"`
	Terminating int `json:"terminating"`
	Unknown     int `json:"unknown"`
}

type TopoPod struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"` // one of the PodPhases keys
	Ready    string `json:"ready"`
	Restarts int64  `json:"restarts"`
}

type TopoBuild struct {
	Config string `json:"config"`
	Name   string `json:"name"`
	Phase  string `json:"phase"` // New, Pending, Running, Complete, Failed, Error, Cancelled
}

type TopoNode struct {
	ID       string            `json:"id"` // Kind/name
	Group    string            `json:"group"`
	Kind     string            `json:"kind"`
	Name     string            `json:"name"`
	App      string            `json:"app,omitempty"`     // application grouping (part-of)
	Release  string            `json:"release,omitempty"` // Helm release
	Health   string            `json:"health"`
	Status   string            `json:"status,omitempty"`
	Ready    string            `json:"ready,omitempty"`
	Desired  *int64            `json:"desired,omitempty"`
	Phases   PodPhases         `json:"phases"`
	Pods     []TopoPod         `json:"pods"`
	Services []string          `json:"services"`
	URL      string            `json:"url,omitempty"`
	VCS      string            `json:"vcs,omitempty"`
	Runtime  string            `json:"runtime,omitempty"`
	Build    *TopoBuild        `json:"build,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type TopoEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// TopoInput is everything BuildProject reads, all from one namespace. Any list may be empty.
type TopoInput struct {
	Namespace                                                          string
	Pods, ReplicaSets, ReplicationControllers                          []*unstructured.Unstructured
	Deployments, StatefulSets, DaemonSets, Jobs, CronJobs, DeployConfs []*unstructured.Unstructured
	Services, Ingresses, Routes, BuildConfigs, Builds                  []*unstructured.Unstructured
	Errors                                                             map[string]string
}

// podPhase classifies a pod like the console's pod ring: terminating first, then image and crash
// errors, then readiness.
func podPhase(p *unstructured.Unstructured) string {
	if p.GetDeletionTimestamp() != nil {
		return "terminating"
	}
	phase, _, _ := unstructured.NestedString(p.Object, "status", "phase")
	statuses, _, _ := unstructured.NestedSlice(p.Object, "status", "containerStatuses")
	failing, allReady := false, len(statuses) > 0
	for _, c := range statuses {
		m, _ := c.(map[string]any)
		if ready, _ := m["ready"].(bool); !ready {
			allReady = false
		}
		reason, _, _ := unstructured.NestedString(m, "state", "waiting", "reason")
		switch reason {
		case "CrashLoopBackOff", "ErrImagePull", "ImagePullBackOff", "CreateContainerConfigError", "CreateContainerError", "InvalidImageName", "RunContainerError":
			failing = true
		}
	}
	switch phase {
	case "Succeeded":
		return "succeeded"
	case "Failed":
		return "failed"
	case "Pending":
		if failing {
			return "failed"
		}
		return "pending"
	case "Running":
		if failing {
			return "failed"
		}
		if allReady {
			return "running"
		}
		return "notReady"
	}
	return "unknown"
}

func (p *PodPhases) add(phase string) {
	switch phase {
	case "running":
		p.Running++
	case "notReady":
		p.NotReady++
	case "pending":
		p.Pending++
	case "failed":
		p.Failed++
	case "succeeded":
		p.Succeeded++
	case "terminating":
		p.Terminating++
	default:
		p.Unknown++
	}
}

// connectsTo parses app.openshift.io/connects-to: a JSON list of names or {kind, name} objects
// (older consoles wrote a plain comma-free name list too).
func connectsTo(v string) [][2]string {
	if v == "" {
		return nil
	}
	var raw []json.RawMessage
	if json.Unmarshal([]byte(v), &raw) != nil {
		return [][2]string{{"", v}}
	}
	var out [][2]string
	for _, r := range raw {
		var name string
		if json.Unmarshal(r, &name) == nil {
			out = append(out, [2]string{"", name})
			continue
		}
		var ref struct{ Kind, Name string }
		if json.Unmarshal(r, &ref) == nil && ref.Name != "" {
			out = append(out, [2]string{ref.Kind, ref.Name})
		}
	}
	return out
}

func releaseOf(u *unstructured.Unstructured) string {
	if v := u.GetAnnotations()["meta.helm.sh/release-name"]; v != "" {
		return v
	}
	if l := u.GetLabels(); l["app.kubernetes.io/managed-by"] == "Helm" {
		return l["app.kubernetes.io/instance"]
	}
	return ""
}

// BuildProject assembles the topology of one namespace.
func BuildProject(in TopoInput) *ProjectTopology {
	out := &ProjectTopology{Namespace: in.Namespace, Nodes: []TopoNode{}, Edges: []TopoEdge{}, Errors: in.Errors}
	type node struct {
		n      TopoNode
		obj    *unstructured.Unstructured
		labels map[string]string // pod template labels (or the first pod's)
	}
	nodes := map[wkey]*node{}
	var order []wkey
	add := func(group string, u *unstructured.Unstructured, kind string) *node {
		k := wkey{u.GetNamespace(), group, kind, u.GetName()}
		if n, ok := nodes[k]; ok {
			return n
		}
		row := project.Project(u)
		l := u.GetLabels()
		n := &node{obj: u, labels: templateLabels(group, kind, u), n: TopoNode{
			ID: kind + "/" + u.GetName(), Group: group, Kind: kind, Name: u.GetName(), Health: row.Health,
			Status: str(row, "status"), Ready: str(row, "ready"), App: l["app.kubernetes.io/part-of"], Release: releaseOf(u),
			VCS: u.GetAnnotations()["app.openshift.io/vcs-uri"], Runtime: l["app.openshift.io/runtime"], Labels: l,
			Pods: []TopoPod{}, Services: []string{},
		}}
		if d, ok, _ := unstructured.NestedInt64(u.Object, "spec", "replicas"); ok {
			n.n.Desired = &d
		}
		nodes[k] = n
		order = append(order, k)
		return n
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
	owner := map[wkey]wkey{}
	for _, g := range []struct {
		group, kind, parent string
		objs                []*unstructured.Unstructured
	}{{"apps", "ReplicaSet", "Deployment", in.ReplicaSets}, {"", "ReplicationController", "DeploymentConfig", in.ReplicationControllers}, {"batch", "Job", "CronJob", in.Jobs}} {
		for _, u := range g.objs {
			if k, n, av := ownerOf(u); k == g.parent {
				owner[wkey{u.GetNamespace(), g.group, g.kind, u.GetName()}] = wkey{u.GetNamespace(), kinds.GroupOf(av), k, n}
			} else {
				add(g.group, u, g.kind) // standalone controller
			}
		}
	}
	for _, p := range in.Pods {
		var n *node
		if kind, name, av := ownerOf(p); kind != "" && kind != "Node" {
			k := wkey{p.GetNamespace(), kinds.GroupOf(av), kind, name}
			if top, ok := owner[k]; ok {
				k = top
			}
			if n = nodes[k]; n == nil { // controller not listed (operator CRD, cache gap): keep its pods visible
				n = &node{labels: p.GetLabels(), n: TopoNode{ID: k.kind + "/" + k.name, Group: k.group, Kind: k.kind, Name: k.name, Health: "neutral", Pods: []TopoPod{}, Services: []string{}}}
				nodes[k] = n
				order = append(order, k)
			}
		} else {
			n = add("", p, "Pod")
		}
		row := project.Project(p)
		ph := podPhase(p)
		n.n.Phases.add(ph)
		restarts, _ := row.Cells["restarts"].(int64)
		n.n.Pods = append(n.n.Pods, TopoPod{Name: p.GetName(), Phase: ph, Ready: str(row, "ready"), Restarts: restarts})
		if n.labels == nil {
			n.labels = p.GetLabels()
		}
	}

	// Services, then the route/ingress URL reaching them.
	urlBySvc := map[string]string{}
	for _, r := range in.Routes {
		svc, _, _ := unstructured.NestedString(r.Object, "spec", "to", "name")
		host, _, _ := unstructured.NestedString(r.Object, "spec", "host")
		if svc == "" || host == "" || urlBySvc[svc] != "" {
			continue
		}
		scheme := "http://"
		if tls, ok, _ := unstructured.NestedMap(r.Object, "spec", "tls"); ok && tls != nil {
			scheme = "https://"
		}
		path, _, _ := unstructured.NestedString(r.Object, "spec", "path")
		urlBySvc[svc] = scheme + host + path
	}
	for _, i := range in.Ingresses {
		rules, _, _ := unstructured.NestedSlice(i.Object, "spec", "rules")
		tls, _, _ := unstructured.NestedSlice(i.Object, "spec", "tls")
		scheme := "http://"
		if len(tls) > 0 {
			scheme = "https://"
		}
		for _, rr := range rules {
			rm, _ := rr.(map[string]any)
			host, _ := rm["host"].(string)
			paths, _, _ := unstructured.NestedSlice(rm, "http", "paths")
			for _, pp := range paths {
				pm, _ := pp.(map[string]any)
				svc, _, _ := unstructured.NestedString(pm, "backend", "service", "name")
				path, _ := pm["path"].(string)
				if host != "" && svc != "" && urlBySvc[svc] == "" {
					urlBySvc[svc] = scheme + host + path
				}
			}
		}
	}
	for _, k := range order {
		n := nodes[k]
		for _, s := range in.Services {
			sel, _, _ := unstructured.NestedStringMap(s.Object, "spec", "selector")
			if selects(sel, n.labels) {
				n.n.Services = append(n.n.Services, s.GetName())
				if n.n.URL == "" {
					n.n.URL = urlBySvc[s.GetName()]
				}
			}
		}
		sort.Strings(n.n.Services)
	}

	// Latest build of the BuildConfig that belongs to each workload (same name or same "app" label).
	latest := map[string]*unstructured.Unstructured{}
	for _, b := range in.Builds {
		bc := b.GetLabels()["openshift.io/build-config.name"]
		if bc == "" {
			continue
		}
		if cur := latest[bc]; cur == nil || buildNumber(b) > buildNumber(cur) {
			latest[bc] = b
		}
	}
	for _, k := range order {
		n := nodes[k]
		for _, bc := range in.BuildConfigs {
			app := bc.GetLabels()["app"]
			if bc.GetName() != n.n.Name && (app == "" || app != n.n.Labels["app"]) {
				continue
			}
			tb := &TopoBuild{Config: bc.GetName()}
			if b := latest[bc.GetName()]; b != nil {
				tb.Name = b.GetName()
				tb.Phase, _, _ = unstructured.NestedString(b.Object, "status", "phase")
			}
			n.n.Build = tb
			break
		}
	}

	byName := map[string][]string{}
	for _, k := range order {
		byName[k.name] = append(byName[k.name], nodes[k].n.ID)
	}
	for _, k := range order {
		n := nodes[k]
		if n.obj == nil {
			continue
		}
		for _, ref := range connectsTo(n.obj.GetAnnotations()["app.openshift.io/connects-to"]) {
			for _, id := range byName[ref[1]] {
				if (ref[0] == "" || id == ref[0]+"/"+ref[1]) && id != n.n.ID {
					out.Edges = append(out.Edges, TopoEdge{Source: n.n.ID, Target: id})
				}
			}
		}
		out.Nodes = append(out.Nodes, n.n)
	}
	for _, k := range order {
		if nodes[k].obj == nil {
			out.Nodes = append(out.Nodes, nodes[k].n)
		}
	}
	sort.SliceStable(out.Nodes, func(i, j int) bool { return out.Nodes[i].Name < out.Nodes[j].Name })
	return out
}

func buildNumber(b *unstructured.Unstructured) int64 {
	n, _ := strconv.ParseInt(b.GetAnnotations()["openshift.io/build.number"], 10, 64)
	if n == 0 {
		n = b.GetCreationTimestamp().Unix()
	}
	return n
}

// CollectProject lists one namespace from the informer caches and builds its topology.
func CollectProject(ctx context.Context, s *cluster.Session, ns string) *ProjectTopology {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	in := TopoInput{Namespace: ns, Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	list := func(group, resource string, dst *[]*unstructured.Unstructured) {
		r, ok := s.Lookup(group, resource)
		if !ok {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			objs, err := s.Informers.List(ctx, cluster.Key{GVR: r.GVR(), Namespace: ns})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				in.Errors[resource] = err.Error()
				return
			}
			*dst = objs
		}()
	}
	list("", "pods", &in.Pods)
	list("apps", "replicasets", &in.ReplicaSets)
	list("", "replicationcontrollers", &in.ReplicationControllers)
	list("apps", "deployments", &in.Deployments)
	list("apps", "statefulsets", &in.StatefulSets)
	list("apps", "daemonsets", &in.DaemonSets)
	list("batch", "jobs", &in.Jobs)
	list("batch", "cronjobs", &in.CronJobs)
	list("apps.openshift.io", "deploymentconfigs", &in.DeployConfs)
	list("", "services", &in.Services)
	list("networking.k8s.io", "ingresses", &in.Ingresses)
	list("route.openshift.io", "routes", &in.Routes)
	list("build.openshift.io", "buildconfigs", &in.BuildConfigs)
	list("build.openshift.io", "builds", &in.Builds)
	wg.Wait()
	if len(in.Errors) == 0 {
		in.Errors = nil
	}
	return BuildProject(in)
}
