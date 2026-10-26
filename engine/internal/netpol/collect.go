package netpol

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Load builds a World with the pods and policies of the given namespaces and the labels of every
// namespace the user may list (namespace selectors need them). Errors are reported per resource.
func Load(ctx context.Context, sess *cluster.Session, namespaces ...string) (*World, map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	w := &World{Namespaces: map[string]map[string]string{}}
	errs := map[string]string{}
	list := func(group, resource, ns string) []map[string]any {
		r, ok := sess.Lookup(group, resource)
		if !ok {
			return nil
		}
		objs, err := sess.Informers.List(ctx, cluster.Key{GVR: r.GVR(), Namespace: ns})
		if err != nil {
			errs[resource] = err.Error()
			return nil
		}
		out := make([]map[string]any, 0, len(objs))
		for _, o := range objs {
			out = append(out, o.Object)
		}
		return out
	}
	seen := map[string]bool{}
	for _, ns := range namespaces {
		if seen[ns] {
			continue
		}
		seen[ns] = true
		for _, o := range list("", "pods", ns) {
			var p corev1.Pod
			if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &p) == nil {
				w.Pods = append(w.Pods, p)
			}
		}
		for _, o := range list("networking.k8s.io", "networkpolicies", ns) {
			var np networkingv1.NetworkPolicy
			if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &np) == nil {
				w.Policies = append(w.Policies, np)
			}
		}
		w.Namespaces[ns] = map[string]string{}
	}
	for _, o := range list("", "namespaces", "") {
		var n corev1.Namespace
		if runtime.DefaultUnstructuredConverter.FromUnstructured(o, &n) == nil {
			w.Namespaces[n.Name] = n.Labels
		}
	}
	return w, errs
}

// Find returns the pod by namespace/name.
func (w *World) Find(ns, name string) *corev1.Pod { return w.podByName(ns, name) }
