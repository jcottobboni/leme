package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// fakeOpenShift serves discovery, the challenging authorize endpoint and the token endpoint on one
// TLS server. Its host is "127.0.0.1", so the OAuth endpoints point to the API server host itself.
func fakeOpenShift(t *testing.T, discoveryHost string) (*httptest.Server, *rest.Config) {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		host := discoveryHost
		if host == "" {
			host = srv.URL
		}
		_ = json.NewEncoder(w).Encode(OAuthServer{Issuer: host, AuthorizationEndpoint: host + "/oauth/authorize", TokenEndpoint: host + "/oauth/token"})
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_id") == "openshift-challenging-client" {
			u, p, ok := r.BasicAuth()
			if !ok || u != "alice" || p != "s3cr3t" || r.Header.Get("X-CSRF-Token") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Location", srv.URL+"/oauth/token/implicit#access_token=sha256~pwd-token&token_type=Bearer")
			w.WriteHeader(http.StatusFound)
			return
		}
		// Web flow: send the browser back to the loopback callback with a code.
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "pkce required", 400)
			return
		}
		cb, _ := url.Parse(q.Get("redirect_uri"))
		cq := cb.Query()
		cq.Set("code", "the-code")
		cq.Set("state", q.Get("state"))
		cb.RawQuery = cq.Encode()
		http.Redirect(w, r, cb.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_id") != webClientID {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "sha256~web-token"})
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}}
}

func TestPasswordLogin(t *testing.T) {
	_, cfg := fakeOpenShift(t, "")
	ctx := context.Background()
	s, err := DiscoverOAuth(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := PasswordLogin(ctx, cfg, s, "alice", "s3cr3t")
	if err != nil || tok != "sha256~pwd-token" {
		t.Fatalf("%q %v", tok, err)
	}
	if _, err := PasswordLogin(ctx, cfg, s, "alice", "wrong"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestDiscoveryRejectsForeignOAuthServer(t *testing.T) {
	_, cfg := fakeOpenShift(t, "https://evil.example.com")
	if _, err := DiscoverOAuth(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "domínio") {
		t.Fatalf("a discovery document pointing outside the cluster must be refused: %v", err)
	}
}

func TestWebLogin(t *testing.T) {
	_, cfg := fakeOpenShift(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := DiscoverOAuth(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w, err := StartWebLogin(cfg, s)
	if err != nil {
		t.Fatal(err)
	}
	// Play the browser: follow the authorize redirect to the loopback callback.
	browser := oauthClient(cfg)
	browser.CheckRedirect = nil
	go func() {
		res, err := browser.Get(w.AuthorizeURL)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}
	}()
	tok, err := w.Wait(ctx)
	if err != nil || tok != "sha256~web-token" {
		t.Fatalf("%q %v", tok, err)
	}
}

func TestSameClusterDomain(t *testing.T) {
	if !SameClusterDomain("https://api.ocp.corp.example.com:6443", "oauth-openshift.apps.ocp.corp.example.com") {
		t.Error("OAuth route under the cluster domain must be accepted")
	}
	if SameClusterDomain("https://api.ocp.corp.example.com:6443", "oauth.example.com") {
		t.Error("foreign host must be rejected")
	}
}

func TestWebLoginIgnoresForeignCallbacks(t *testing.T) {
	_, cfg := fakeOpenShift(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := DiscoverOAuth(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	w, err := StartWebLogin(cfg, s)
	if err != nil {
		t.Fatal(err)
	}
	cb := strings.Replace(w.redirect, "/callback", "/callback?state=forged&code=x", 1)
	res, err := http.Get(cb)
	if err != nil || res.StatusCode != http.StatusBadRequest {
		t.Fatalf("forged callback: %v %v", err, res)
	}
	browser := oauthClient(cfg)
	browser.CheckRedirect = nil
	go func() {
		if r, err := browser.Get(w.AuthorizeURL); err == nil {
			r.Body.Close()
		}
	}()
	if tok, err := w.Wait(ctx); err != nil || tok != "sha256~web-token" {
		t.Fatalf("the real login must still succeed: %q %v", tok, err)
	}
}
