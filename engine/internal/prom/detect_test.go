package prom

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

// A cluster without any Prometheus Service must yield an empty list, not null: the settings
// dialog read its length and the whole window went blank.
func TestDetectNothingIsEmptyList(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "demo"}})
	s := cluster.NewStaticSession(kube.Context{Name: "c"}, cs, nil, nil, true)
	got, err := Detect(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if string(b) != "[]" {
		t.Fatalf("want [], got %s", b)
	}
}
