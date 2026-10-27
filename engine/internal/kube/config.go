// Package kube loads kubeconfig files and builds REST configs per context.
package kube

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Context describes one kubeconfig context as shown to the UI.
type Context struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	User      string `json:"user"`
	Namespace string `json:"namespace,omitempty"`
	Server    string `json:"server"`
	Source    string `json:"source"`
	Current   bool   `json:"current"`
	Prod      bool   `json:"prod"`
	// SharedProd marks a context that is production only because a production context uses the
	// same API server: unlocking it would unlock that cluster for the same identity.
	SharedProd bool   `json:"sharedProd,omitempty"`
	Local      bool   `json:"local"`
	Provider   string `json:"provider"`
}

// Fingerprint identifies what the context points to (server, cluster and user entries), so state
// keyed by context name can tell when the name now means another cluster or identity.
func (c Context) Fingerprint() string {
	h := sha256.Sum256([]byte(c.Server + "\x00" + c.Cluster + "\x00" + c.User))
	return hex.EncodeToString(h[:12])
}

// Loader resolves kubeconfig paths and contexts.
type Loader struct {
	base []string // $KUBECONFIG (or ~/.kube/config) plus --kubeconfig flags, fixed at start

	mu      sync.RWMutex
	sources []string // files or folders the user added in the app (settings.json)
	scanned map[string]scan
}

// scan caches a folder expansion: Contexts, RESTConfig and every cluster list read the paths, and
// walking a folder each time would turn a large folder into a cost on every request.
type scan struct {
	files []string
	at    time.Time
}

const scanTTL = 5 * time.Second

// NewLoader uses $KUBECONFIG (or ~/.kube/config) plus extra paths.
func NewLoader(extra []string) *Loader {
	var paths []string
	if env := os.Getenv("KUBECONFIG"); env != "" {
		paths = append(paths, filepath.SplitList(env)...)
	} else if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".kube", "config"))
	}
	paths = append(paths, extra...)
	return &Loader{base: dedupe(paths)}
}

func dedupe(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Base returns the kubeconfig paths that come from the environment and the command line.
func (l *Loader) Base() []string { return append([]string(nil), l.base...) }

// Sources returns the files and folders added in the app.
func (l *Loader) Sources() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.sources...)
}

// SetSources replaces the files and folders added in the app. Folders are scanned on every read,
// so kubeconfigs dropped into them later show up on the next reload.
func (l *Loader) SetSources(paths []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sources = append([]string(nil), paths...)
	l.scanned = nil
}

// Paths returns the kubeconfig files in precedence order: the base paths first, then the files
// added in the app, with folders expanded.
func (l *Loader) Paths() []string {
	paths := l.Base()
	for _, s := range l.Sources() {
		paths = append(paths, l.expand(s)...)
	}
	return dedupe(paths)
}

func (l *Loader) expand(src string) []string {
	l.mu.RLock()
	c, ok := l.scanned[src]
	l.mu.RUnlock()
	if ok && time.Since(c.at) < scanTTL {
		return c.files
	}
	files := ExpandSource(src)
	l.mu.Lock()
	if l.scanned == nil {
		l.scanned = map[string]scan{}
	}
	l.scanned[src] = scan{files: files, at: time.Now()}
	l.mu.Unlock()
	return files
}

// A folder scan stops after maxSourceFiles kubeconfigs, maxScanEntries entries or maxScanDepth
// levels, so picking a large folder by mistake (home, /) cannot stall the engine.
const (
	maxSourceFiles = 200
	maxScanEntries = 5000
	maxScanDepth   = 4
)

// ExpandSource lists the kubeconfig files a source stands for: the file itself, or for a folder,
// every config, *.yaml, *.yml and *.kubeconfig file under it (sorted). Hidden entries and
// backups (*.bak*, *~) are skipped. A missing path yields nothing.
func ExpandSource(path string) []string {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		return []string{path}
	}
	var out []string
	seen := 0
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > maxScanEntries {
			return filepath.SkipAll
		}
		if d.IsDir() && p != path {
			if rel, _ := filepath.Rel(path, p); strings.Count(rel, string(filepath.Separator)) >= maxScanDepth-1 {
				return filepath.SkipDir
			}
		}
		name := d.Name()
		if p != path && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !isKubeconfigName(name) {
			return nil
		}
		if len(out) >= maxSourceFiles {
			return filepath.SkipAll
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out
}

func isKubeconfigName(name string) bool {
	n := strings.ToLower(name)
	if strings.Contains(n, ".bak") || strings.HasSuffix(n, "~") {
		return false
	}
	switch filepath.Ext(n) {
	case ".yaml", ".yml", ".kubeconfig":
		return true
	}
	return n == "config"
}

func (l *Loader) rules() *clientcmd.ClientConfigLoadingRules {
	var existing []string
	for _, p := range l.Paths() {
		if _, err := os.Stat(p); err == nil {
			existing = append(existing, p)
		}
	}
	return &clientcmd.ClientConfigLoadingRules{Precedence: existing}
}

// Contexts lists every context across the kubeconfig files. It never contacts a cluster.
func (l *Loader) Contexts() ([]Context, error) {
	raw, err := l.rules().Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	var out []Context
	for name, c := range raw.Contexts {
		server := ""
		if cl, ok := raw.Clusters[c.Cluster]; ok {
			server = cl.Server
		}
		out = append(out, Context{
			ID:        ContextID(name),
			Name:      name,
			Cluster:   c.Cluster,
			User:      c.AuthInfo,
			Namespace: c.Namespace,
			Server:    server,
			Source:    c.LocationOfOrigin,
			Current:   name == raw.CurrentContext,
			Prod:      IsProduction(server, name, c.Cluster, c.AuthInfo, c.Namespace),
			Local:     IsLocal(name, server),
			Provider:  DetectProvider(server, raw.AuthInfos[c.AuthInfo]),
		})
	}
	markSharedProd(out)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// markSharedProd treats a context as production when another context on the same API server is
// production: stg and prd namespaces often live on one cluster, and the read-only guard covers
// the whole session, not a namespace.
func markSharedProd(ctxs []Context) {
	prodServers := map[string]bool{}
	for _, c := range ctxs {
		if c.Prod && c.Server != "" {
			prodServers[serverKey(c.Server)] = true
		}
	}
	for i := range ctxs {
		if !ctxs[i].Prod && ctxs[i].Server != "" && prodServers[serverKey(ctxs[i].Server)] {
			ctxs[i].Prod, ctxs[i].SharedProd = true, true
		}
	}
}

func serverKey(server string) string { return strings.TrimRight(strings.ToLower(server), "/") }

// RESTConfig builds a client config pinned to the given context, ignoring current-context.
func (l *Loader) RESTConfig(contextName string) (*rest.Config, string, error) {
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(l.rules(), &clientcmd.ConfigOverrides{CurrentContext: contextName})
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("context %q: %w", contextName, err)
	}
	ns, _, _ := cc.Namespace()
	cfg.QPS = 50
	cfg.Burst = 100
	cfg.UserAgent = "leme/0.3"
	return cfg, ns, nil
}

// ClientConfig returns a kubeconfig view pinned to contextName with the given default namespace,
// for libraries (Helm) that need a clientcmd.ClientConfig rather than a rest.Config.
func (l *Loader) ClientConfig(contextName, namespace string) clientcmd.ClientConfig {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(l.rules(), &clientcmd.ConfigOverrides{
		CurrentContext: contextName,
		Context:        clientcmdapi.Context{Namespace: namespace},
	})
}

// ContextID is a stable, URL-safe identifier for a context name.
func ContextID(name string) string {
	sum := sha1.Sum([]byte(name))
	return hex.EncodeToString(sum[:])[:12]
}

// prodPattern matches prod markers as whole words; a trailing digit still counts (prod01, prd2).
// The short markers prd and prod also count at the end of a word (ocpprd, k8sprod01), since
// cluster names often glue them to a prefix; a following letter still rules them out (product, sprdx).
var prodPattern = regexp.MustCompile(`(?i)(^|[^a-z])(production|producao|produção|produçao|produccion|producción|live)([^a-z]|$)|(prd|prod)([^a-z]|$)`)

// IsProduction flags a context as production when the API server host or any of the
// given names (context, cluster, user, namespace) carries a production marker.
// The server host matters because a non-prod namespace may live on a prod cluster.
func IsProduction(server string, names ...string) bool {
	if u, err := url.Parse(server); err == nil && prodPattern.MatchString(u.Hostname()) {
		return true
	}
	for _, n := range names {
		if prodPattern.MatchString(n) {
			return true
		}
	}
	return false
}

var localContextPrefixes = []string{"kind-", "k3d-", "minikube", "docker-desktop", "rancher-desktop", "orbstack", "colima", "microk8s"}

// localNets are the private ranges local VM and container drivers (minikube, colima, Docker) use.
var localNets = []*net.IPNet{mustCIDR("172.16.0.0/12"), mustCIDR("192.168.0.0/16")}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// IsLocal reports clusters that clearly run on this machine. Only these start writable;
// everything else starts read-only (fail-safe), and production needs a typed confirmation to unlock.
// A loopback address alone is not enough: SSH tunnels and `kubectl proxy` expose remote clusters
// on 127.0.0.1, so the context name must also come from a local cluster tool.
func IsLocal(contextName, server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	n := strings.ToLower(contextName)
	named := false
	for _, p := range localContextPrefixes {
		if strings.HasPrefix(n, p) {
			named = true
			break
		}
	}
	if !named {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "" || h == "localhost" || h == "host.docker.internal" || h == "kubernetes.docker.internal" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, ln := range localNets {
		if ln.Contains(ip) {
			return true
		}
	}
	return false
}

// DetectProvider guesses the platform from the server URL and auth plugin.
func DetectProvider(server string, auth *clientcmdapi.AuthInfo) string {
	s := strings.ToLower(server)
	switch {
	case strings.Contains(s, ".eks.amazonaws.com"):
		return "eks"
	case strings.Contains(s, "azmk8s.io"):
		return "aks"
	case strings.Contains(s, "api-ocp") || strings.Contains(s, "openshift") || looksLikeOpenShiftAPI(s):
		return "openshift"
	case strings.Contains(s, "127.0.0.1") || strings.Contains(s, "localhost"):
		return "local"
	}
	if auth != nil && auth.Exec != nil {
		cmd := strings.ToLower(filepath.Base(auth.Exec.Command))
		switch {
		case strings.Contains(cmd, "gke-gcloud"):
			return "gke"
		case strings.Contains(cmd, "aws"):
			return "eks"
		case strings.Contains(cmd, "kubelogin"):
			return "aks"
		}
	}
	return "kubernetes"
}

// looksLikeOpenShiftAPI matches the conventional https://api.<cluster>.<domain>:6443 endpoint.
func looksLikeOpenShiftAPI(server string) bool {
	u, err := url.Parse(server)
	return err == nil && strings.HasPrefix(u.Hostname(), "api.") && u.Port() == "6443"
}
