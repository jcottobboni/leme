package graph

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/project"
)

// routeKinds are the Gateway API route resources and their kinds.
var routeKinds = []struct{ resource, kind string }{
	{"httproutes", "HTTPRoute"}, {"grpcroutes", "GRPCRoute"}, {"tlsroutes", "TLSRoute"}, {"tcproutes", "TCPRoute"}, {"udproutes", "UDPRoute"},
}

const gatewaysKey = project.GatewayGroup + "/gateways"

func routeKey(resource string) string { return project.GatewayGroup + "/" + resource }

// gatewayAPI adds the Gateway API edges: Gateway → GatewayClass ("class"), Gateway → Route
// ("attaches", from the route's parentRefs, also across namespaces) and Route → Service ("routes",
// from backendRefs). Routes of other namespaces attached to a Gateway of this namespace come in
// Input.ForeignRoutes. Backend Services of this namespace that do not exist are marked missing.
func (b *builder) gatewayAPI() {
	ns := b.in.Namespace
	for _, gw := range b.list(gatewaysKey) {
		if class, _, _ := unstructured.NestedString(gw.Object, "spec", "gatewayClassName"); class != "" {
			b.edge(gid(project.GatewayGroup, "Gateway", ns, gw.GetName()), b.ref(project.GatewayGroup, "GatewayClass", "", class, nil), "class")
		}
	}
	var services map[string]bool
	if _, listed := b.in.Objects["/services"]; listed {
		services = map[string]bool{}
		for _, s := range b.list("/services") {
			services[s.GetName()] = true
		}
	}
	for _, rk := range routeKinds {
		for _, rt := range b.list(routeKey(rk.resource)) {
			b.routeEdges(rt, services)
		}
	}
	for _, rt := range b.in.ForeignRoutes {
		b.obj(rt)
		b.routeEdges(rt, nil)
	}
}

func (b *builder) routeEdges(rt *unstructured.Unstructured, services map[string]bool) {
	from := gid(project.GatewayGroup, rt.GetKind(), rt.GetNamespace(), rt.GetName())
	for _, p := range project.RouteParents(rt) {
		if p.Name == "" || p.Group != project.GatewayGroup {
			continue
		}
		b.edge(b.ref(p.Group, p.Kind, p.Namespace, p.Name, nil), from, "attaches")
	}
	for _, be := range project.RouteBackends(rt) {
		if !be.IsService() {
			continue
		}
		var known map[string]bool
		if be.Namespace == b.in.Namespace {
			known = services
		}
		b.edge(from, b.ref("", "Service", be.Namespace, be.Name, known), "routes")
	}
}

// foreignRoutes lists the routes of every namespace and keeps those of other namespaces attached to
// a Gateway of ns. Best effort: without cluster-wide list permission the topology simply shows the
// routes of the namespace.
func foreignRoutes(ctx context.Context, sess *cluster.Session, ns string, gateways []*unstructured.Unstructured) []*unstructured.Unstructured {
	if ns == "" || len(gateways) == 0 || sess.Dynamic == nil {
		return nil
	}
	names := map[string]bool{}
	for _, g := range gateways {
		names[g.GetName()] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out []*unstructured.Unstructured
	for _, rk := range routeKinds {
		res, ok := sess.Lookup(project.GatewayGroup, rk.resource)
		if !ok {
			continue
		}
		l, err := sess.Dynamic.Resource(res.GVR()).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			continue
		}
		for i := range l.Items {
			rt := &l.Items[i]
			if rt.GetNamespace() == ns {
				continue
			}
			for _, p := range project.RouteParents(rt) {
				if p.Group == project.GatewayGroup && p.Kind == "Gateway" && p.Namespace == ns && names[p.Name] {
					out = append(out, rt)
					break
				}
			}
		}
	}
	return out
}
