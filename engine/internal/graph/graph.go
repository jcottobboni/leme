// Package graph builds the relationship graph (topology) of a namespace or of one object.
package graph

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/project"
)

// Node is one vertex. Group/Kind/Name/Namespace let the UI open the object.
type Node struct {
	ID        string `json:"id"`
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Health    string `json:"health"`
	Status    string `json:"status,omitempty"`
	// Count > 0 marks an aggregate of that many pods owned by the same controller.
	Count int            `json:"count,omitempty"`
	Pods  map[string]int `json:"pods,omitempty"` // aggregate: pods per health
	// Ref marks a node known only because something references it (it was not listed).
	Ref     bool `json:"ref,omitempty"`
	Missing bool `json:"missing,omitempty"`
	// App is the application the object belongs to (labels / Helm release); empty when unknown.
	App string `json:"app,omitempty"`
	// Owner is the controller of an aggregate's pods (Count > 0), so the UI can open it by group and kind.
	Owner *Owner `json:"owner,omitempty"`
}

// Owner identifies the controller behind an aggregate node.
type Owner struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// Edge is a directed relationship.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Rel  string `json:"rel"`
}

// Graph is the API answer.
type Graph struct {
	Root      string            `json:"root,omitempty"`
	Nodes     []Node            `json:"nodes"`
	Edges     []Edge            `json:"edges"`
	Truncated bool              `json:"truncated,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// Input is everything the builder needs, already listed.
type Input struct {
	Namespace string
	// Objects keyed by "group/resource" (e.g. "/pods", "apps/deployments").
	Objects map[string][]*unstructured.Unstructured
	// Known names of ConfigMaps and Secrets in the namespace (nil = unknown, references are not checked).
	ConfigMaps, Secrets map[string]bool
	// Nodes are cluster nodes (may be empty when not permitted).
	Nodes []*unstructured.Unstructured
}

// AggregateAbove collapses a controller's pods into one node when it owns more than this many.
const AggregateAbove = 6

// MaxNodes caps the graph sent to the UI.
const MaxNodes = 2000

// id is the node id "Kind/namespace/name" of a built-in kind.
func id(kind, ns, name string) string { return kind + "/" + ns + "/" + name }

// gid is the node id of an object of any group: built-in kinds keep "Kind/namespace/name"; other
// groups use "Kind.group/namespace/name", so a CRD named like a built-in kind (Istio's Gateway, an
// operator's Deployment) is a different node.
func gid(group, kind, ns, name string) string {
	if !kinds.Builtin(group) {
		kind += "." + group
	}
	return id(kind, ns, name)
}

type builder struct {
	in    Input
	nodes map[string]*Node
	src   map[string]*unstructured.Unstructured
	edges map[Edge]bool
}

func (b *builder) add(n Node) *Node {
	if e, ok := b.nodes[n.ID]; ok {
		if e.Ref && !n.Ref {
			*e = n
		}
		return e
	}
	b.nodes[n.ID] = &n
	return &n
}

func (b *builder) obj(u *unstructured.Unstructured) *Node {
	gv, _ := schema.ParseGroupVersion(u.GetAPIVersion())
	row := project.Project(u)
	status, _ := row.Cells["status"].(string)
	nid := gid(gv.Group, u.GetKind(), u.GetNamespace(), u.GetName())
	b.src[nid] = u
	return b.add(Node{ID: nid, Group: gv.Group, Kind: u.GetKind(), Name: u.GetName(), Namespace: u.GetNamespace(), Health: row.Health, Status: status, App: project.AppOf(u.GetLabels(), u.GetAnnotations())})
}

// ref adds a node for something referenced by name; known reports whether it exists (nil = unknown).
func (b *builder) ref(group, kind, ns, name string, known map[string]bool) string {
	n := Node{ID: gid(group, kind, ns, name), Group: group, Kind: kind, Name: name, Namespace: ns, Health: "neutral", Ref: true}
	if known != nil && !known[name] {
		n.Missing, n.Health, n.Status = true, "error", "Missing"
	}
	return b.add(n).ID
}

func (b *builder) edge(from, to, rel string) {
	if from != to {
		b.edges[Edge{From: from, To: to, Rel: rel}] = true
	}
}

func (b *builder) list(key string) []*unstructured.Unstructured { return b.in.Objects[key] }

func labelsMatch(sel map[string]any, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if s, _ := v.(string); labels[k] != s {
			return false
		}
	}
	return true
}

// Build creates the full namespace graph.
func Build(in Input) *Graph {
	b := &builder{in: in, nodes: map[string]*Node{}, src: map[string]*unstructured.Unstructured{}, edges: map[Edge]bool{}}
	ns := in.Namespace
	recentBuilds := latestBuilds(b.list("build.openshift.io/builds"), 2)
	for _, key := range namespacedKeys {
		for _, u := range b.list(key) {
			if (key == "apps/replicasets" || key == "/replicationcontrollers") && isIdleReplicaSet(u) {
				continue
			}
			if key == "build.openshift.io/builds" && !recentBuilds[u.GetName()] {
				continue
			}
			b.obj(u)
		}
	}
	nodeHealth := map[string]*unstructured.Unstructured{}
	for _, n := range in.Nodes {
		nodeHealth[n.GetName()] = n
	}
	// Owner references.
	for _, n := range b.nodes {
		if n.Ref {
			continue
		}
		u := b.src[n.ID]
		if u == nil {
			continue
		}
		for _, o := range u.GetOwnerReferences() {
			og := kinds.GroupOf(o.APIVersion)
			oid := gid(og, o.Kind, ns, o.Name)
			if _, ok := b.nodes[oid]; ok {
				b.edge(oid, n.ID, "owns")
			} else if o.Controller != nil && *o.Controller && !kinds.Builtin(og) {
				// A controller the topology does not list (Argo Rollout, Kruise CloneSet…): show it as a
				// reference so its pods group under it and the UI can open it by group and kind.
				b.edge(b.ref(og, o.Kind, ns, o.Name, nil), n.ID, "owns")
			}
		}
	}
	pods := b.list("/pods")
	// Services select pods.
	for _, svc := range b.list("/services") {
		sel, _, _ := unstructured.NestedMap(svc.Object, "spec", "selector")
		for _, p := range pods {
			if labelsMatch(sel, p.GetLabels()) {
				b.edge(id("Service", ns, svc.GetName()), id("Pod", ns, p.GetName()), "selects")
			}
		}
	}
	// Ingress → Service.
	for _, ing := range b.list("networking.k8s.io/ingresses") {
		from := id("Ingress", ns, ing.GetName())
		if name, ok, _ := unstructured.NestedString(ing.Object, "spec", "defaultBackend", "service", "name"); ok {
			b.edge(from, b.ref("", "Service", ns, name, nil), "routes")
		}
		rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
		for _, r := range rules {
			paths, _, _ := unstructured.NestedSlice(asMap(r), "http", "paths")
			for _, p := range paths {
				if name, ok, _ := unstructured.NestedString(asMap(p), "backend", "service", "name"); ok {
					b.edge(from, b.ref("", "Service", ns, name, nil), "routes")
				}
			}
		}
		if tls, _, _ := unstructured.NestedSlice(ing.Object, "spec", "tls"); len(tls) > 0 {
			for _, t := range tls {
				if name, ok, _ := unstructured.NestedString(asMap(t), "secretName"); ok && name != "" {
					b.edge(from, b.ref("", "Secret", ns, name, in.Secrets), "tls")
				}
			}
		}
	}
	// Gateway API HTTPRoute → Service, Gateway → HTTPRoute.
	for _, rt := range b.list("gateway.networking.k8s.io/httproutes") {
		from := id("HTTPRoute", ns, rt.GetName())
		parents, _, _ := unstructured.NestedSlice(rt.Object, "spec", "parentRefs")
		for _, p := range parents {
			pm := asMap(p)
			name, _, _ := unstructured.NestedString(pm, "name")
			pns, _, _ := unstructured.NestedString(pm, "namespace")
			if pns == "" {
				pns = ns
			}
			if name != "" {
				b.edge(b.ref("gateway.networking.k8s.io", "Gateway", pns, name, nil), from, "attaches")
			}
		}
		rules, _, _ := unstructured.NestedSlice(rt.Object, "spec", "rules")
		for _, r := range rules {
			refs, _, _ := unstructured.NestedSlice(asMap(r), "backendRefs")
			for _, br := range refs {
				bm := asMap(br)
				kind, _, _ := unstructured.NestedString(bm, "kind")
				if kind != "" && kind != "Service" {
					continue
				}
				name, _, _ := unstructured.NestedString(bm, "name")
				bns, _, _ := unstructured.NestedString(bm, "namespace")
				if bns == "" {
					bns = ns
				}
				if name != "" {
					b.edge(from, b.ref("", "Service", bns, name, nil), "routes")
				}
			}
		}
	}
	// OpenShift Route → Service.
	for _, rt := range b.list("route.openshift.io/routes") {
		from := id("Route", ns, rt.GetName())
		if name, ok, _ := unstructured.NestedString(rt.Object, "spec", "to", "name"); ok {
			b.edge(from, b.ref("", "Service", ns, name, nil), "routes")
		}
		alts, _, _ := unstructured.NestedSlice(rt.Object, "spec", "alternateBackends")
		for _, a := range alts {
			if name, ok, _ := unstructured.NestedString(asMap(a), "name"); ok {
				b.edge(from, b.ref("", "Service", ns, name, nil), "routes")
			}
		}
	}
	// Pods → config, storage, service account, node.
	for _, p := range pods {
		b.podRefs(p, nodeHealth)
	}
	// PVC → PV → StorageClass.
	for _, pvc := range b.list("/persistentvolumeclaims") {
		from := id("PersistentVolumeClaim", ns, pvc.GetName())
		if v, ok, _ := unstructured.NestedString(pvc.Object, "spec", "volumeName"); ok && v != "" {
			pv := b.ref("", "PersistentVolume", "", v, nil)
			b.edge(from, pv, "bound")
		}
		if sc, ok, _ := unstructured.NestedString(pvc.Object, "spec", "storageClassName"); ok && sc != "" {
			b.edge(from, b.ref("storage.k8s.io", "StorageClass", "", sc, nil), "class")
		}
	}
	// OpenShift: BuildConfig → ImageStream (output), ImageStream → DeploymentConfig (image change trigger).
	for _, bc := range b.list("build.openshift.io/buildconfigs") {
		if kind, _, _ := unstructured.NestedString(bc.Object, "spec", "output", "to", "kind"); kind == "ImageStreamTag" {
			name, _, _ := unstructured.NestedString(bc.Object, "spec", "output", "to", "name")
			is, _, _ := strings.Cut(name, ":")
			isNS, _, _ := unstructured.NestedString(bc.Object, "spec", "output", "to", "namespace")
			if isNS == "" {
				isNS = ns
			}
			b.edge(id("BuildConfig", ns, bc.GetName()), b.ref("image.openshift.io", "ImageStream", isNS, is, nil), "produces")
		}
	}
	for _, dc := range b.list("apps.openshift.io/deploymentconfigs") {
		triggers, _, _ := unstructured.NestedSlice(dc.Object, "spec", "triggers")
		for _, t := range triggers {
			tm := asMap(t)
			if typ, _, _ := unstructured.NestedString(tm, "type"); typ != "ImageChange" {
				continue
			}
			name, _, _ := unstructured.NestedString(tm, "imageChangeParams", "from", "name")
			isNS, _, _ := unstructured.NestedString(tm, "imageChangeParams", "from", "namespace")
			if isNS == "" {
				isNS = ns
			}
			is, _, _ := strings.Cut(name, ":")
			if is != "" {
				b.edge(b.ref("image.openshift.io", "ImageStream", isNS, is, nil), id("DeploymentConfig", ns, dc.GetName()), "triggers")
			}
		}
	}
	// HPA → target.
	for _, h := range b.list("autoscaling/horizontalpodautoscalers") {
		av, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "apiVersion")
		kind, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "kind")
		name, _, _ := unstructured.NestedString(h.Object, "spec", "scaleTargetRef", "name")
		target := gid(kinds.GroupOf(av), kind, ns, name)
		if _, ok := b.nodes[target]; ok {
			b.edge(id("HorizontalPodAutoscaler", ns, h.GetName()), target, "scales")
		}
	}
	// ServiceAccount → RoleBinding → Role.
	for _, rb := range b.list("rbac.authorization.k8s.io/rolebindings") {
		rbID := id("RoleBinding", ns, rb.GetName())
		subs, _, _ := unstructured.NestedSlice(rb.Object, "subjects")
		bound := false
		for _, s := range subs {
			sm := asMap(s)
			kind, _, _ := unstructured.NestedString(sm, "kind")
			name, _, _ := unstructured.NestedString(sm, "name")
			sns, _, _ := unstructured.NestedString(sm, "namespace")
			if sns == "" {
				sns = ns
			}
			if kind == "ServiceAccount" && sns == ns {
				if _, ok := b.nodes[id("ServiceAccount", ns, name)]; ok {
					b.edge(id("ServiceAccount", ns, name), rbID, "bound")
					bound = true
				}
			}
		}
		if !bound {
			// Bindings for users/groups only clutter the workload view.
			delete(b.nodes, rbID)
			continue
		}
		kind, _, _ := unstructured.NestedString(rb.Object, "roleRef", "kind")
		name, _, _ := unstructured.NestedString(rb.Object, "roleRef", "name")
		rns := ns
		if kind == "ClusterRole" {
			rns = ""
		}
		b.edge(rbID, b.ref("rbac.authorization.k8s.io", kind, rns, name, nil), "grants")
	}
	// Service accounts nobody uses are noise (every namespace has "default").
	used := map[string]bool{}
	for e := range b.edges {
		used[e.From], used[e.To] = true, true
	}
	for nid, n := range b.nodes {
		if n.Kind == "ServiceAccount" && !used[nid] {
			delete(b.nodes, nid)
		}
	}
	return b.graph()
}

// latestBuilds keeps the newest n builds of each BuildConfig (old builds are history, not topology).
func latestBuilds(builds []*unstructured.Unstructured, n int) map[string]bool {
	by := map[string][]*unstructured.Unstructured{}
	for _, b := range builds {
		k := b.GetLabels()["openshift.io/build-config.name"]
		by[k] = append(by[k], b)
	}
	keep := map[string]bool{}
	for _, list := range by {
		sort.Slice(list, func(i, j int) bool { return list[i].GetCreationTimestamp().After(list[j].GetCreationTimestamp().Time) })
		for i, b := range list {
			if i < n {
				keep[b.GetName()] = true
			}
		}
	}
	return keep
}

func isIdleReplicaSet(u *unstructured.Unstructured) bool {
	spec, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	cur, _, _ := unstructured.NestedInt64(u.Object, "status", "replicas")
	return spec == 0 && cur == 0 && len(u.GetOwnerReferences()) > 0
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func (b *builder) podRefs(p *unstructured.Unstructured, nodes map[string]*unstructured.Unstructured) {
	ns := b.in.Namespace
	from := id("Pod", ns, p.GetName())
	vols, _, _ := unstructured.NestedSlice(p.Object, "spec", "volumes")
	for _, v := range vols {
		vm := asMap(v)
		if name, ok, _ := unstructured.NestedString(vm, "configMap", "name"); ok {
			b.edge(from, b.ref("", "ConfigMap", ns, name, b.in.ConfigMaps), "mounts")
		}
		if name, ok, _ := unstructured.NestedString(vm, "secret", "secretName"); ok {
			b.edge(from, b.ref("", "Secret", ns, name, b.in.Secrets), "mounts")
		}
		if name, ok, _ := unstructured.NestedString(vm, "persistentVolumeClaim", "claimName"); ok {
			b.edge(from, b.ref("", "PersistentVolumeClaim", ns, name, nil), "mounts")
		}
		srcs, _, _ := unstructured.NestedSlice(vm, "projected", "sources")
		for _, s := range srcs {
			sm := asMap(s)
			if name, ok, _ := unstructured.NestedString(sm, "configMap", "name"); ok && name != "kube-root-ca.crt" && name != "openshift-service-ca.crt" {
				b.edge(from, b.ref("", "ConfigMap", ns, name, b.in.ConfigMaps), "mounts")
			}
			if name, ok, _ := unstructured.NestedString(sm, "secret", "name"); ok {
				b.edge(from, b.ref("", "Secret", ns, name, b.in.Secrets), "mounts")
			}
		}
	}
	for _, field := range []string{"initContainers", "containers"} {
		cs, _, _ := unstructured.NestedSlice(p.Object, "spec", field)
		for _, c := range cs {
			cm := asMap(c)
			envFrom, _, _ := unstructured.NestedSlice(cm, "envFrom")
			for _, e := range envFrom {
				em := asMap(e)
				if name, ok, _ := unstructured.NestedString(em, "configMapRef", "name"); ok {
					b.edge(from, b.ref("", "ConfigMap", ns, name, b.in.ConfigMaps), "env")
				}
				if name, ok, _ := unstructured.NestedString(em, "secretRef", "name"); ok {
					b.edge(from, b.ref("", "Secret", ns, name, b.in.Secrets), "env")
				}
			}
			env, _, _ := unstructured.NestedSlice(cm, "env")
			for _, e := range env {
				em := asMap(e)
				if name, ok, _ := unstructured.NestedString(em, "valueFrom", "configMapKeyRef", "name"); ok {
					b.edge(from, b.ref("", "ConfigMap", ns, name, b.in.ConfigMaps), "env")
				}
				if name, ok, _ := unstructured.NestedString(em, "valueFrom", "secretKeyRef", "name"); ok {
					b.edge(from, b.ref("", "Secret", ns, name, b.in.Secrets), "env")
				}
			}
		}
	}
	pulls, _, _ := unstructured.NestedSlice(p.Object, "spec", "imagePullSecrets")
	for _, s := range pulls {
		if name, ok, _ := unstructured.NestedString(asMap(s), "name"); ok && name != "" {
			b.edge(from, b.ref("", "Secret", ns, name, b.in.Secrets), "pulls")
		}
	}
	if sa, ok, _ := unstructured.NestedString(p.Object, "spec", "serviceAccountName"); ok && sa != "" {
		if _, exists := b.nodes[id("ServiceAccount", ns, sa)]; exists {
			b.edge(from, id("ServiceAccount", ns, sa), "uses")
		}
	}
	if nodeName, ok, _ := unstructured.NestedString(p.Object, "spec", "nodeName"); ok && nodeName != "" {
		nid := id("Node", "", nodeName)
		if u, ok := nodes[nodeName]; ok {
			b.obj(u)
		} else {
			b.ref("", "Node", "", nodeName, nil)
		}
		b.edge(from, nid, "runs on")
	}
}

func (b *builder) graph() *Graph {
	g := &Graph{}
	for _, n := range b.nodes {
		g.Nodes = append(g.Nodes, *n)
	}
	for e := range b.edges {
		_, okF := b.nodes[e.From]
		_, okT := b.nodes[e.To]
		if okF && okT {
			g.Edges = append(g.Edges, e)
		}
	}
	sortGraph(g)
	return g
}

func sortGraph(g *Graph) {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
}

// dependencies are things workloads use. They are often shared by unrelated workloads, so from them the
// traversal only follows outgoing edges (PVC → PV → StorageClass, ServiceAccount → RoleBinding → Role).
var dependencies = map[string]bool{"ConfigMap": true, "Secret": true, "Node": true, "ServiceAccount": true, "StorageClass": true,
	"PersistentVolume": true, "PersistentVolumeClaim": true, "RoleBinding": true, "Role": true, "ClusterRole": true, "Gateway": true}

// Neighborhood keeps the part of g connected to root within depth hops.
func Neighborhood(g *Graph, root string, depth int) *Graph {
	out, in := map[string][]string{}, map[string][]string{}
	for _, e := range g.Edges {
		out[e.From] = append(out[e.From], e.To)
		in[e.To] = append(in[e.To], e.From)
	}
	dep := map[string]bool{}
	for _, n := range g.Nodes {
		dep[n.ID] = dependencies[n.Kind] && kinds.Builtin(n.Group)
	}
	keep := map[string]bool{root: true}
	frontier := []string{root}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			targets := out[n]
			if n == root || !dep[n] {
				targets = append(append([]string(nil), targets...), in[n]...)
			}
			for _, m := range targets {
				if !keep[m] {
					keep[m] = true
					next = append(next, m)
				}
			}
		}
		frontier = next
	}
	res := &Graph{Root: root, Errors: g.Errors}
	for _, n := range g.Nodes {
		if keep[n.ID] {
			res.Nodes = append(res.Nodes, n)
		}
	}
	for _, e := range g.Edges {
		if keep[e.From] && keep[e.To] {
			res.Edges = append(res.Edges, e)
		}
	}
	return res
}

// Aggregate collapses pods of controllers that own more than AggregateAbove pods (except keep).
func Aggregate(g *Graph, keep string) *Graph {
	owner := map[string]string{} // pod id -> owner id
	count := map[string]int{}
	for _, e := range g.Edges {
		if e.Rel == "owns" && strings.HasPrefix(e.To, "Pod/") {
			owner[e.To] = e.From
			count[e.From]++
		}
	}
	byID := map[string]*Node{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}
	group := map[string]string{} // pod id -> aggregate id
	aggs := map[string]*Node{}
	for pid, oid := range owner {
		if count[oid] <= AggregateAbove || pid == keep {
			continue
		}
		aid := "Pods/" + oid
		a, ok := aggs[aid]
		if !ok {
			parts := strings.SplitN(oid, "/", 3)
			a = &Node{ID: aid, Kind: "Pod", Name: fmt.Sprintf("%s pods", parts[len(parts)-1]), Namespace: parts[1], Health: "ok", Pods: map[string]int{}}
			if o := byID[oid]; o != nil {
				a.App = o.App
				a.Owner = &Owner{Group: o.Group, Kind: o.Kind, Name: o.Name}
			}
			aggs[aid] = a
		}
		group[pid] = aid
	}
	if len(group) == 0 {
		return g
	}
	rank := map[string]int{"neutral": 0, "ok": 1, "warn": 2, "error": 3}
	out := &Graph{Root: g.Root, Errors: g.Errors, Truncated: g.Truncated}
	for _, n := range g.Nodes {
		if aid, ok := group[n.ID]; ok {
			a := aggs[aid]
			a.Count++
			a.Pods[n.Health]++
			if a.App == "" {
				a.App = n.App
			}
			if rank[n.Health] > rank[a.Health] {
				a.Health = n.Health
			}
			continue
		}
		out.Nodes = append(out.Nodes, n)
	}
	for _, a := range aggs {
		a.Status = fmt.Sprintf("%d pods", a.Count)
		out.Nodes = append(out.Nodes, *a)
	}
	seen := map[Edge]bool{}
	for _, e := range g.Edges {
		if a, ok := group[e.From]; ok {
			e.From = a
		}
		if a, ok := group[e.To]; ok {
			e.To = a
		}
		if e.From != e.To && !seen[e] {
			seen[e] = true
			out.Edges = append(out.Edges, e)
		}
	}
	sortGraph(out)
	return out
}

// Cap trims a graph to MaxNodes.
func Cap(g *Graph) *Graph {
	if len(g.Nodes) <= MaxNodes {
		return g
	}
	keep := map[string]bool{}
	for _, n := range g.Nodes[:MaxNodes] {
		keep[n.ID] = true
	}
	out := &Graph{Root: g.Root, Errors: g.Errors, Truncated: true, Nodes: g.Nodes[:MaxNodes]}
	for _, e := range g.Edges {
		if keep[e.From] && keep[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	return out
}
