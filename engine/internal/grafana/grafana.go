// Package grafana detects Grafana and Prometheus web UIs that the app opens through a local
// port-forward. Admin credentials are only ever returned as a reference to the Secret that holds
// them: this package never reads Secret objects with their data (metadata only).
package grafana

import (
	"context"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/prom"
)

// Kinds of target.
const (
	KindGrafana    = "grafana"
	KindPrometheus = "prometheus"
)

// SecretRef points at a Secret; it never carries values.
type SecretRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Target is a web UI behind a Service.
type Target struct {
	Kind        string     `json:"kind"` // grafana | prometheus
	Namespace   string     `json:"namespace"`
	Service     string     `json:"service"`
	Port        int        `json:"port"`
	Scheme      string     `json:"scheme"` // http | https
	Score       int        `json:"score"`
	AdminSecret *SecretRef `json:"adminSecret,omitempty"`
}

// Same reports whether t is the service port (namespace, service, port).
func (t Target) Same(namespace, service string, port int) bool {
	return t.Namespace == namespace && t.Service == service && t.Port == port
}

// Find returns the detected target for a service port.
func Find(targets []Target, namespace, service string, port int) (Target, bool) {
	for _, t := range targets {
		if t.Same(namespace, service, port) {
			return t, true
		}
	}
	return Target{}, false
}

// labelScore is the evidence that a Service fronts Grafana (0 = none). Only labels count: a name
// alone ("grafana-agent", "grafana-operator-metrics") is not enough.
func labelScore(l map[string]string) int {
	switch {
	case strings.EqualFold(l["app.kubernetes.io/name"], "grafana"):
		return 5
	case strings.EqualFold(l["app"], "grafana"):
		return 4
	}
	return 0
}

// ScoreGrafana returns the Grafana target of a Service: Grafana labels and a web port (3000 or a
// port named http/https/http-web/service). The best port wins.
func ScoreGrafana(s corev1.Service) (Target, bool) {
	base := labelScore(s.Labels)
	if base == 0 || strings.HasSuffix(s.Name, "-headless") || s.Spec.Type == corev1.ServiceTypeExternalName {
		return Target{}, false
	}
	t := Target{Kind: KindGrafana, Namespace: s.Namespace, Service: s.Name, Scheme: "http"}
	best := 0
	for _, p := range s.Spec.Ports {
		if p.Protocol != "" && p.Protocol != corev1.ProtocolTCP {
			continue
		}
		pts := 0
		switch {
		case p.Port == 3000:
			pts = 3
		case p.Name == "http" || p.Name == "http-web" || p.Name == "service" || p.Name == "https" || p.Name == "web":
			pts = 2
		}
		if pts > best {
			best = pts
			t.Port = int(p.Port)
			t.Scheme = "http"
			if p.Name == "https" || p.Port == 443 {
				t.Scheme = "https"
			}
		}
	}
	if best == 0 {
		return Target{}, false
	}
	t.Score = base + best
	return t, true
}

// Grafana environment variables that take the admin login from a Secret.
var adminEnv = map[string]bool{"GF_SECURITY_ADMIN_PASSWORD": true, "GF_SECURITY_ADMIN_USER": true}

// AdminSecretFromPods finds the Secret the Grafana pods take the admin login from (env
// secretKeyRef, or a GF_SECURITY_ADMIN_PASSWORD__FILE pointing into a secret volume). Only names
// are read from the pod spec; literal env values are ignored.
func AdminSecretFromPods(pods []corev1.Pod) *SecretRef {
	for _, p := range pods {
		for _, c := range p.Spec.Containers {
			for _, e := range c.Env {
				if adminEnv[e.Name] && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil && e.ValueFrom.SecretKeyRef.Name != "" {
					return &SecretRef{Namespace: p.Namespace, Name: e.ValueFrom.SecretKeyRef.Name}
				}
			}
			for _, e := range c.Env {
				if e.Name != "GF_SECURITY_ADMIN_PASSWORD__FILE" || e.Value == "" {
					continue
				}
				for _, m := range c.VolumeMounts {
					if !strings.HasPrefix(e.Value, strings.TrimSuffix(m.MountPath, "/")+"/") {
						continue
					}
					for _, v := range p.Spec.Volumes {
						if v.Name == m.Name && v.Secret != nil && v.Secret.SecretName != "" {
							return &SecretRef{Namespace: p.Namespace, Name: v.Secret.SecretName}
						}
					}
				}
			}
		}
	}
	return nil
}

// SecretCandidates are the conventional admin Secret names of a Grafana Service: the Helm chart
// (same name as the service), "<svc>-admin" and the Grafana operator ("<name>-admin-credentials").
func SecretCandidates(s corev1.Service) []string {
	out := []string{s.Name + "-admin", s.Name, s.Name + "-admin-credentials"}
	if strings.HasSuffix(s.Name, "-service") {
		out = append(out, strings.TrimSuffix(s.Name, "-service")+"-admin-credentials")
	}
	if inst := s.Labels["app.kubernetes.io/instance"]; inst != "" && inst+"-grafana" != s.Name {
		out = append(out, inst+"-grafana")
	}
	return out
}

var secretsGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

var commonNamespaces = []string{"monitoring", "grafana", "observability", "prometheus", "kube-prometheus", "cattle-monitoring-system", "openshift-monitoring", "kube-system", "default"}

// Detect lists Grafana services (by labels) and Prometheus services (prom.Detect). It falls back to
// well-known namespaces when the user cannot list services cluster-wide.
func Detect(ctx context.Context, sess *cluster.Session) ([]Target, error) {
	var svcs []corev1.Service
	l, err := sess.Typed.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err == nil {
		svcs = l.Items
	} else {
		for _, ns := range commonNamespaces {
			if l, e := sess.Typed.CoreV1().Services(ns).List(ctx, metav1.ListOptions{}); e == nil {
				svcs = append(svcs, l.Items...)
			}
		}
	}
	var out []Target
	for _, s := range svcs {
		t, ok := ScoreGrafana(s)
		if !ok {
			continue
		}
		t.AdminSecret = adminSecret(ctx, sess, s)
		out = append(out, t)
	}
	cands, _ := prom.Detect(ctx, sess)
	for _, c := range cands {
		port, err := strconv.Atoi(c.Port)
		if err != nil || c.Mode != "service" {
			continue
		}
		scheme := c.Scheme
		if scheme == "" {
			scheme = "http"
		}
		out = append(out, Target{Kind: KindPrometheus, Namespace: c.Namespace, Service: c.Service, Port: port, Scheme: scheme, Score: c.Score})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == KindGrafana
		}
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Namespace+"/"+out[i].Service < out[j].Namespace+"/"+out[j].Service
	})
	if len(out) > 16 {
		out = out[:16]
	}
	return out, nil
}

// adminSecret resolves the reference: the pod spec first, then conventional names checked with a
// metadata-only GET (the Secret's data never reaches the engine).
func adminSecret(ctx context.Context, sess *cluster.Session, s corev1.Service) *SecretRef {
	if len(s.Spec.Selector) > 0 {
		pods, err := sess.Typed.CoreV1().Pods(s.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(s.Spec.Selector).String(), Limit: 5})
		if err == nil {
			if ref := AdminSecretFromPods(pods.Items); ref != nil {
				return ref
			}
		}
	}
	if sess.Meta == nil {
		return nil
	}
	for _, name := range SecretCandidates(s) {
		if _, err := sess.Meta.Resource(secretsGVR).Namespace(s.Namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
			return &SecretRef{Namespace: s.Namespace, Name: name}
		}
	}
	return nil
}
