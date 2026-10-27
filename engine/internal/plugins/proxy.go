package plugins

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Policy decides, per request, what a plugin may do in the cluster. It is read on every request,
// so locking the cluster while a plugin runs takes effect immediately.
type Policy struct {
	ReadOnly func() bool // writes locked (production guard)
	Shell    func() bool // exec/attach allowed while writes stay locked
}

// check returns the reason a request is refused (given to the plugin's kubectl), or nil. upgrade
// marks requests that become interactive streams (WebSocket/SPDY).
func (p Policy) check(method, path string, upgrade bool) error {
	if !p.ReadOnly() {
		return nil
	}
	if path != "/" && pathpkg.Clean(path) != strings.TrimSuffix(path, "/") {
		return errors.New("Leme: caminho não canônico recusado com o cluster travado")
	}
	sub := subresource(path)
	switch sub {
	case "exec", "attach":
		if p.Shell() {
			return nil
		}
		return errors.New("Leme: cluster travado; exec/attach só com \"Liberar shell\"")
	case "proxy":
		return errors.New("Leme: cluster travado; proxy para pods, services e nodes bloqueado")
	case "portforward":
		return nil // like the app's own port-forwards: reading, not changing
	}
	if upgrade {
		// Other interactive streams (e.g. KubeVirt console/vnc on aggregated APIs) are refused.
		return errors.New("Leme: cluster travado; streams interativos bloqueados")
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	case http.MethodPost:
		if readOnlyReview(path) {
			return nil
		}
	}
	return errors.New("Leme: cluster travado (produção); este plugin só pode ler. Destrave a escrita no Leme para alterar")
}

// readOnlyReview matches the POST endpoints that only ask questions (kubectl auth can-i / whoami).
var readOnlyReview = regexp.MustCompile(`^/apis/(authorization\.k8s\.io/v1(beta1)?/selfsubject(access|rules)reviews|authentication\.k8s\.io/v1(beta1|alpha1)?/selfsubjectreviews)/?$`).MatchString

func isUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" || strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// subresource returns the subresource of a resource path (/api/v1/namespaces/n/pods/p/exec ->
// exec), or "" for collections, objects and non-resource paths.
func subresource(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	var rest []string
	switch {
	case len(segs) >= 2 && segs[0] == "api":
		rest = segs[2:]
	case len(segs) >= 3 && segs[0] == "apis":
		rest = segs[3:]
	default:
		return ""
	}
	if len(rest) > 0 && rest[0] == "proxy" {
		return "proxy" // legacy /api/v1/proxy/...
	}
	if len(rest) >= 3 && rest[0] == "namespaces" {
		rest = rest[2:]
	}
	// rest = resource[/name[/subresource[/...]]]; services/x:port/proxy/... and nodes/n/proxy/... too.
	if len(rest) >= 3 {
		return rest[2]
	}
	return ""
}

// Proxy is a loopback reverse proxy to the API server that holds the user's credentials, so the
// plugin's kubeconfig carries only a one-time token.
type Proxy struct {
	URL   string
	Token string
	// CAData is the PEM of the proxy's one-time certificate: clients (kubectl, helm…) refuse to send
	// a bearer token over plain HTTP, so the proxy serves HTTPS with a certificate made for this run.
	CAData []byte
	srv    *http.Server
	ln     net.Listener

	mu      sync.Mutex
	streams map[string]*stream // by remote address
	stop    chan struct{}
}

// stream is a connection taken over by an interactive stream (exec, attach, port-forward). Those
// bypass the HTTP server's shutdown, so the proxy closes them itself.
type stream struct {
	conn     net.Conn
	method   string
	path     string
	upgrade  bool
	hijacked bool
}

// StartProxy listens on 127.0.0.1 for the duration of one plugin run.
func StartProxy(cfg *rest.Config, pol Policy) (*Proxy, error) {
	target, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, err
	}
	if target.Scheme == "" {
		target.Scheme = "https"
	}
	rt, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	p := &Proxy{Token: hex.EncodeToString(tok), streams: map[string]*stream{}, stop: make(chan struct{})}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = rt
	rp.FlushInterval = -1 // watches and log follows stream
	director := rp.Director
	rp.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		// Our token must not reach the API server; the transport adds the user's credentials only
		// when the request has no Authorization of its own.
		r.Header.Del("Authorization")
		// Nor can the plugin choose another identity or smuggle its token in a WebSocket subprotocol.
		for k := range r.Header {
			if lk := strings.ToLower(k); strings.HasPrefix(lk, "impersonate-") || strings.HasPrefix(lk, "x-remote-") {
				r.Header.Del(k)
			}
		}
		if v := r.Header.Values("Sec-WebSocket-Protocol"); len(v) > 0 {
			var keep []string
			for _, h := range v {
				for _, sp := range strings.Split(h, ",") {
					if sp = strings.TrimSpace(sp); sp != "" && !strings.HasPrefix(sp, "base64url.bearer.authorization.k8s.io.") {
						keep = append(keep, sp)
					}
				}
			}
			r.Header.Del("Sec-WebSocket-Protocol")
			if len(keep) > 0 {
				r.Header.Set("Sec-WebSocket-Protocol", strings.Join(keep, ", "))
			}
		}
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		status(w, http.StatusBadGateway, "Leme: falha ao falar com o API server: "+err.Error())
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(p.Token)) != 1 {
			status(w, http.StatusUnauthorized, "Leme: token do plugin inválido")
			return
		}
		up := isUpgrade(r)
		if err := pol.check(r.Method, r.URL.Path, up); err != nil {
			status(w, http.StatusForbidden, err.Error())
			return
		}
		if up {
			p.mu.Lock()
			if st := p.streams[r.RemoteAddr]; st != nil {
				st.method, st.path, st.upgrade = r.Method, r.URL.Path, true
			}
			p.mu.Unlock()
		}
		rp.ServeHTTP(w, r)
	})
	cert, caPEM, err := selfSigned()
	if err != nil {
		return nil, err
	}
	p.CAData = caPEM
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p.ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	p.URL = "https://" + ln.Addr().String()
	p.srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ConnState: func(c net.Conn, st http.ConnState) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch st {
		case http.StateNew:
			p.streams[c.RemoteAddr().String()] = &stream{conn: c}
		case http.StateHijacked:
			if s := p.streams[c.RemoteAddr().String()]; s != nil {
				s.hijacked = true
			}
		case http.StateClosed:
			delete(p.streams, c.RemoteAddr().String())
		}
	}}
	go func() { _ = p.srv.Serve(p.ln) }()
	go p.enforce(pol)
	return p, nil
}

// enforce re-checks open interactive streams: locking the cluster or revoking the shell closes
// the exec/attach sessions a plugin opened before.
func (p *Proxy) enforce(pol Policy) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
		}
		p.mu.Lock()
		for k, s := range p.streams {
			if s.hijacked && s.upgrade && pol.check(s.method, s.path, true) != nil {
				_ = s.conn.Close()
				delete(p.streams, k)
			}
		}
		p.mu.Unlock()
	}
}

// selfSigned makes a short-lived certificate for 127.0.0.1, its own CA. The key never leaves memory.
func selfSigned() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Leme plugin proxy"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// Close stops the proxy and its open streams.
func (p *Proxy) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
	_ = p.srv.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	// Hijacked connections (exec, attach, port-forward) survive Shutdown and Close.
	for k, s := range p.streams {
		_ = s.conn.Close()
		delete(p.streams, k)
	}
}

// status writes a Kubernetes Status, which kubectl prints as a normal API error.
func status(w http.ResponseWriter, code int, msg string) {
	reason := map[int]string{401: "Unauthorized", 403: "Forbidden", 502: "ServiceUnavailable"}[code]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "message": msg, "reason": reason, "code": code})
}

// WriteKubeconfig writes a kubeconfig with a single context, named like the real one, pointing to
// the proxy. Cluster and user entries keep their real names so `--cluster`/`--user` resolve.
func WriteKubeconfig(dir string, p *Proxy, contextName, cluster, user, namespace string) (string, error) {
	if cluster == "" {
		cluster = "leme"
	}
	if user == "" {
		user = "leme"
	}
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[cluster] = &clientcmdapi.Cluster{Server: p.URL, CertificateAuthorityData: p.CAData}
	cfg.AuthInfos[user] = &clientcmdapi.AuthInfo{Token: p.Token}
	cfg.Contexts[contextName] = &clientcmdapi.Context{Cluster: cluster, AuthInfo: user, Namespace: namespace}
	cfg.CurrentContext = contextName
	path := filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		return "", err
	}
	return path, os.Chmod(path, 0o600)
}
