package mcp

import (
	"strings"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func TestFindResourceAmbiguity(t *testing.T) {
	res := []cluster.Resource{
		{Group: "networking.istio.io", Version: "v1", Resource: "gateways", Kind: "Gateway", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways", Kind: "Gateway", Namespaced: true},
		{Group: "", Version: "v1", Resource: "nodes", Kind: "Node"},
		{Group: "longhorn.io", Version: "v1beta2", Resource: "nodes", Kind: "Node", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true, ShortNames: []string{"deploy"}},
	}
	// Both orders: the answer must not depend on discovery order.
	for _, order := range [][]cluster.Resource{res, {res[1], res[0], res[3], res[2], res[4]}} {
		s := cluster.NewStaticSession(kube.Context{Name: "t"}, nil, nil, order, true)
		_, err := findResource(s, "", "Gateway")
		if err == nil || !strings.Contains(err.Error(), `"networking.istio.io"`) || !strings.Contains(err.Error(), `"gateway.networking.k8s.io"`) {
			t.Fatalf("ambiguous Gateway: %v", err)
		}
		if r, err := findResource(s, "networking.istio.io", "Gateway"); err != nil || r.Group != "networking.istio.io" {
			t.Fatalf("Gateway with group: %+v %v", r, err)
		}
		if _, err := findResource(s, "", "nodes"); err == nil || !strings.Contains(err.Error(), `"core"`) {
			t.Fatalf("ambiguous plural must list the core group: %v", err)
		}
		if r, err := findResource(s, "core", "Node"); err != nil || r.Group != "" || r.Namespaced {
			t.Fatalf("core Node: %+v %v", r, err)
		}
		if r, err := findResource(s, "", "deploy"); err != nil || r.Kind != "Deployment" {
			t.Fatalf("unique short name: %+v %v", r, err)
		}
		if _, err := findResource(s, "", "Widget"); err == nil || !strings.Contains(err.Error(), "unknown kind") {
			t.Fatalf("unknown kind: %v", err)
		}
	}
}
