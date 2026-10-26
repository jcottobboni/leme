package ops

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Usage is CPU (millicores) and memory (bytes).
type Usage struct {
	CPU    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
}

// PodUsage returns usage keyed by "namespace/name", summed across containers.
func PodUsage(ctx context.Context, s *cluster.Session, namespace string) (map[string]Usage, error) {
	l, err := s.Metrics.MetricsV1beta1().PodMetricses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]Usage, len(l.Items))
	for _, pm := range l.Items {
		var u Usage
		for _, c := range pm.Containers {
			u.CPU += c.Usage.Cpu().MilliValue()
			u.Memory += c.Usage.Memory().Value()
		}
		out[pm.Namespace+"/"+pm.Name] = u
	}
	return out, nil
}

// NodeUsage returns usage keyed by node name.
func NodeUsage(ctx context.Context, s *cluster.Session) (map[string]Usage, error) {
	l, err := s.Metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]Usage, len(l.Items))
	for _, nm := range l.Items {
		out[nm.Name] = Usage{CPU: nm.Usage.Cpu().MilliValue(), Memory: nm.Usage.Memory().Value()}
	}
	return out, nil
}
