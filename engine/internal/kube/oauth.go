package kube

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// OAuthServer is the OpenShift OAuth metadata (/.well-known/oauth-authorization-server).
type OAuthServer struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
}

// oauthClient trusts the system roots plus the kubeconfig CA (OpenShift ingress certificates are often
// signed by the cluster CA) and honours insecure-skip-tls-verify only if the kubeconfig already chose it.
func oauthClient(cfg *rest.Config) *http.Client {
	roots, _ := x509.SystemCertPool()
	if roots == nil {
		roots = x509.NewCertPool()
	}
	if len(cfg.CAData) > 0 {
		roots.AppendCertsFromPEM(cfg.CAData)
	} else if cfg.CAFile != "" {
		if b, err := os.ReadFile(cfg.CAFile); err == nil {
			roots.AppendCertsFromPEM(b)
		}
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.Insecure}, Proxy: http.ProxyFromEnvironment} //nolint:gosec // mirrors the kubeconfig choice
	if cfg.Proxy != nil {
		tr.Proxy = cfg.Proxy
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr,
		// Never follow redirects automatically: the token comes back in a redirect we must read.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// checkEndpoint only allows https endpoints under the cluster's domain: the user's password or the
// authorization code is never sent anywhere else, even if the discovery document says so.
func checkEndpoint(apiServer, endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("endpoint OAuth inválido: %q", endpoint)
	}
	host := strings.ToLower(u.Hostname())
	api, _ := url.Parse(apiServer)
	// The OpenShift OAuth server is the API host itself or a route like oauth-openshift.apps.<cluster>.
	plausible := api != nil && host == strings.ToLower(api.Hostname()) || strings.HasPrefix(host, "oauth") || strings.Contains(host, ".apps.")
	if !SameClusterDomain(apiServer, host) || !plausible {
		return nil, fmt.Errorf("o servidor OAuth %s não está no domínio do cluster; login recusado", u.Hostname())
	}
	return u, nil
}

// DiscoverOAuth reads the OAuth server metadata from the API server (served anonymously by OpenShift).
func DiscoverOAuth(ctx context.Context, cfg *rest.Config) (*OAuthServer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.Host, "/")+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		return nil, err
	}
	res, err := oauthClient(cfg).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("este cluster não expõe OAuth do OpenShift (HTTP %d)", res.StatusCode)
	}
	var s OAuthServer
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&s); err != nil {
		return nil, err
	}
	if _, err := checkEndpoint(cfg.Host, s.AuthorizationEndpoint); err != nil {
		return nil, err
	}
	if _, err := checkEndpoint(cfg.Host, s.TokenEndpoint); err != nil {
		return nil, err
	}
	return &s, nil
}

// ErrBadCredentials is returned when the OAuth server rejects the username or password.
var ErrBadCredentials = errors.New("usuário ou senha inválidos")

// PasswordLogin uses the challenging client (what `oc login -u` does): basic auth on the authorize
// endpoint, token returned in the redirect fragment. Credentials are not stored or logged.
func PasswordLogin(ctx context.Context, cfg *rest.Config, s *OAuthServer, user, password string) (string, error) {
	u, err := checkEndpoint(cfg.Host, s.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "token")
	q.Set("client_id", "openshift-challenging-client")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(user, password)
	req.Header.Set("X-CSRF-Token", "1")
	res, err := oauthClient(cfg).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return "", ErrBadCredentials
	case res.StatusCode != http.StatusFound:
		return "", fmt.Errorf("resposta inesperada do servidor OAuth (HTTP %d)", res.StatusCode)
	}
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	frag, _ := url.ParseQuery(loc.Fragment)
	if e := frag.Get("error"); e != "" {
		return "", fmt.Errorf("OAuth: %s", e)
	}
	token := frag.Get("access_token")
	if token == "" {
		return "", errors.New("o servidor OAuth não devolveu token")
	}
	return token, nil
}

// WebLogin is an authorization-code + PKCE flow with a loopback callback (what `oc login --web` does).
type WebLogin struct {
	AuthorizeURL string
	verifier     string
	redirect     string
	ln           net.Listener
	srv          *http.Server
	result       chan webResult
	cfg          *rest.Config
	server       *OAuthServer
}

type webResult struct {
	code string
	err  error
}

const webClientID = "openshift-cli-client"

// WebLoginTTL bounds how long the loopback callback stays open.
var WebLoginTTL = 5 * time.Minute

// StartWebLogin opens a loopback listener and returns the URL the user must open in a browser.
func StartWebLogin(cfg *rest.Config, s *OAuthServer) (*WebLogin, error) {
	auth, err := checkEndpoint(cfg.Host, s.AuthorizationEndpoint)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		ln.Close()
		return nil, err
	}
	stateB := make([]byte, 16)
	if _, err := rand.Read(stateB); err != nil {
		ln.Close()
		return nil, err
	}
	w := &WebLogin{verifier: base64.RawURLEncoding.EncodeToString(b), ln: ln, result: make(chan webResult, 1), cfg: cfg, server: s}
	state := base64.RawURLEncoding.EncodeToString(stateB)
	w.redirect = fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	sum := sha256.Sum256([]byte(w.verifier))
	q := auth.Query()
	q.Set("client_id", webClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", w.redirect)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	auth.RawQuery = q.Encode()
	w.AuthorizeURL = auth.String()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(rw http.ResponseWriter, r *http.Request) {
		qs := r.URL.Query()
		// Anything without our random state (another local process, a web page probing ports) is
		// rejected without affecting the pending login.
		if qs.Get("state") != state {
			http.Error(rw, "invalid state", http.StatusBadRequest)
			return
		}
		res := webResult{code: qs.Get("code")}
		switch {
		case qs.Get("error") != "":
			res.err = fmt.Errorf("OAuth: %s", qs.Get("error"))
		case res.code == "":
			res.err = errors.New("sem código de autorização")
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.err != nil {
			fmt.Fprint(rw, "<p>Falha no login. Volte ao Leme.</p>")
		} else {
			fmt.Fprint(rw, "<p>Login concluído. Pode fechar esta aba e voltar ao Leme.</p>")
		}
		select {
		case w.result <- res:
		default:
		}
	})
	w.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = w.srv.Serve(ln) }()
	// A login nobody finishes must not leave a listener open.
	time.AfterFunc(WebLoginTTL, w.Close)
	return w, nil
}

// Wait blocks until the browser returns (or ctx ends) and exchanges the code for a token.
func (w *WebLogin) Wait(ctx context.Context) (string, error) {
	defer w.srv.Close()
	var res webResult
	timeout := time.NewTimer(WebLoginTTL)
	defer timeout.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timeout.C:
		return "", errors.New("tempo esgotado esperando o login no navegador")
	case res = <-w.result:
	}
	if res.err != nil {
		return "", res.err
	}
	tok, err := checkEndpoint(w.cfg.Host, w.server.TokenEndpoint)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {res.code}, "redirect_uri": {w.redirect}, "client_id": {webClientID}, "code_verifier": {w.verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tok.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRes, err := oauthClient(w.cfg).Do(req)
	if err != nil {
		return "", err
	}
	defer httpRes.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(httpRes.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("OAuth: %s %s", body.Error, body.Description)
	}
	return body.AccessToken, nil
}

// Close stops the callback listener.
func (w *WebLogin) Close() { _ = w.srv.Close() }
