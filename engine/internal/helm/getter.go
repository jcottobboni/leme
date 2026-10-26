// Package helm runs Helm SDK actions pinned to a session's kubeconfig context.
package helm

import (
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// discoCache is the per-session discovery state shared by every Helm action.
type discoCache struct {
	once   sync.Once
	dc     discovery.CachedDiscoveryInterface
	mapper meta.RESTMapper
	err    error
}

func (d *discoCache) init(cfg *rest.Config) {
	d.once.Do(func() {
		c, err := discovery.NewDiscoveryClientForConfig(rest.CopyConfig(cfg))
		if err != nil {
			d.err = err
			return
		}
		d.dc = memory.NewMemCacheClient(c)
		d.mapper = restmapper.NewShortcutExpander(restmapper.NewDeferredDiscoveryRESTMapper(d.dc), d.dc, nil)
	})
}

// restGetter satisfies genericclioptions.RESTClientGetter from an existing rest.Config, so Helm
// never reads the user's current-context.
type restGetter struct {
	cfg   *rest.Config
	cc    clientcmd.ClientConfig
	disco *discoCache
}

func (g *restGetter) ToRESTConfig() (*rest.Config, error) { return rest.CopyConfig(g.cfg), nil }

func (g *restGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	g.disco.init(g.cfg)
	return g.disco.dc, g.disco.err
}

func (g *restGetter) ToRESTMapper() (meta.RESTMapper, error) {
	g.disco.init(g.cfg)
	return g.disco.mapper, g.disco.err
}

func (g *restGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig { return g.cc }
