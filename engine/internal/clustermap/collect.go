package clustermap

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Collect lists from the session informer caches, cluster-wide; when that is forbidden it falls back to
// fallbackNS (scope = that namespace), like GetOverview.
func Collect(ctx context.Context, s *cluster.Session, fallbackNS string) *ClusterMap {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	in := Input{Scope: "cluster", Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	fellBack := false
	list := func(group, resource string, dst *[]*unstructured.Unstructured) {
		r, ok := s.Lookup(group, resource)
		if !ok {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			objs, err := s.Informers.List(ctx, cluster.Key{GVR: r.GVR()})
			if err != nil && fallbackNS != "" && resource != "namespaces" {
				objs, err = s.Informers.List(ctx, cluster.Key{GVR: r.GVR(), Namespace: fallbackNS})
				if err == nil {
					mu.Lock()
					fellBack = true
					mu.Unlock()
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				in.Errors[resource] = err.Error()
				return
			}
			*dst = objs
		}()
	}
	list("", "namespaces", &in.Namespaces)
	list("", "pods", &in.Pods)
	list("apps", "replicasets", &in.ReplicaSets)
	list("", "replicationcontrollers", &in.ReplicationControllers)
	list("apps", "deployments", &in.Deployments)
	list("apps", "statefulsets", &in.StatefulSets)
	list("apps", "daemonsets", &in.DaemonSets)
	list("batch", "jobs", &in.Jobs)
	list("batch", "cronjobs", &in.CronJobs)
	list("apps.openshift.io", "deploymentconfigs", &in.DeployConfs)
	list("", "services", &in.Services)
	list("networking.k8s.io", "ingresses", &in.Ingresses)
	list("", "persistentvolumeclaims", &in.PVCs)
	wg.Wait()
	if fellBack {
		in.Scope = fallbackNS
		if len(in.Namespaces) == 0 { // namespaces cannot be listed: the scoped namespace still gets its island
			in.Namespaces = []*unstructured.Unstructured{{Object: map[string]any{"metadata": map[string]any{"name": fallbackNS}}}}
		}
	}
	if len(in.Errors) == 0 {
		in.Errors = nil
	}
	return Build(in)
}
