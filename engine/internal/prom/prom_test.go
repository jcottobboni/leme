package prom

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/jcottobboni/leme/engine/internal/settings"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildQuotesAndScopes(t *testing.T) {
	qs, unit, err := build(Scope{Kind: "pod", Namespace: "demo", Name: `web-1`}, "cpu", 2*time.Minute, "")
	if err != nil || unit != "cores" {
		t.Fatal(err, unit)
	}
	want := `sum by (container) (rate(container_cpu_usage_seconds_total{namespace="demo",pod="web-1",container!="",container!="POD"}[120s]))`
	if qs[0].expr != want {
		t.Fatalf("got  %s\nwant %s", qs[0].expr, want)
	}
	// Quotes in user input must not break out of the string literal.
	qs, _, _ = build(Scope{Kind: "namespace", Namespace: `x"} or vector(1) #`}, "memory", time.Minute, "")
	if !strings.Contains(qs[0].expr, `namespace="x\"} or vector(1) #"`) {
		t.Fatalf("namespace not escaped: %s", qs[0].expr)
	}
	qs, _, err = build(Scope{Kind: "workload", Namespace: "demo", Pods: []string{"web-a.b", "web-c"}}, "cpu", time.Minute, "")
	if err != nil || !strings.Contains(qs[0].expr, `pod=~"web-a\\.b|web-c"`) {
		t.Fatalf("workload regex: %v %s", err, qs[0].expr)
	}
	if _, _, err := build(Scope{Kind: "workload", Namespace: "demo", Pods: []string{"bad|.*"}}, "cpu", time.Minute, ""); err == nil {
		t.Fatal("invalid pod names must be rejected")
	}
	qs, _, _ = build(Scope{Kind: "node", Name: "n1"}, "network", time.Minute, "instance")
	if len(qs) != 2 || !strings.Contains(qs[0].expr, `id="/",instance="n1"`) || qs[1].legend != "=tx" {
		t.Fatalf("node network: %+v", qs)
	}
}

func TestSteps(t *testing.T) {
	step, window := Steps(15 * time.Minute)
	if step != 15*time.Second || window != 2*time.Minute {
		t.Fatal(step, window)
	}
	step, window = Steps(7 * 24 * time.Hour)
	if step != 3024*time.Second || window != step {
		t.Fatal(step, window)
	}
}

func TestParse(t *testing.T) {
	b := []byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"container":"nginx"},"values":[[1700000000,"0.5"],[1700000015,"NaN"],[1700000030,"0.25"]]}]}}`)
	s, err := parse(b, "container")
	if err != nil || len(s) != 1 || s[0].Name != "nginx" || len(s[0].Points) != 3 {
		t.Fatalf("%v %+v", err, s)
	}
	if _, err := parse([]byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`), ""); err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatal(err)
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://thanos.apps.example.com", "http://127.0.0.1:9090", "http://localhost:9090/prom"} {
		if _, err := ValidateURL(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://prometheus.example.com", "ftp://x", "file:///etc/passwd", "not a url"} {
		if _, err := ValidateURL(bad); err == nil {
			t.Errorf("%s must be rejected", bad)
		}
	}
}

func TestScore(t *testing.T) {
	svc := func(ns, name string, ports ...corev1.ServicePort) corev1.Service {
		return corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Spec: corev1.ServiceSpec{Ports: ports}}
	}
	if _, ok := score(svc("monitoring", "prometheus-alertmanager", corev1.ServicePort{Port: 9093})); ok {
		t.Fatal("alertmanager is not a query endpoint")
	}
	c, ok := score(svc("monitoring", "prometheus-server", corev1.ServicePort{Name: "http", Port: 80}))
	if !ok || c.Port != "80" || c.Score < 5 {
		t.Fatalf("%+v %v", c, ok)
	}
	th, ok := score(svc("openshift-monitoring", "thanos-querier", corev1.ServicePort{Name: "web", Port: 9091}))
	if !ok || th.Scheme != "https" {
		t.Fatalf("%+v", th)
	}
}

func TestTokenAllowed(t *testing.T) {
	cases := []struct {
		api, host string
		want      bool
	}{
		{"https://api.ocp.corp.example.com:6443", "thanos-querier-openshift-monitoring.apps.ocp.corp.example.com", true},
		{"https://api.ocp.corp.example.com:6443", "api.ocp.corp.example.com", true},
		{"https://api.ocp.corp.example.com:6443", "evil.example.com", false},
		{"https://api.ocp.corp.example.com:6443", "ocp.corp.example.com.evil.com", false},
		{"https://api.example.com", "x.com", false},
		{"https://10.0.0.1:6443", "prometheus.10.0.0.1.nip.io", false},
		{"https://k8s.internal:6443", "prom.k8s.internal", false},
	}
	for _, c := range cases {
		if got := TokenAllowed(c.api, c.host); got != c.want {
			t.Errorf("%s → %s: got %v want %v", c.api, c.host, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := []settings.Prometheus{
		{},
		{Mode: "service", Namespace: "monitoring", Service: "prometheus-server", Port: "80"},
		{Mode: "service", Namespace: "monitoring", Service: "prom", Port: "web", Scheme: "https", Path: "/prometheus"},
		{Mode: "url", URL: "https://thanos.apps.example.com", SendToken: true},
	}
	for _, c := range ok {
		if err := Validate(c); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []settings.Prometheus{
		{Mode: "service", Namespace: "monitoring", Service: "prom", Path: "/../../../api/v1/secrets"},
		{Mode: "service", Namespace: "Bad NS", Service: "prom"},
		{Mode: "service", Namespace: "monitoring", Service: "prom", Port: "80/../x"},
		{Mode: "service", Namespace: "monitoring", Service: "prom", SendToken: true},
		{Mode: "url", URL: "https://user:pass@thanos.example.com"},
		{Mode: "other"},
	}
	for _, c := range bad {
		if err := Validate(c); err == nil {
			t.Errorf("%+v must be rejected", c)
		}
	}
}

func TestBuildReferenceLines(t *testing.T) {
	qs, _, _ := build(Scope{Kind: "node", Name: "n1"}, "cpu", time.Minute, "node")
	refs := map[string]string{}
	for _, x := range qs {
		if x.ref != "" {
			refs[x.ref] = x.expr
		}
	}
	if len(refs) != 4 || !strings.Contains(refs["capacity"], `machine_cpu_cores{node="n1"}`) || !strings.Contains(refs["allocatable"], `kube_node_status_allocatable{resource="cpu",node="n1"}`) {
		t.Fatalf("node refs: %v", refs)
	}
	qs, _, _ = build(Scope{Kind: "cluster"}, "memory", time.Minute, "node")
	if !strings.Contains(qs[1].expr, "sum(machine_memory_bytes)") {
		t.Fatalf("cluster capacity: %s", qs[1].expr)
	}
	qs, _, _ = build(Scope{Kind: "pod", Namespace: "d", Name: "p"}, "cpu", time.Minute, "")
	if len(qs) != 3 || qs[1].ref != "requests" || !strings.Contains(qs[1].expr, `resource="cpu",namespace="d",pod="p"`) {
		t.Fatalf("pod refs: %+v", qs)
	}
	if qs, _, _ = build(Scope{Kind: "node", Name: "n1"}, "network", time.Minute, "node"); len(qs) != 2 {
		t.Fatalf("network has no refs: %+v", qs)
	}
}
