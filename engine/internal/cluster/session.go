// Package cluster holds per-context connections, discovery and shared caches.
package cluster

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

// ErrReadOnly is returned for mutations on a read-only (production-guarded) cluster.
var ErrReadOnly = errors.New("cluster is read-only: unlock it explicitly before changing resources")

// Resource is one listable API resource returned by discovery.
type Resource struct {
	Group      string   `json:"group"`
	Version    string   `json:"version"`
	Resource   string   `json:"resource"`
	Kind       string   `json:"kind"`
	Namespaced bool     `json:"namespaced"`
	Category   string   `json:"category"`
	Verbs      []string `json:"verbs"`
	ShortNames []string `json:"shortNames,omitempty"`
}

// GVR returns the schema identifier.
func (r Resource) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource}
}

// Session is a live connection to one context.
type Session struct {
	Context   kube.Context
	Config    *rest.Config
	Namespace string
	Typed     kubernetes.Interface
	Dynamic   dynamic.Interface
	Metrics   metricsclient.Interface
	Meta      metadata.Interface
	Informers *InformerManager

	loader    *kube.Loader
	mu        sync.RWMutex
	readOnly  bool
	resources []Resource
	byKey     map[string]Resource

	// life ends on Disconnect; write ends whenever the session is (re)locked.
	life     context.Context
	endLife  context.CancelFunc
	write    context.Context
	endWrite context.CancelFunc
}

// ClientConfig returns the kubeconfig view of this session's context with a default namespace.
func (s *Session) ClientConfig(namespace string) clientcmd.ClientConfig {
	return s.loader.ClientConfig(s.Context.Name, namespace)
}

// Life is cancelled when the session is disconnected. Long-lived streams derive from it.
func (s *Session) Life() context.Context { return s.life }

// WriteScope is cancelled when the session is locked or disconnected. Interactive
// write-capable streams (exec, port-forward) derive from it so re-locking ends them.
func (s *Session) WriteScope() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.write
}

// ReadOnly reports whether mutations are blocked.
func (s *Session) ReadOnly() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readOnly
}

// SetReadOnly toggles the guard. Locking ends every write-capable stream.
func (s *Session) SetReadOnly(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v && !s.readOnly {
		s.endWrite()
		s.write, s.endWrite = context.WithCancel(s.life)
	}
	s.readOnly = v
}

// CheckWritable returns ErrReadOnly when the guard is active.
func (s *Session) CheckWritable() error {
	if s.ReadOnly() {
		return ErrReadOnly
	}
	return nil
}

// NewStaticSession builds a session from ready clients and a fixed resource list, without
// discovery or informers. It is meant for tests and for short-lived tools (e.g. the MCP server)
// that reuse the engine's packages with their own clients.
func NewStaticSession(kc kube.Context, typed kubernetes.Interface, dyn dynamic.Interface, resources []Resource, readOnly bool) *Session {
	life, endLife := context.WithCancel(context.Background())
	write, endWrite := context.WithCancel(life)
	s := &Session{Context: kc, Typed: typed, Dynamic: dyn, readOnly: readOnly, resources: resources, byKey: map[string]Resource{},
		life: life, endLife: endLife, write: write, endWrite: endWrite}
	for _, r := range resources {
		s.byKey[r.Group+"/"+r.Resource] = r
	}
	return s
}

// Resources returns the discovered resources.
func (s *Session) Resources() []Resource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Resource(nil), s.resources...)
}

// Lookup finds a resource by group and plural name.
func (s *Session) Lookup(group, resource string) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.byKey[group+"/"+resource]
	return r, ok
}

// LookupKind finds a resource by group and kind.
func (s *Session) LookupKind(group, kind string) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.resources {
		if r.Group == group && r.Kind == kind {
			return r, true
		}
	}
	return Resource{}, false
}

// Manager owns all sessions.
type Manager struct {
	loader   *kube.Loader
	mu       sync.Mutex
	sessions map[string]*Session
	// tokens obtained by an in-app OpenShift login, per context id. Memory only: never written to disk.
	tokens map[string]string
}

// NewManager creates a manager.
func NewManager(loader *kube.Loader) *Manager {
	return &Manager{loader: loader, sessions: map[string]*Session{}, tokens: map[string]string{}}
}

// Loader exposes the kubeconfig loader.
func (m *Manager) Loader() *kube.Loader { return m.loader }

// Get returns an existing session.
func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

// Connect opens (or reuses) a session for the context id and runs discovery.
func (m *Manager) Connect(ctx context.Context, id string) (*Session, error) {
	if s, ok := m.Get(id); ok {
		return s, nil
	}
	contexts, err := m.loader.Contexts()
	if err != nil {
		return nil, err
	}
	var kc *kube.Context
	for i := range contexts {
		if contexts[i].ID == id {
			kc = &contexts[i]
			break
		}
	}
	if kc == nil {
		return nil, fmt.Errorf("unknown context id %q", id)
	}
	cfg, ns, err := m.loader.RESTConfig(kc.Name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	tok := m.tokens[id]
	m.mu.Unlock()
	if tok != "" {
		cfg = WithToken(cfg, tok)
	}
	// A login (or logout) that lands while we connect must not be overwritten by this session.
	stale := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.tokens[id] != tok
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	meta, err := metadata.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	// Remote clusters start read-only (fail-safe); production additionally needs a typed confirmation to unlock.
	s := &Session{Context: *kc, Config: cfg, Namespace: ns, Typed: typed, Dynamic: dyn, Metrics: mc, Meta: meta, loader: m.loader, readOnly: kc.Prod || !kc.Local}
	s.life, s.endLife = context.WithCancel(context.Background())
	s.write, s.endWrite = context.WithCancel(s.life)
	if err := s.discover(ctx); err != nil {
		s.endLife()
		if tok != "" && apierrors.IsUnauthorized(errors.Unwrap(err)) || tok != "" && strings.Contains(err.Error(), "Unauthorized") {
			// The in-app login expired: fall back to the kubeconfig credentials next time.
			m.ClearToken(id)
		}
		return nil, err
	}
	if stale() {
		s.endLife()
		return nil, errors.New("o login mudou durante a conexão; conecte de novo")
	}
	s.Informers = NewInformerManager(dyn, 2*time.Minute)

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.sessions[id]; ok {
		s.Informers.StopAll()
		s.endLife()
		return existing, nil
	}
	m.sessions[id] = s
	return s, nil
}

// WithToken returns a copy of cfg that authenticates only with the bearer token.
func WithToken(cfg *rest.Config, token string) *rest.Config {
	c := rest.AnonymousClientConfig(cfg)
	c.BearerToken = token
	c.QPS, c.Burst, c.UserAgent, c.Proxy = cfg.QPS, cfg.Burst, cfg.UserAgent, cfg.Proxy
	return c
}

// Resolve returns the context and its client config without connecting (used by the OpenShift login).
func (m *Manager) Resolve(id string) (kube.Context, *rest.Config, error) {
	contexts, err := m.loader.Contexts()
	if err != nil {
		return kube.Context{}, nil, err
	}
	for _, c := range contexts {
		if c.ID == id {
			cfg, _, err := m.loader.RESTConfig(c.Name)
			return c, cfg, err
		}
	}
	return kube.Context{}, nil, fmt.Errorf("unknown context id %q", id)
}

// SetToken stores an in-app login token for the context and drops the current session so the next
// Connect uses it. The token is kept in memory only.
func (m *Manager) SetToken(id, token string) {
	m.mu.Lock()
	m.tokens[id] = token
	m.mu.Unlock()
	m.Disconnect(id)
}

// ClearToken forgets the in-app login (back to the kubeconfig credentials) and drops the session.
func (m *Manager) ClearToken(id string) {
	m.mu.Lock()
	delete(m.tokens, id)
	m.mu.Unlock()
}

// HasToken reports whether the context uses an in-app login token.
func (m *Manager) HasToken(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens[id] != ""
}

// Disconnect stops caches and forgets the session.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		s.endLife()
		if s.Informers != nil {
			s.Informers.StopAll()
		}
	}
}

func (s *Session) discover(ctx context.Context) error {
	// A bounded HTTP timeout keeps an unreachable API server (VPN down) from pinning goroutines.
	dcfg := rest.CopyConfig(s.Config)
	dcfg.Timeout = 30 * time.Second
	dc, err := discovery.NewDiscoveryClientForConfig(dcfg)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	var lists []*metav1.APIResourceList
	var derr error
	go func() {
		defer close(done)
		lists, derr = dc.ServerPreferredResources()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	if derr != nil && len(lists) == 0 {
		return fmt.Errorf("discovery: %w", derr)
	}
	var out []Resource
	for _, l := range lists {
		gv, err := schema.ParseGroupVersion(l.GroupVersion)
		if err != nil {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") || !hasVerbs(r.Verbs, "list", "watch") {
				continue
			}
			out = append(out, Resource{
				Group: gv.Group, Version: gv.Version, Resource: r.Name, Kind: r.Kind,
				Namespaced: r.Namespaced, Category: categorize(gv.Group, r.Kind), Verbs: r.Verbs, ShortNames: r.ShortNames,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Kind < out[j].Kind
	})
	by := map[string]Resource{}
	for _, r := range out {
		by[r.Group+"/"+r.Resource] = r
	}
	s.mu.Lock()
	s.resources, s.byKey = out, by
	s.mu.Unlock()
	return nil
}

func hasVerbs(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, v := range have {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

var categories = map[string]string{
	"Pod": "Workloads", "Deployment": "Workloads", "StatefulSet": "Workloads", "DaemonSet": "Workloads",
	"ReplicaSet": "Workloads", "Job": "Workloads", "CronJob": "Workloads", "ReplicationController": "Workloads",
	"HorizontalPodAutoscaler": "Workloads", "PodDisruptionBudget": "Workloads",
	"DeploymentConfig": "Workloads", "BuildConfig": "Workloads", "Build": "Workloads", "ImageStream": "Workloads",
	"Service": "Network", "Ingress": "Network", "IngressClass": "Network", "Endpoints": "Network",
	"EndpointSlice": "Network", "NetworkPolicy": "Network", "Route": "Network",
	"Gateway": "Network", "GatewayClass": "Network", "HTTPRoute": "Network", "GRPCRoute": "Network", "ReferenceGrant": "Network",
	"ConfigMap": "Config", "Secret": "Config", "ResourceQuota": "Config", "LimitRange": "Config",
	"MutatingWebhookConfiguration": "Config", "ValidatingWebhookConfiguration": "Config",
	"PersistentVolumeClaim": "Storage", "PersistentVolume": "Storage", "StorageClass": "Storage",
	"VolumeAttachment": "Storage", "CSIDriver": "Storage", "CSINode": "Storage", "VolumeSnapshot": "Storage",
	"ServiceAccount": "Access", "Role": "Access", "ClusterRole": "Access", "RoleBinding": "Access", "ClusterRoleBinding": "Access",
	"Node": "Cluster", "Namespace": "Cluster", "Event": "Cluster", "Project": "Cluster",
	"PriorityClass": "Cluster", "RuntimeClass": "Cluster", "Lease": "Cluster",
	"CustomResourceDefinition": "Cluster", "APIService": "Cluster", "ComponentStatus": "Cluster",
}

func categorize(group, kind string) string {
	if !kinds.Builtin(group) {
		return "Custom Resources"
	}
	if c, ok := categories[kind]; ok && !(kind == "Event" && group == "events.k8s.io") {
		return c
	}
	if strings.HasSuffix(group, ".openshift.io") {
		// Built-in OpenShift groups without a curated category stay with the custom resources, as before.
		return "Custom Resources"
	}
	return "Cluster"
}
