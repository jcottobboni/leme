package api

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func TestPrinterForCRD(t *testing.T) {
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": "widgets.example.io"},
		"spec": map[string]any{"group": "example.io", "versions": []any{map[string]any{
			"name": "v1", "served": true,
			"additionalPrinterColumns": []any{map[string]any{"name": "Phase", "type": "string", "jsonPath": ".status.phase"}},
		}}},
	}}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[k8sschema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"}, crd)
	widgets := cluster.Resource{Group: "example.io", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true}
	gadgets := cluster.Resource{Group: "example.io", Version: "v1", Resource: "gadgets", Kind: "Gadget", Namespaced: true}
	pods := cluster.Resource{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	sess := cluster.NewStaticSession(kube.Context{Name: "t"}, nil, dyn, []cluster.Resource{widgets, gadgets, pods}, true)
	ctx := context.Background()

	p := printerFor(ctx, sess, widgets)
	if p == nil || len(p.Columns()) != 4 || p.Columns()[2].Key != "pc:Phase" {
		t.Fatalf("widget printer: %+v", p)
	}
	if printerFor(ctx, sess, gadgets) != nil {
		t.Error("CRD not readable: generic columns")
	}
	if printerFor(ctx, sess, pods) != nil {
		t.Error("built-in kinds keep their own columns")
	}
	// Served from the session cache: the CRD is not read again.
	before := len(dyn.Actions())
	if printerFor(ctx, sess, widgets) != p || len(dyn.Actions()) != before {
		t.Error("printer not cached")
	}
}
