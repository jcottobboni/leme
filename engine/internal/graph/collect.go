package graph

import (
	"context"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

var namespaced = []string{"/pods", "apps/replicasets", "apps/deployments", "apps/statefulsets", "apps/daemonsets", "batch/jobs", "batch/cronjobs",
	"/services", "networking.k8s.io/ingresses", "gateway.networking.k8s.io/httproutes", "route.openshift.io/routes",
	"/replicationcontrollers", "apps.openshift.io/deploymentconfigs", "build.openshift.io/buildconfigs", "build.openshift.io/builds", "image.openshift.io/imagestreams",
	"/persistentvolumeclaims", "autoscaling/horizontalpodautoscalers", "/serviceaccounts", "rbac.authorization.k8s.io/rolebindings"}

// Collect lists what Build needs from the session caches. ConfigMaps and Secrets are listed as
// metadata only, so their contents never enter memory. Failures are reported per resource.
func Collect(ctx context.Context, sess *cluster.Session, ns string) (Input, map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	in := Input{Namespace: ns, Objects: map[string][]*unstructured.Unstructured{}}
	errs := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	list := func(key string, namespace string, set func([]*unstructured.Unstructured)) {
		group, resource, _ := strings.Cut(key, "/")
		res, ok := sess.Lookup(group, resource)
		if !ok {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			objs, err := sess.Informers.List(ctx, cluster.Key{GVR: res.GVR(), Namespace: namespace})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[key] = err.Error()
				return
			}
			set(objs)
		}()
	}
	for _, key := range namespaced {
		list(key, ns, func(o []*unstructured.Unstructured) { in.Objects[key] = o })
	}
	list("/nodes", "", func(o []*unstructured.Unstructured) { in.Nodes = o })
	names := func(resource string, set func(map[string]bool)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := sess.Meta.Resource(schema.GroupVersionResource{Version: "v1", Resource: resource}).Namespace(ns).List(ctx, metav1.ListOptions{})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs["/"+resource] = err.Error()
				return
			}
			m := make(map[string]bool, len(l.Items))
			for _, it := range l.Items {
				m[it.Name] = true
			}
			set(m)
		}()
	}
	names("configmaps", func(m map[string]bool) { in.ConfigMaps = m })
	names("secrets", func(m map[string]bool) { in.Secrets = m })
	wg.Wait()
	return in, errs
}

// ForObject builds the topology around one object of the namespace.
func ForObject(ctx context.Context, sess *cluster.Session, kind, ns, name string) *Graph {
	in, errs := Collect(ctx, sess, ns)
	root := id(kind, ns, name)
	if ns == "" {
		root = id(kind, "", name)
	}
	g := Aggregate(Neighborhood(Build(in), root, 8), root)
	g.Root = root
	if len(errs) > 0 {
		g.Errors = errs
	}
	return Cap(g)
}

// ForNamespace builds the topology of a whole namespace.
func ForNamespace(ctx context.Context, sess *cluster.Session, ns string) *Graph {
	in, errs := Collect(ctx, sess, ns)
	g := Aggregate(Build(in), "")
	if len(errs) > 0 {
		g.Errors = errs
	}
	return Cap(g)
}
