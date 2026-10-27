package graph

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func hasEdge(g *Graph, from, to, rel string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Rel == rel {
			return true
		}
	}
	return false
}

func TestGatewayAPIEdges(t *testing.T) {
	in := Input{Namespace: "demo", Objects: map[string][]*unstructured.Unstructured{
		"/services": objs(t, `apiVersion: v1
kind: Service
metadata: {name: web, namespace: demo}
spec: {selector: {app: web}}`),
		"/pods": objs(t, `apiVersion: v1
kind: Pod
metadata: {name: web-1, namespace: demo, labels: {app: web}}
status: {phase: Running}`),
		"gateway.networking.k8s.io/gateways": objs(t, `apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: gw, namespace: demo}
spec: {gatewayClassName: example, listeners: [{name: http, port: 80, protocol: HTTP}]}
status: {conditions: [{type: Accepted, status: "True"}, {type: Programmed, status: "True"}]}`),
		"gateway.networking.k8s.io/httproutes": objs(t, `apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: web, namespace: demo}
spec:
  parentRefs: [{name: gw}, {name: shared, namespace: infra}]
  rules: [{backendRefs: [{name: web, port: 80}, {name: ghost, port: 80}, {name: api, namespace: backend, port: 8080}]}]`),
		"gateway.networking.k8s.io/grpcroutes": objs(t, `apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata: {name: rpc, namespace: demo}
spec:
  parentRefs: [{name: gw}]
  rules: [{backendRefs: [{name: web, port: 9000}, {group: example.com, kind: Backend, name: ext}]}]`),
	},
		ForeignRoutes: objs(t, `apiVersion: gateway.networking.k8s.io/v1
kind: TCPRoute
metadata: {name: db, namespace: other}
spec:
  parentRefs: [{name: gw, namespace: demo}]
  rules: [{backendRefs: [{name: db, port: 5432}]}]`),
	}
	g := Build(in)
	gw := "Gateway/demo/gw"
	if n := node(g, gw); n == nil || n.Ref || n.Health != "ok" || n.Group != "gateway.networking.k8s.io" {
		t.Fatalf("gateway node %+v", n)
	}
	checks := []struct{ from, to, rel string }{
		{gw, "GatewayClass//example", "class"},
		{gw, "HTTPRoute/demo/web", "attaches"},
		{"Gateway/infra/shared", "HTTPRoute/demo/web", "attaches"}, // cross-namespace parent
		{gw, "GRPCRoute/demo/rpc", "attaches"},
		{"HTTPRoute/demo/web", "Service/demo/web", "routes"},
		{"HTTPRoute/demo/web", "Service/demo/ghost", "routes"},
		{"HTTPRoute/demo/web", "Service/backend/api", "routes"},
		{"GRPCRoute/demo/rpc", "Service/demo/web", "routes"},
		{"Service/demo/web", "Pod/demo/web-1", "selects"},
		{gw, "TCPRoute/other/db", "attaches"}, // route of another namespace attached to this Gateway
		{"TCPRoute/other/db", "Service/other/db", "routes"},
	}
	for _, c := range checks {
		if !hasEdge(g, c.from, c.to, c.rel) {
			t.Errorf("missing edge %s -%s-> %s", c.from, c.rel, c.to)
		}
	}
	if n := node(g, "Service/demo/ghost"); n == nil || !n.Missing {
		t.Errorf("missing backend must be marked: %+v", n)
	}
	if n := node(g, "Service/backend/api"); n == nil || n.Missing {
		t.Errorf("a backend in another namespace is unknown, not missing: %+v", n)
	}
	for _, e := range g.Edges {
		if e.To == "Backend.example.com/demo/ext" {
			t.Errorf("non-Service backends are not drawn: %+v", e)
		}
	}
	// From the route, the neighborhood reaches the Gateway and the pods behind the Service.
	nb := Neighborhood(g, "HTTPRoute/demo/web", 8)
	for _, id := range []string{gw, "Service/demo/web", "Pod/demo/web-1"} {
		if node(nb, id) == nil {
			t.Errorf("neighborhood lacks %s", id)
		}
	}
}

func TestIstioGatewayIsNotGatewayAPI(t *testing.T) {
	in := Input{Namespace: "demo", Objects: map[string][]*unstructured.Unstructured{
		"gateway.networking.k8s.io/httproutes": objs(t, `apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: web, namespace: demo}
spec: {parentRefs: [{name: gw}]}`),
	}}
	g := Build(in)
	if !hasEdge(g, "Gateway/demo/gw", "HTTPRoute/demo/web", "attaches") {
		t.Fatal("edge from the Gateway API Gateway")
	}
	// An Istio Gateway with the same name would be "Gateway.networking.istio.io/demo/gw": a different node.
	if node(g, "Gateway.networking.istio.io/demo/gw") != nil {
		t.Fatal("Istio Gateway node created")
	}
}
