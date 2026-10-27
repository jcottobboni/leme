package analyze

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

func init() {
	for _, r := range []struct{ res, kind string }{
		{"gateways", "Gateway"}, {"gatewayclasses", "GatewayClass"}, {"httproutes", "HTTPRoute"}, {"grpcroutes", "GRPCRoute"},
		{"tlsroutes", "TLSRoute"}, {"tcproutes", "TCPRoute"}, {"udproutes", "UDPRoute"}, {"referencegrants", "ReferenceGrant"},
	} {
		testResources = append(testResources, cluster.Resource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: r.res, Kind: r.kind, Namespaced: r.kind != "GatewayClass"})
	}
	testResources = append(testResources, cluster.Resource{Group: "networking.istio.io", Version: "v1", Resource: "gateways", Kind: "Gateway", Namespaced: true})
}

const gwAPI = "gateway.networking.k8s.io/v1"

func conds(c ...m) m { return m{"conditions": toAny(c)} }

func toAny(c []m) []any {
	out := make([]any, len(c))
	for i := range c {
		out[i] = c[i]
	}
	return out
}

func parentStatus(gw string, c ...m) m {
	return m{"parents": []any{m{"parentRef": m{"name": gw}, "controllerName": "example.com/gw", "conditions": toAny(c)}}}
}

func TestGatewayAPIRules(t *testing.T) {
	svc := o("v1", "Service", "demo", "web", m{"spec": m{"selector": m{"app": "web"}, "ports": []any{m{"name": "http", "port": int64(80), "targetPort": int64(8080)}}}})
	other := o("v1", "Service", "backend", "api", m{"spec": m{"ports": []any{m{"port": int64(8080)}}}})
	gw := o(gwAPI, "Gateway", "demo", "gw", m{"spec": m{"gatewayClassName": "example"},
		"status": conds(m{"type": "Accepted", "status": "True"}, m{"type": "Programmed", "status": "False", "reason": "AddressNotAssigned", "message": "no address"})})
	pending := o(gwAPI, "Gateway", "demo", "gw-pending", m{"spec": m{"gatewayClassName": "none"},
		"status": conds(m{"type": "Accepted", "status": "Unknown", "reason": "Pending"}, m{"type": "Programmed", "status": "Unknown", "reason": "Pending"})})
	ok := o(gwAPI, "HTTPRoute", "demo", "ok", m{"spec": m{"parentRefs": []any{m{"name": "gw"}}, "rules": []any{m{"backendRefs": []any{m{"name": "web", "port": int64(80)}}}}},
		"status": parentStatus("gw", m{"type": "Accepted", "status": "True"}, m{"type": "ResolvedRefs", "status": "True"})})
	missing := o(gwAPI, "HTTPRoute", "demo", "missing", m{"spec": m{"parentRefs": []any{m{"name": "gw"}}, "rules": []any{m{"backendRefs": []any{m{"name": "nope", "port": int64(80)}}}}}})
	badPort := o(gwAPI, "HTTPRoute", "demo", "bad-port", m{"spec": m{"parentRefs": []any{m{"name": "gw"}}, "rules": []any{m{"backendRefs": []any{m{"name": "web", "port": int64(9999)}}}}}})
	rejected := o(gwAPI, "HTTPRoute", "demo", "rejected", m{"spec": m{"parentRefs": []any{m{"name": "gw"}, m{"name": "ghost"}}},
		"status": parentStatus("gw", m{"type": "Accepted", "status": "False", "reason": "NoMatchingListenerHostname"}, m{"type": "ResolvedRefs", "status": "False", "reason": "BackendNotFound"})})
	cross := o(gwAPI, "GRPCRoute", "demo", "cross", m{"spec": m{"parentRefs": []any{m{"name": "gw"}}, "rules": []any{m{"backendRefs": []any{m{"name": "api", "namespace": "backend", "port": int64(8080)}}}}}})
	granted := o(gwAPI, "HTTPRoute", "demo", "granted", m{"spec": m{"parentRefs": []any{m{"name": "gw"}}, "rules": []any{m{"backendRefs": []any{m{"name": "api", "namespace": "backend", "port": int64(8080)}}}}}})
	grant := o(gwAPI, "ReferenceGrant", "backend", "allow-demo", m{"spec": m{
		"from": []any{m{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": "demo"}},
		"to":   []any{m{"group": "", "kind": "Service"}},
	}})
	// Istio Gateway with fields that would look broken to the Gateway API rules.
	istio := o("networking.istio.io/v1", "Gateway", "demo", "gw", m{"spec": m{"selector": m{"istio": "ingressgateway"}},
		"status": conds(m{"type": "Programmed", "status": "False", "reason": "Invalid"})})

	s := session(svc, other, ok, missing, badPort, rejected, cross, granted, grant)
	// The fake tracker guesses "gatewaies" from the kind: Gateways are stored under their real resource.
	tr := s.Dynamic.(*dynfake.FakeDynamicClient).Tracker()
	for _, u := range []*unstructured.Unstructured{gw, pending, istio} {
		gv, _ := schema.ParseGroupVersion(u.GetAPIVersion())
		if err := tr.Create(gv.WithResource("gateways"), u, u.GetNamespace()); err != nil {
			t.Fatal(err)
		}
	}
	get := func(group, kind, name string) []Finding {
		t.Helper()
		fs, err := Object(context.Background(), s, Ref{group, kind, "demo", name})
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}
	const g = "gateway.networking.k8s.io"
	if f := find(get(g, "Gateway", "gw"), "gateway-not-programmed"); f == nil || !strings.Contains(strings.Join(f.Evidence, " "), "AddressNotAssigned") {
		t.Errorf("gateway-not-programmed: %+v", f)
	}
	if find(get(g, "Gateway", "gw-pending"), "gateway-pending") == nil {
		t.Error("gateway-pending")
	}
	if fs := get(g, "HTTPRoute", "ok"); len(fs) != 0 {
		t.Errorf("healthy route got %s", ids(fs))
	}
	if f := find(get(g, "HTTPRoute", "missing"), "gwroute-missing-backend"); f == nil || !f.Fixable || f.Severity != High {
		t.Errorf("gwroute-missing-backend: %+v", f)
	}
	if find(get(g, "HTTPRoute", "bad-port"), "gwroute-missing-port") == nil {
		t.Error("gwroute-missing-port")
	}
	fs := get(g, "HTTPRoute", "rejected")
	for _, id := range []string{"gwroute-not-accepted", "gwroute-refs-unresolved", "gwroute-missing-gateway"} {
		if find(fs, id) == nil {
			t.Errorf("rejected: missing %s in %s", id, ids(fs))
		}
	}
	if f := find(get(g, "GRPCRoute", "cross"), "gwroute-no-referencegrant"); f == nil || !strings.Contains(f.Suggestion, "kind: GRPCRoute") {
		t.Errorf("gwroute-no-referencegrant: %+v", f)
	}
	if fs := get(g, "HTTPRoute", "granted"); len(fs) != 0 {
		t.Errorf("granted cross-namespace route got %s", ids(fs))
	}
	// Istio's Gateway never gets Gateway API (or any built-in) findings.
	if fs := get("networking.istio.io", "Gateway", "gw"); len(fs) != 0 {
		t.Errorf("Istio Gateway got %s", ids(fs))
	}
	// Namespace diagnosis includes the Gateway API kinds.
	all, _, err := Namespace(context.Background(), s, "demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gateway-not-programmed", "gwroute-missing-backend", "gwroute-no-referencegrant"} {
		if find(all, id) == nil {
			t.Errorf("namespace: missing %s in %s", id, ids(all))
		}
	}
	for _, f := range all {
		if f.Object.Group == "networking.istio.io" {
			t.Errorf("namespace: Istio finding %s", f.ID)
		}
	}
}
