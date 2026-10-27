package istio

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Istio API groups and the kinds the panel reads. The version is whatever discovery prefers
// (security.istio.io v1/v1beta1, networking.istio.io v1/v1beta1/v1alpha3).
const (
	SecurityGroup   = "security.istio.io"
	NetworkingGroup = "networking.istio.io"
)

var panelKinds = []struct{ group, kind string }{
	{SecurityGroup, "PeerAuthentication"},
	{NetworkingGroup, "VirtualService"},
	{NetworkingGroup, "DestinationRule"},
	{NetworkingGroup, "Gateway"},
}

// Lister lists the objects of a resource in a namespace ("" for all).
type Lister func(ctx context.Context, r cluster.Resource, ns string) ([]*unstructured.Unstructured, error)

// InformerLister reads through the session's shared caches.
func InformerLister(sess *cluster.Session) Lister {
	return func(ctx context.Context, r cluster.Resource, ns string) ([]*unstructured.Unstructured, error) {
		return sess.Informers.List(ctx, cluster.Key{GVR: r.GVR(), Namespace: ns})
	}
}

// Lookup is the part of a session the collector needs.
type Lookup interface {
	LookupKind(group, kind string) (cluster.Resource, bool)
	Lookup(group, resource string) (cluster.Resource, bool)
}

// Installed reports which Istio kinds discovery knows, as "group/version Kind".
func Installed(l Lookup) []string {
	out := []string{}
	for _, k := range panelKinds {
		if r, ok := l.LookupKind(k.group, k.kind); ok {
			out = append(out, r.Group+"/"+r.Version+" "+r.Kind)
		}
	}
	return out
}

// Load builds the report for namespace ns ("" = every namespace). Without Istio CRDs it returns
// Installed=false and lists nothing. Errors are reported per kind.
func Load(ctx context.Context, l Lookup, list Lister, ns string) Report {
	rep := Report{Kinds: Installed(l), RootNamespace: DefaultRootNamespace, Namespaces: []NamespaceMTLS{},
		VirtualServices: []VirtualService{}, DestinationRules: []DestinationRule{}, Gateways: []Gateway{}}
	if len(rep.Kinds) == 0 {
		return rep
	}
	rep.Installed = true
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	errs := map[string]string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	got := map[string][]*unstructured.Unstructured{}
	fetch := func(key string, r cluster.Resource, ok bool, namespaces ...string) {
		if !ok {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var all []*unstructured.Unstructured
			for _, n := range namespaces {
				if !r.Namespaced {
					n = ""
				}
				objs, err := list(ctx, r, n)
				if err != nil {
					mu.Lock()
					errs[key] = err.Error()
					mu.Unlock()
					return
				}
				all = append(all, objs...)
			}
			mu.Lock()
			got[key] = all
			mu.Unlock()
		}()
	}
	pa, paOK := l.LookupKind(SecurityGroup, "PeerAuthentication")
	if ns == "" {
		fetch("peerauthentications", pa, paOK, "")
	} else if ns == rep.RootNamespace {
		fetch("peerauthentications", pa, paOK, ns)
	} else {
		// The mesh-wide policy lives in the root namespace.
		fetch("peerauthentications", pa, paOK, rep.RootNamespace, ns)
	}
	for _, k := range panelKinds[1:] {
		r, ok := l.LookupKind(k.group, k.kind)
		fetch(strings.ToLower(k.kind)+"s", r, ok, ns)
	}
	svc, svcOK := l.Lookup("", "services")
	// Hosts may point to Services of other namespaces, so the index is cluster-wide.
	fetch("services", svc, svcOK, "")
	nsr, nsOK := l.Lookup("", "namespaces")
	fetch("namespaces", nsr, nsOK, "")
	wg.Wait()

	namespaces := got["namespaces"]
	if ns != "" {
		namespaces = nil
		for _, n := range got["namespaces"] {
			if n.GetName() == ns {
				namespaces = append(namespaces, n)
			}
		}
		if len(namespaces) == 0 { // no permission to list namespaces: still resolve the one asked
			u := &unstructured.Unstructured{Object: map[string]any{}}
			u.SetName(ns)
			namespaces = append(namespaces, u)
		}
	}
	rep.Namespaces = ResolveMTLS(namespaces, got["peerauthentications"], rep.RootNamespace)
	svcs := NewServices(got["services"])
	for _, u := range sorted(got["virtualservices"]) {
		rep.VirtualServices = append(rep.VirtualServices, ParseVirtualService(u, svcs))
	}
	for _, u := range sorted(got["destinationrules"]) {
		rep.DestinationRules = append(rep.DestinationRules, ParseDestinationRule(u, svcs))
	}
	for _, u := range sorted(got["gateways"]) {
		rep.Gateways = append(rep.Gateways, ParseGateway(u))
	}
	if len(errs) > 0 {
		rep.Errors = errs
	}
	return rep
}

func sorted(list []*unstructured.Unstructured) []*unstructured.Unstructured {
	out := append([]*unstructured.Unstructured(nil), list...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].GetNamespace() != out[j].GetNamespace() {
			return out[i].GetNamespace() < out[j].GetNamespace()
		}
		return out[i].GetName() < out[j].GetName()
	})
	return out
}
