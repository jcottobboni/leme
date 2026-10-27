package ops

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/redact"
)

// Annotations that may embed secret material are masked, descriptive ones stay, and a Secret with
// a masked annotation cannot be applied back.
func TestMaskSecretAnnotations(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "annotations": map[string]any{
			"kapp.k14s.io/original":              `{"kind":"Secret","data":{"pw":"cHc="}}`,
			"openshift.io/token-secret.value":    "eyJhbGciOi",
			"kubernetes.io/service-account.name": "builder",
			"meta.helm.sh/release-name":          "app",
		}},
		"data": map[string]any{"pw": "cHc="}}}
	if !MaskSecret(u) {
		t.Fatal("nothing masked")
	}
	a := u.GetAnnotations()
	if !redact.IsMask(a["kapp.k14s.io/original"]) || !redact.IsMask(a["openshift.io/token-secret.value"]) {
		t.Fatalf("secret-bearing annotations visible: %v", a)
	}
	if a["kubernetes.io/service-account.name"] != "builder" || a["meta.helm.sh/release-name"] != "app" {
		t.Fatalf("descriptive annotations masked: %v", a)
	}
	u2 := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "annotations": map[string]any{"x": redact.Mask}}}}
	if !containsHidden(u2) {
		t.Fatal("a masked annotation must block applying the Secret")
	}
}
