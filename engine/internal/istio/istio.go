// Package istio reads the Istio configuration of a cluster (mTLS, routing, gateways) without any
// Istio controller or istioctl, and optionally runs `istioctl analyze` on an offline export.
package istio

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// DefaultRootNamespace is Istio's default mesh root namespace (meshConfig.rootNamespace).
const DefaultRootNamespace = "istio-system"

// mTLS modes as PeerAuthentication defines them. UNSET means "inherit from the parent".
const (
	Strict     = "STRICT"
	Permissive = "PERMISSIVE"
	Disable    = "DISABLE"
	Unset      = "UNSET"
)

// Where the effective namespace mode comes from.
const (
	SourceNamespace = "namespace" // a PeerAuthentication without selector in the namespace
	SourceMesh      = "mesh"      // a PeerAuthentication without selector in the root namespace
	SourceDefault   = "default"   // nothing set: Istio accepts both plaintext and mTLS (PERMISSIVE)
)

// Report is the Istio panel view of a cluster or namespace.
type Report struct {
	Installed        bool              `json:"installed"`
	Kinds            []string          `json:"kinds"`
	RootNamespace    string            `json:"rootNamespace"`
	Namespaces       []NamespaceMTLS   `json:"namespaces"`
	VirtualServices  []VirtualService  `json:"virtualServices"`
	DestinationRules []DestinationRule `json:"destinationRules"`
	Gateways         []Gateway         `json:"gateways"`
	Errors           map[string]string `json:"errors,omitempty"`
}

// NamespaceMTLS is the effective peer authentication of one namespace.
type NamespaceMTLS struct {
	Name       string      `json:"name"`
	MTLS       string      `json:"mtls"`
	Source     string      `json:"source"`
	Policy     string      `json:"policy,omitempty"` // namespace/name of the deciding PeerAuthentication
	Exceptions []Exception `json:"exceptions"`
	Injection  string      `json:"injection"` // enabled, disabled, rev:<name>, ambient or ""
}

// Exception is a workload-scoped PeerAuthentication that overrides the namespace mode.
type Exception struct {
	Policy    string            `json:"policy"`
	Selector  map[string]string `json:"selector"`
	Mode      string            `json:"mode"` // declared mode (UNSET inherits)
	Effective string            `json:"effective"`
	Ports     []PortMode        `json:"ports,omitempty"`
}

// PortMode is a port-level override inside a workload PeerAuthentication.
type PortMode struct {
	Port      int64  `json:"port"`
	Mode      string `json:"mode"`
	Effective string `json:"effective"`
}

// ServiceRef links an Istio host to a Kubernetes Service.
type ServiceRef struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Found     bool   `json:"found"`
}

// Destination is one weighted target of a route.
type Destination struct {
	Host    string     `json:"host"`
	Subset  string     `json:"subset,omitempty"`
	Port    int64      `json:"port,omitempty"`
	Weight  int64      `json:"weight"`
	Service ServiceRef `json:"service"`
}

// Route is one HTTP, TCP or TLS rule of a VirtualService.
type Route struct {
	Name         string        `json:"name,omitempty"`
	Match        []string      `json:"match"` // OR-ed conditions, each one human readable
	Destinations []Destination `json:"destinations"`
	Notes        []string      `json:"notes,omitempty"` // redirect, rewrite, timeout, fault, mirror…
}

// VirtualService summarizes the routing of a VirtualService.
type VirtualService struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Hosts     []string `json:"hosts"`
	Gateways  []string `json:"gateways"`
	HTTP      []Route  `json:"http"`
	TCP       []Route  `json:"tcp"`
	TLS       []Route  `json:"tls"`
}

// Subset is a named version of a destination.
type Subset struct {
	Name    string            `json:"name"`
	Labels  map[string]string `json:"labels"`
	TLSMode string            `json:"tlsMode,omitempty"`
}

// DestinationRule summarizes a DestinationRule and the Service it targets.
type DestinationRule struct {
	Namespace string     `json:"namespace"`
	Name      string     `json:"name"`
	Host      string     `json:"host"`
	Service   ServiceRef `json:"service"`
	Subsets   []Subset   `json:"subsets"`
	TLSMode   string     `json:"tlsMode,omitempty"`
}

// Server is one listener of a Gateway.
type Server struct {
	Port     int64    `json:"port"`
	Name     string   `json:"name,omitempty"`
	Protocol string   `json:"protocol"`
	Hosts    []string `json:"hosts"`
	TLSMode  string   `json:"tlsMode,omitempty"`
}

// Gateway summarizes an Istio Gateway (networking.istio.io, not the Gateway API).
type Gateway struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Selector  map[string]string `json:"selector"`
	Servers   []Server          `json:"servers"`
}

// --- mTLS ---

type peerAuth struct {
	ns, name string
	created  time.Time
	selector map[string]string
	mode     string
	ports    []PortMode
}

func readPeerAuth(u *unstructured.Unstructured) peerAuth {
	p := peerAuth{ns: u.GetNamespace(), name: u.GetName(), created: u.GetCreationTimestamp().Time}
	p.selector, _, _ = unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels")
	p.mode = normMode(str(u.Object, "spec", "mtls", "mode"))
	if ports, ok, _ := unstructured.NestedMap(u.Object, "spec", "portLevelMtls"); ok {
		for k, v := range ports {
			var port int64
			if _, err := fmt.Sscan(k, &port); err != nil {
				continue
			}
			m, _ := v.(map[string]any)
			p.ports = append(p.ports, PortMode{Port: port, Mode: normMode(str(m, "mode"))})
		}
		sort.Slice(p.ports, func(i, j int) bool { return p.ports[i].Port < p.ports[j].Port })
	}
	return p
}

func normMode(s string) string {
	switch s = strings.ToUpper(strings.TrimSpace(s)); s {
	case Strict, Permissive, Disable:
		return s
	}
	return Unset
}

// oldest returns the namespace-wide (selector-less) policy Istio honours when several exist: the
// oldest one, then by name.
func oldest(list []peerAuth) *peerAuth {
	var best *peerAuth
	for i := range list {
		p := &list[i]
		if len(p.selector) > 0 {
			continue
		}
		if best == nil || p.created.Before(best.created) || p.created.Equal(best.created) && p.name < best.name {
			best = p
		}
	}
	return best
}

// ResolveMTLS computes the effective mTLS mode of each namespace: the namespace-wide
// PeerAuthentication wins over the mesh-wide one in the root namespace; UNSET inherits; nothing set
// is reported as UNSET with source "default". Workload policies become exceptions.
func ResolveMTLS(namespaces []*unstructured.Unstructured, peerAuths []*unstructured.Unstructured, root string) []NamespaceMTLS {
	if root == "" {
		root = DefaultRootNamespace
	}
	byNS := map[string][]peerAuth{}
	for _, u := range peerAuths {
		p := readPeerAuth(u)
		byNS[p.ns] = append(byNS[p.ns], p)
	}
	mesh := oldest(byNS[root])
	out := make([]NamespaceMTLS, 0, len(namespaces))
	for _, n := range namespaces {
		r := NamespaceMTLS{Name: n.GetName(), MTLS: Unset, Source: SourceDefault, Exceptions: []Exception{}, Injection: injection(n.GetLabels())}
		if p := oldest(byNS[r.Name]); p != nil && p.mode != Unset && r.Name != root {
			r.MTLS, r.Source, r.Policy = p.mode, SourceNamespace, p.ns+"/"+p.name
		} else if mesh != nil && mesh.mode != Unset {
			r.MTLS, r.Source, r.Policy = mesh.mode, SourceMesh, mesh.ns+"/"+mesh.name
		}
		inherited := r.MTLS
		if inherited == Unset {
			inherited = Permissive
		}
		for _, p := range byNS[r.Name] {
			if len(p.selector) == 0 {
				continue
			}
			e := Exception{Policy: p.ns + "/" + p.name, Selector: p.selector, Mode: p.mode, Effective: p.mode}
			if e.Effective == Unset {
				e.Effective = inherited
			}
			for _, pm := range p.ports {
				pm.Effective = pm.Mode
				if pm.Effective == Unset {
					pm.Effective = e.Effective
				}
				e.Ports = append(e.Ports, pm)
			}
			r.Exceptions = append(r.Exceptions, e)
		}
		sort.Slice(r.Exceptions, func(i, j int) bool { return r.Exceptions[i].Policy < r.Exceptions[j].Policy })
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func injection(l map[string]string) string {
	switch {
	case l["istio-injection"] == "enabled":
		return "enabled"
	case l["istio-injection"] == "disabled":
		return "disabled"
	case l["istio.io/rev"] != "":
		return "rev:" + l["istio.io/rev"]
	case l["istio.io/dataplane-mode"] == "ambient":
		return "ambient"
	}
	return ""
}

// --- hosts and Services ---

// Services is the set of known Services as "namespace/name".
type Services map[string]bool

// NewServices indexes Service objects.
func NewServices(list []*unstructured.Unstructured) Services {
	s := Services{}
	for _, u := range list {
		s[u.GetNamespace()+"/"+u.GetName()] = true
	}
	return s
}

// ResolveHost maps an Istio host, written relative to namespace ns, to a Kubernetes Service:
// "reviews" → ns/reviews; "reviews.bookinfo", "reviews.bookinfo.svc[.cluster.local]" → bookinfo/reviews.
// Wildcards and external names (ServiceEntry hosts) are not Services.
func ResolveHost(host, ns string, svcs Services) ServiceRef {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || strings.Contains(host, "*") {
		return ServiceRef{}
	}
	parts := strings.Split(host, ".")
	var ref ServiceRef
	switch {
	case len(parts) == 1:
		ref = ServiceRef{Namespace: ns, Name: parts[0]}
	case len(parts) >= 3 && parts[2] == "svc":
		ref = ServiceRef{Namespace: parts[1], Name: parts[0]}
	case len(parts) == 2:
		// "name.namespace" only when such a Service exists; otherwise it is an external domain.
		if svcs[parts[1]+"/"+parts[0]] {
			return ServiceRef{Namespace: parts[1], Name: parts[0], Found: true}
		}
		return ServiceRef{}
	default:
		return ServiceRef{}
	}
	ref.Found = svcs[ref.Namespace+"/"+ref.Name]
	return ref
}

// --- VirtualService ---

// ParseVirtualService reads hosts, gateways and the HTTP/TCP/TLS routes with their weights.
func ParseVirtualService(u *unstructured.Unstructured, svcs Services) VirtualService {
	ns := u.GetNamespace()
	vs := VirtualService{Namespace: ns, Name: u.GetName(), Hosts: strs(u.Object, "spec", "hosts"), Gateways: strs(u.Object, "spec", "gateways"),
		HTTP: []Route{}, TCP: []Route{}, TLS: []Route{}}
	for _, proto := range []string{"http", "tcp", "tls"} {
		rules, _, _ := unstructured.NestedSlice(u.Object, "spec", proto)
		for _, x := range rules {
			rule, ok := x.(map[string]any)
			if !ok {
				continue
			}
			r := Route{Name: str(rule, "name"), Match: []string{}, Destinations: []Destination{}}
			matches, _ := rule["match"].([]any)
			for _, m := range matches {
				if mm, ok := m.(map[string]any); ok {
					if s := describeMatch(mm); s != "" {
						r.Match = append(r.Match, s)
					}
				}
			}
			routes, _ := rule["route"].([]any)
			for _, d := range routes {
				dm, ok := d.(map[string]any)
				if !ok {
					continue
				}
				host := str(dm, "destination", "host")
				dst := Destination{Host: host, Subset: str(dm, "destination", "subset"), Port: num(dm, "destination", "port", "number"), Weight: num(dm, "weight")}
				dst.Service = ResolveHost(host, ns, svcs)
				r.Destinations = append(r.Destinations, dst)
			}
			// A single destination takes all traffic when no weight is given.
			if len(r.Destinations) == 1 && r.Destinations[0].Weight == 0 {
				r.Destinations[0].Weight = 100
			}
			if proto == "http" {
				r.Notes = httpNotes(rule)
			}
			switch proto {
			case "http":
				vs.HTTP = append(vs.HTTP, r)
			case "tcp":
				vs.TCP = append(vs.TCP, r)
			default:
				vs.TLS = append(vs.TLS, r)
			}
		}
	}
	return vs
}

func stringMatch(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range []string{"exact", "prefix", "regex"} {
		if s, ok := m[k].(string); ok {
			return k + " " + s
		}
	}
	return ""
}

func describeMatch(m map[string]any) string {
	var parts []string
	for _, k := range []string{"uri", "scheme", "method", "authority"} {
		if s := stringMatch(m[k]); s != "" {
			parts = append(parts, k+" "+s)
		}
	}
	for _, k := range []string{"headers", "queryParams", "withoutHeaders"} {
		hm, _ := m[k].(map[string]any)
		keys := make([]string, 0, len(hm))
		for h := range hm {
			keys = append(keys, h)
		}
		sort.Strings(keys)
		label := map[string]string{"headers": "header", "queryParams": "query", "withoutHeaders": "sem header"}[k]
		for _, h := range keys {
			parts = append(parts, strings.TrimSpace(label+" "+h+" "+stringMatch(hm[h])))
		}
	}
	if p := num(m, "port"); p > 0 {
		parts = append(parts, fmt.Sprintf("porta %d", p))
	}
	if s := strs(m, "sniHosts"); len(s) > 0 {
		parts = append(parts, "sni "+strings.Join(s, ", "))
	}
	if s := strs(m, "destinationSubnets"); len(s) > 0 {
		parts = append(parts, "subnets "+strings.Join(s, ", "))
	}
	if l, ok, _ := unstructured.NestedStringMap(m, "sourceLabels"); ok && len(l) > 0 {
		parts = append(parts, "origem "+labelString(l))
	}
	if s := str(m, "sourceNamespace"); s != "" {
		parts = append(parts, "namespace de origem "+s)
	}
	if s := strs(m, "gateways"); len(s) > 0 {
		parts = append(parts, "gateways "+strings.Join(s, ", "))
	}
	return strings.Join(parts, " · ")
}

func httpNotes(rule map[string]any) []string {
	var notes []string
	if r, ok := rule["redirect"].(map[string]any); ok {
		notes = append(notes, strings.TrimSpace("redirect "+str(r, "authority")+str(r, "uri")))
	}
	if r, ok := rule["rewrite"].(map[string]any); ok {
		notes = append(notes, strings.TrimSpace("rewrite "+str(r, "authority")+" "+str(r, "uri")))
	}
	if r, ok := rule["directResponse"].(map[string]any); ok {
		notes = append(notes, fmt.Sprintf("resposta direta %d", num(r, "status")))
	}
	if s := str(rule, "timeout"); s != "" {
		notes = append(notes, "timeout "+s)
	}
	if _, ok := rule["retries"].(map[string]any); ok {
		notes = append(notes, fmt.Sprintf("retries %d", num(rule, "retries", "attempts")))
	}
	if _, ok := rule["fault"].(map[string]any); ok {
		notes = append(notes, "injeção de falha")
	}
	if s := str(rule, "mirror", "host"); s != "" {
		notes = append(notes, "espelho "+s)
	}
	return notes
}

// --- DestinationRule and Gateway ---

// ParseDestinationRule reads the host, its Service, subsets and the client TLS mode.
func ParseDestinationRule(u *unstructured.Unstructured, svcs Services) DestinationRule {
	host := str(u.Object, "spec", "host")
	dr := DestinationRule{Namespace: u.GetNamespace(), Name: u.GetName(), Host: host, Service: ResolveHost(host, u.GetNamespace(), svcs),
		Subsets: []Subset{}, TLSMode: str(u.Object, "spec", "trafficPolicy", "tls", "mode")}
	subsets, _, _ := unstructured.NestedSlice(u.Object, "spec", "subsets")
	for _, x := range subsets {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		l, _, _ := unstructured.NestedStringMap(m, "labels")
		if l == nil {
			l = map[string]string{}
		}
		dr.Subsets = append(dr.Subsets, Subset{Name: str(m, "name"), Labels: l, TLSMode: str(m, "trafficPolicy", "tls", "mode")})
	}
	return dr
}

// ParseGateway reads the workload selector and the servers.
func ParseGateway(u *unstructured.Unstructured) Gateway {
	sel, _, _ := unstructured.NestedStringMap(u.Object, "spec", "selector")
	if sel == nil {
		sel = map[string]string{}
	}
	g := Gateway{Namespace: u.GetNamespace(), Name: u.GetName(), Selector: sel, Servers: []Server{}}
	servers, _, _ := unstructured.NestedSlice(u.Object, "spec", "servers")
	for _, x := range servers {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		hosts := strs(m, "hosts")
		if hosts == nil {
			hosts = []string{}
		}
		g.Servers = append(g.Servers, Server{Port: num(m, "port", "number"), Name: str(m, "port", "name"), Protocol: str(m, "port", "protocol"),
			Hosts: hosts, TLSMode: str(m, "tls", "mode")})
	}
	return g
}

// --- helpers over unstructured maps ---

func str(o map[string]any, path ...string) string {
	s, _, _ := unstructured.NestedString(o, path...)
	return s
}

func strs(o map[string]any, path ...string) []string {
	v, _, _ := unstructured.NestedFieldNoCopy(o, path...)
	list, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// num reads an integer that may be decoded as int64 or float64 (JSON).
func num(o map[string]any, path ...string) int64 {
	v, _, _ := unstructured.NestedFieldNoCopy(o, path...)
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func labelString(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+l[k])
	}
	return strings.Join(parts, ",")
}
