package project

import (
	"testing"
)

func TestGatewayProjection(t *testing.T) {
	gw := obj(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: gw, namespace: infra, uid: g1}
spec:
  gatewayClassName: example
  listeners:
  - {name: http, port: 80, protocol: HTTP}
  - {name: https, port: 443, protocol: HTTPS, hostname: "*.example.com"}
status:
  addresses: [{type: IPAddress, value: 10.0.0.7}]
  listeners: [{name: http, attachedRoutes: 2}, {name: https, attachedRoutes: 1}]
  conditions:
  - {type: Accepted, status: "True"}
  - {type: Programmed, status: "True"}
`)
	r := Project(gw)
	if r.Cells["class"] != "example" || r.Cells["addresses"] != "10.0.0.7" || r.Cells["routes"] != int64(3) ||
		r.Cells["listeners"] != "http:80/HTTP, https:443/HTTPS *.example.com" || r.Cells["status"] != GwProgrammed || r.Health != "ok" {
		t.Fatalf("got %+v", r)
	}
	cols := Columns("gateway.networking.k8s.io", "Gateway", true)
	if len(cols) < 6 || cols[2].Key != "class" {
		t.Fatalf("columns %+v", cols)
	}

	for _, c := range []struct{ conds, state, health string }{
		{`[{type: Accepted, status: "False", reason: InvalidParameters}]`, GwNotAccepted, "error"},
		{`[{type: Accepted, status: "True"}, {type: Programmed, status: "False", reason: AddressNotAssigned}]`, GwNotProgrammed, "error"},
		{`[{type: Accepted, status: Unknown, reason: Pending}, {type: Programmed, status: Unknown, reason: Pending}]`, GwPending, "warn"},
		{`[]`, RouteNoStatus, "neutral"},
	} {
		u := obj(t, "apiVersion: gateway.networking.k8s.io/v1\nkind: Gateway\nmetadata: {name: g, namespace: n}\nstatus: {conditions: "+c.conds+"}\n")
		if r := Project(u); r.Cells["status"] != c.state || r.Health != c.health {
			t.Errorf("%s: got %v/%s", c.conds, r.Cells["status"], r.Health)
		}
	}
}

func TestIstioGatewayNotProjectedAsGatewayAPI(t *testing.T) {
	u := obj(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata: {name: gw, namespace: demo}
spec: {selector: {istio: ingressgateway}, servers: [{port: {number: 80, name: http, protocol: HTTP}, hosts: ["*"]}]}
`)
	r := Project(u)
	if _, ok := r.Cells["listeners"]; ok {
		t.Fatalf("Istio Gateway projected as Gateway API: %+v", r)
	}
	if cols := Columns("networking.istio.io", "Gateway", true); len(cols) != 3 {
		t.Fatalf("Istio Gateway columns %+v", cols)
	}
}

func TestRouteProjection(t *testing.T) {
	rt := obj(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: web, namespace: demo}
spec:
  hostnames: [web.example.com]
  parentRefs: [{name: gw, namespace: infra, sectionName: http}]
  rules:
  - backendRefs: [{name: web, port: 80}, {name: api, namespace: backend, port: 8080}]
  - backendRefs: [{name: web, port: 80}]
status:
  parents:
  - parentRef: {name: gw, namespace: infra}
    controllerName: example.com/gw
    conditions: [{type: Accepted, status: "True"}, {type: ResolvedRefs, status: "True"}]
`)
	r := Project(rt)
	if r.Cells["hostnames"] != "web.example.com" || r.Cells["parents"] != "infra/gw:http" || r.Cells["backends"] != "web:80, backend/api:8080" ||
		r.Cells["status"] != RouteAccepted || r.Health != "ok" {
		t.Fatalf("got %+v", r)
	}
	for _, c := range []struct{ conds, state, health string }{
		{`[{type: Accepted, status: "False", reason: NotAllowedByListeners}]`, RouteNotAccept, "error"},
		{`[{type: Accepted, status: "True"}, {type: ResolvedRefs, status: "False", reason: BackendNotFound}]`, RouteUnresolved, "error"},
		{`[{type: Accepted, status: Unknown}]`, GwPending, "warn"},
	} {
		u := obj(t, "apiVersion: gateway.networking.k8s.io/v1\nkind: TCPRoute\nmetadata: {name: t, namespace: n}\nstatus: {parents: [{parentRef: {name: gw}, conditions: "+c.conds+"}]}\n")
		if r := Project(u); r.Cells["status"] != c.state || r.Health != c.health {
			t.Errorf("%s: got %v/%s", c.conds, r.Cells["status"], r.Health)
		}
	}
	if r := Project(obj(t, "apiVersion: gateway.networking.k8s.io/v1\nkind: UDPRoute\nmetadata: {name: u, namespace: n}\n")); r.Cells["status"] != RouteNoStatus || r.Health != "neutral" {
		t.Errorf("no status: %+v", r)
	}
	if cols := Columns("gateway.networking.k8s.io", "TCPRoute", true); cols[2].Key != "parents" {
		t.Errorf("TCPRoute has no hostnames column: %+v", cols)
	}
}

func TestGatewayClassAndGrant(t *testing.T) {
	r := Project(obj(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: {name: example}
spec: {controllerName: example.com/gateway-controller}
status: {conditions: [{type: Accepted, status: "True"}]}
`))
	if r.Cells["controller"] != "example.com/gateway-controller" || r.Cells["status"] != GwAccepted || r.Health != "ok" {
		t.Fatalf("class %+v", r)
	}
	r = Project(obj(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: ReferenceGrant
metadata: {name: g, namespace: backend}
spec:
  from: [{group: gateway.networking.k8s.io, kind: HTTPRoute, namespace: demo}]
  to: [{group: "", kind: Service, name: api}]
`))
	if r.Cells["from"] != "demo/HTTPRoute" || r.Cells["to"] != "Service api" {
		t.Fatalf("grant %+v", r)
	}
}
