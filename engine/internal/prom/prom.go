// Package prom queries a Prometheus-compatible API through the API server service proxy or a direct URL.
package prom

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// Series is one line of a chart.
type Series struct {
	Name   string       `json:"name"`
	Ref    string       `json:"ref,omitempty"` // reference line: capacity, allocatable, requests, limits
	Points [][2]float64 `json:"points"`        // [unix seconds, value]
}

// Client runs PromQL queries for one session.
type Client struct {
	get func(ctx context.Context, path string, params map[string]string) ([]byte, error)
}

// ValidateURL accepts https anywhere and http only on loopback (bearer tokens must not travel in clear text).
func ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid Prometheus URL")
	}
	if u.User != nil {
		return nil, errors.New("credentials in the Prometheus URL are not supported")
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("plain http is only allowed for localhost; use https")
		}
	default:
		return nil, errors.New("Prometheus URL must be https://")
	}
	return u, nil
}

var (
	dns1123   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	portRe    = regexp.MustCompile(`^([0-9]{1,5}|[a-z0-9]([-a-z0-9]*[a-z0-9])?)$`)
	proxyPath = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)*/?$`)
)

// Validate checks a configuration before it is used or saved.
func Validate(cfg settings.Prometheus) error {
	switch cfg.Mode {
	case "", "service":
		if cfg.Mode == "" {
			return nil
		}
		if !dns1123.MatchString(cfg.Namespace) || !dns1123.MatchString(cfg.Service) {
			return errors.New("namespace and service must be valid Kubernetes names")
		}
		if cfg.Port != "" && !portRe.MatchString(cfg.Port) {
			return errors.New("port must be a number or a port name")
		}
		if cfg.Scheme != "" && cfg.Scheme != "http" && cfg.Scheme != "https" {
			return errors.New("scheme must be http or https")
		}
		// The path is appended to .../services/<svc>/proxy; ".." would escape to other API paths.
		if cfg.Path != "" && (!proxyPath.MatchString(cfg.Path) || strings.Contains(cfg.Path, "..")) {
			return errors.New("invalid path prefix")
		}
		if cfg.SendToken || cfg.URL != "" {
			return errors.New("url and sendToken only apply to direct URL mode")
		}
	case "url":
		if _, err := ValidateURL(cfg.URL); err != nil {
			return err
		}
	default:
		return errors.New("mode must be service or url")
	}
	return nil
}

// TokenAllowed reports whether the kubeconfig bearer token may be sent to host (see kube.SameClusterDomain).
func TokenAllowed(apiServer, host string) bool { return kube.SameClusterDomain(apiServer, host) }

// NewClient builds a client from a saved or detected configuration.
func NewClient(sess *cluster.Session, cfg settings.Prometheus) (*Client, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	switch cfg.Mode {
	case "service":
		scheme := cfg.Scheme
		if scheme != "https" {
			scheme = ""
		}
		prefix := "/" + strings.Trim(cfg.Path, "/")
		if prefix == "/" {
			prefix = ""
		}
		return &Client{get: func(ctx context.Context, path string, params map[string]string) ([]byte, error) {
			return sess.Typed.CoreV1().Services(cfg.Namespace).ProxyGet(scheme, cfg.Service, cfg.Port, prefix+path, params).DoRaw(ctx)
		}}, nil
	case "url":
		u, err := ValidateURL(cfg.URL)
		if err != nil {
			return nil, err
		}
		token := ""
		if cfg.SendToken {
			if !TokenAllowed(sess.Config.Host, u.Hostname()) {
				return nil, fmt.Errorf("the cluster token is only sent to the API server or hosts under its domain; %s is not", u.Hostname())
			}
			token = sess.Config.BearerToken
			if token == "" && sess.Config.BearerTokenFile != "" {
				b, err := os.ReadFile(sess.Config.BearerTokenFile)
				if err != nil {
					return nil, err
				}
				token = strings.TrimSpace(string(b))
			}
			if token == "" {
				return nil, errors.New("this context does not authenticate with a bearer token; disable \"send token\" or use the service proxy")
			}
		}
		roots, _ := x509.SystemCertPool()
		if roots == nil {
			roots = x509.NewCertPool()
		}
		// OpenShift routes are often signed by the cluster's ingress CA, which is usually in the kubeconfig CA bundle.
		if len(sess.Config.CAData) > 0 {
			roots.AppendCertsFromPEM(sess.Config.CAData)
		} else if sess.Config.CAFile != "" {
			if b, err := os.ReadFile(sess.Config.CAFile); err == nil {
				roots.AppendCertsFromPEM(b)
			}
		}
		hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			Proxy:           http.ProxyFromEnvironment,
		}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		return &Client{get: func(ctx context.Context, path string, params map[string]string) ([]byte, error) {
			target := *u
			target.Path = strings.TrimRight(u.Path, "/") + path
			q := u.Query()
			for k, v := range params {
				q.Set(k, v)
			}
			target.RawQuery = q.Encode()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
			if err != nil {
				return nil, err
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			res, err := hc.Do(req)
			if err != nil {
				return nil, err
			}
			defer res.Body.Close()
			b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
			if err != nil {
				return nil, err
			}
			if res.StatusCode >= 300 {
				return nil, fmt.Errorf("prometheus: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 160)])))
			}
			return b, nil
		}}, nil
	default:
		return nil, errors.New("prometheus is not configured")
	}
}

type apiResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][2]any          `json:"values"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func parse(b []byte, legend string) ([]Series, error) {
	var r apiResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("unexpected Prometheus response: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus %s: %s", r.ErrorType, r.Error)
	}
	out := make([]Series, 0, len(r.Data.Result))
	for _, res := range r.Data.Result {
		s := Series{Name: legendOf(res.Metric, legend)}
		vals := res.Values
		if r.Data.ResultType == "vector" {
			vals = [][2]any{res.Value}
		}
		for _, v := range vals {
			ts, _ := v[0].(float64)
			str, _ := v[1].(string)
			f, err := strconv.ParseFloat(str, 64)
			if err != nil {
				continue
			}
			s.Points = append(s.Points, [2]float64{ts, f})
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func legendOf(m map[string]string, legend string) string {
	if legend == "" {
		return "total"
	}
	if strings.HasPrefix(legend, "=") {
		return legend[1:]
	}
	if v := m[legend]; v != "" {
		return v
	}
	return "total"
}

// QueryRange runs a range query.
func (c *Client) QueryRange(ctx context.Context, expr string, start, end time.Time, step time.Duration, legend string) ([]Series, error) {
	b, err := c.get(ctx, "/api/v1/query_range", map[string]string{
		"query": expr,
		"start": strconv.FormatInt(start.Unix(), 10),
		"end":   strconv.FormatInt(end.Unix(), 10),
		"step":  strconv.FormatInt(int64(step.Seconds()), 10),
	})
	if err != nil {
		return nil, err
	}
	return parse(b, legend)
}

// Query runs an instant query.
func (c *Client) Query(ctx context.Context, expr string) ([]Series, error) {
	b, err := c.get(ctx, "/api/v1/query", map[string]string{"query": expr})
	if err != nil {
		return nil, err
	}
	return parse(b, "")
}

// Candidate is a detected Prometheus-like service.
type Candidate struct {
	settings.Prometheus
	Score int `json:"score"`
}

var commonNamespaces = []string{"monitoring", "prometheus", "openshift-monitoring", "kube-prometheus", "observability", "cattle-monitoring-system", "kube-system", "default"}

// Detect looks for Prometheus/Thanos query services. It falls back to well-known namespaces when the
// user cannot list services cluster-wide.
func Detect(ctx context.Context, sess *cluster.Session) ([]Candidate, error) {
	var svcs []corev1.Service
	l, err := sess.Typed.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err == nil {
		svcs = l.Items
	} else {
		for _, ns := range commonNamespaces {
			if l, err := sess.Typed.CoreV1().Services(ns).List(ctx, metav1.ListOptions{}); err == nil {
				svcs = append(svcs, l.Items...)
			}
		}
	}
	var out []Candidate
	for _, s := range svcs {
		if c, ok := score(s); ok {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > 8 {
		out = out[:8]
	}
	return out, nil
}

var excluded = []string{"alertmanager", "operator", "node-exporter", "kube-state", "pushgateway", "adapter", "blackbox", "webhook", "grafana", "config-reloader", "-headless"}

func score(s corev1.Service) (Candidate, bool) {
	name := strings.ToLower(s.Name)
	app := strings.ToLower(s.Labels["app.kubernetes.io/name"] + " " + s.Labels["app"])
	isThanos := strings.Contains(name, "thanos-quer")
	if !strings.Contains(name, "prometheus") && !isThanos && !strings.Contains(app, "prometheus") {
		return Candidate{}, false
	}
	for _, x := range excluded {
		if strings.Contains(name, x) {
			return Candidate{}, false
		}
	}
	c := Candidate{Prometheus: settings.Prometheus{Mode: "service", Namespace: s.Namespace, Service: s.Name}}
	best := -1
	for _, p := range s.Spec.Ports {
		pts := 0
		switch {
		case p.Port == 9090 || p.Name == "web" || p.Name == "http-web":
			pts = 3
		case p.Port == 80 || p.Name == "http":
			pts = 2
		case p.Port == 9091 && isThanos:
			pts = 3
		}
		if pts > best {
			best = pts
			c.Port = strconv.Itoa(int(p.Port))
			if p.Name == "https" || p.Port == 9091 || p.Port == 443 {
				c.Scheme = "https"
			}
		}
	}
	if c.Port == "" {
		return Candidate{}, false
	}
	c.Score = best
	switch {
	case name == "prometheus-server" || name == "prometheus-operated" || name == "prometheus-k8s" || strings.HasSuffix(name, "-prometheus"):
		c.Score += 5
	case isThanos:
		c.Score += 4
	}
	if s.Namespace == "openshift-monitoring" {
		// The service proxy cannot pass the user's credentials to the OAuth/RBAC proxy in front of Thanos.
		c.Score -= 3
	}
	return c, true
}

// cache keeps query results briefly so several charts and panels share one request.
type cache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

type cacheEntry struct {
	at     time.Time
	series []Series
}

var results = &cache{m: map[string]cacheEntry{}}

func (c *cache) get(k string) ([]Series, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Since(e.at) > 10*time.Second {
		return nil, false
	}
	return e.series, true
}

func (c *cache) put(k string, s []Series) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 500 {
		for key, e := range c.m {
			if time.Since(e.at) > 10*time.Second {
				delete(c.m, key)
			}
		}
	}
	c.m[k] = cacheEntry{at: time.Now(), series: s}
}
