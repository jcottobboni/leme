// Package kube loads kubeconfig files and builds REST configs per context.
package kube

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	Local     bool   `json:"local"`
	Provider  string `json:"provider"`
}

// Loader resolves kubeconfig paths and contexts.
type Loader struct {
	paths []string
}

// NewLoader uses $KUBECONFIG (or ~/.kube/config) plus extra paths.
func NewLoader(extra []string) *Loader {
	var paths []string
	if env := os.Getenv("KUBECONFIG"); env != "" {
		paths = append(paths, filepath.SplitList(env)...)
	} else if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".kube", "config"))
	}
	paths = append(paths, extra...)
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return &Loader{paths: out}
}

// Paths returns the kubeconfig files in precedence order.
func (l *Loader) Paths() []string { return append([]string(nil), l.paths...) }

func (l *Loader) rules() *clientcmd.ClientConfigLoadingRules {
	var existing []string
	for _, p := range l.paths {
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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

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
var prodPattern = regexp.MustCompile(`(?i)(^|[^a-z])(prd|prod|production|producao|produção|produçao|live)([^a-z]|$)`)

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

var localContextPrefixes = []string{"kind-", "k3d-", "minikube", "docker-desktop", "rancher-desktop", "orbstack", "colima"}

// IsLocal reports clusters that clearly run on this machine. Only these start writable;
// everything else starts read-only (fail-safe), and production needs a typed confirmation to unlock.
func IsLocal(contextName, server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "127.0.0.1" || h == "localhost" || h == "::1" || h == "host.docker.internal" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	n := strings.ToLower(contextName)
	for _, p := range localContextPrefixes {
		if strings.HasPrefix(n, p) && (h == "" || strings.HasPrefix(h, "192.168.") || strings.HasPrefix(h, "172.")) {
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
