// Package anonymize replaces cluster identifiers (namespaces, object names, private hosts, IPs and
// e-mails) with stable aliases before text leaves the machine, and restores them in the replies.
// It is a best-effort privacy layer on top of internal/redact, which already removes credentials.
package anonymize

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Kinds of identifiers and their alias prefixes.
const (
	Namespace = "ns"
	Object    = "obj"
	Host      = "host"
	IP        = "ip"
	Email     = "email"
)

var (
	ipRe    = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	emailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	hostRe  = regexp.MustCompile(`\b[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,62}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,62}[a-zA-Z0-9])?)+\.[a-zA-Z]{2,}\b`)
	// Public, well-known domains (API groups, label keys, public registries) carry no private
	// information and the model needs them to understand the YAML.
	publicSuffixes = []string{
		"kubernetes.io", "k8s.io", "openshift.io", "openshift.com", "x-k8s.io", "istio.io", "cert-manager.io", "helm.sh",
		"docker.io", "docker.com", "quay.io", "gcr.io", "ghcr.io", "github.com", "githubusercontent.com", "redhat.com", "redhat.io",
		"cluster.local", "example.com", "example.org", "googleapis.com", "amazonaws.com", "azurecr.io", "nginx.org", "prometheus.io",
	}
	// Names too common to be identifying; replacing them would only confuse the model.
	stopNames = map[string]bool{
		"default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true, "true": true, "false": true,
		"app": true,
	}
	// Only dotted names ending in a real-looking TLD are hosts; "status.phase" or "nginx.conf" are not.
	hostTLDs = map[string]bool{
		"com": true, "net": true, "org": true, "io": true, "br": true, "local": true, "internal": true, "corp": true, "lan": true,
		"cloud": true, "dev": true, "app": true, "gov": true, "edu": true, "intra": true, "intranet": true, "eu": true, "us": true,
		"uk": true, "de": true, "pt": true, "es": true, "fr": true, "info": true, "biz": true, "co": true, "ai": true, "tech": true,
	}
	versionLike = regexp.MustCompile(`^v?\d+(\.\d+)+$`)
)

// Map is a reversible mapping for one conversation. The zero value is not usable; call New.
// A disabled Map passes text through unchanged.
type Map struct {
	enabled bool
	mu      sync.Mutex
	fwd     map[string]string // real -> alias
	rev     map[string]string // alias -> real
	next    map[string]int
	order   []string // real values, longest first (rebuilt lazily)
	dirty   bool
	maxLen  int // longest alias, for streaming holdback
}

// New returns an empty map.
func New(enabled bool) *Map {
	return &Map{enabled: enabled, fwd: map[string]string{}, rev: map[string]string{}, next: map[string]int{}}
}

// Enabled reports whether identifiers are replaced.
func (m *Map) Enabled() bool { return m.enabled }

// Learn registers a real identifier of a kind and returns its alias.
func (m *Map) Learn(kind, value string) string {
	if !m.enabled || len(value) < 3 || stopNames[value] || versionLike.MatchString(value) {
		return value
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.learnLocked(kind, value)
}

func (m *Map) learnLocked(kind, value string) string {
	if a, ok := m.fwd[value]; ok {
		return a
	}
	m.next[kind]++
	a := kind + "-" + itoa(m.next[kind])
	m.fwd[value], m.rev[a] = a, value
	m.dirty = true
	if len(a) > m.maxLen {
		m.maxLen = len(a)
	}
	return a
}

// Text replaces known identifiers, and discovers private hosts, IPs and e-mails on the way.
func (m *Map) Text(s string) string {
	if !m.enabled || s == "" {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range emailRe.FindAllString(s, -1) {
		m.learnLocked(Email, v)
	}
	for _, v := range hostRe.FindAllString(s, -1) {
		if tld := v[strings.LastIndex(v, ".")+1:]; hostTLDs[strings.ToLower(tld)] && !public(v) {
			m.learnLocked(Host, strings.ToLower(v))
		}
	}
	for _, v := range ipRe.FindAllString(s, -1) {
		if v != "0.0.0.0" && v != "127.0.0.1" {
			m.learnLocked(IP, v)
		}
	}
	if m.dirty {
		m.order = m.order[:0]
		for k := range m.fwd {
			m.order = append(m.order, k)
		}
		sort.Slice(m.order, func(i, j int) bool {
			return len(m.order[i]) > len(m.order[j]) || (len(m.order[i]) == len(m.order[j]) && m.order[i] < m.order[j])
		})
		m.dirty = false
	}
	return replace(s, m.order, func(k string) string { return m.fwd[k] }, true)
}

// Restore puts the real identifiers back.
func (m *Map) Restore(s string) string {
	if !m.enabled || s == "" {
		return s
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.rev))
	for k := range m.rev {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j]) || (len(keys[i]) == len(keys[j]) && keys[i] < keys[j])
	})
	return replace(s, keys, func(k string) string { return m.rev[k] }, false)
}

// Holdback is how many trailing characters a streaming restorer keeps until more text arrives
// (an alias split across two deltas must not reach the user half-restored).
func (m *Map) Holdback() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxLen
}

// Restorer restores identifiers in a stream of text deltas.
type Restorer struct {
	m   *Map
	buf string
}

// NewRestorer returns a streaming restorer.
func (m *Map) NewRestorer() *Restorer { return &Restorer{m: m} }

// Push adds a delta and returns the restored text that is safe to show now.
func (r *Restorer) Push(delta string) string {
	if !r.m.enabled {
		return delta
	}
	r.buf += delta
	keep := r.m.Holdback()
	if len(r.buf) <= keep {
		return ""
	}
	cut := len(r.buf) - keep
	// Never cut inside an identifier-like run: back up to the previous boundary.
	for cut > 0 && isIdent(r.buf[cut-1]) && cut < len(r.buf) && isIdent(r.buf[cut]) {
		cut--
	}
	for cut > 0 && !utf8.RuneStart(r.buf[cut]) {
		cut--
	}
	out := r.m.Restore(r.buf[:cut])
	r.buf = r.buf[cut:]
	return out
}

// Flush returns whatever is still held back.
func (r *Restorer) Flush() string {
	out := r.m.Restore(r.buf)
	r.buf = ""
	return out
}

func public(host string) bool {
	h := strings.ToLower(host)
	for _, s := range publicSuffixes {
		if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}

func isIdent(b byte) bool {
	return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// replace substitutes every key (tried in the given order) found at identifier boundaries.
func replace(s string, keys []string, to func(string) string, fold bool) string {
	if len(keys) == 0 {
		return s
	}
	var b strings.Builder
	low := s
	if fold {
		low = strings.ToLower(s)
	}
	for i := 0; i < len(s); {
		matched := false
		if i == 0 || !isIdent(s[i-1]) {
			for _, k := range keys {
				kk := k
				if fold {
					kk = strings.ToLower(k)
				}
				if strings.HasPrefix(low[i:], kk) {
					end := i + len(kk)
					// "-suffix" also counts: Kubernetes derives names from their owner
					// (Deployment broken -> ReplicaSet broken-86b9596b76 -> Pod broken-86b9596b76-h4hht).
					if end == len(s) || !isIdent(s[end]) || s[end] == '-' {
						b.WriteString(to(k))
						i = end
						matched = true
						break
					}
				}
			}
		}
		if !matched {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
