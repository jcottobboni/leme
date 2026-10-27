package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func grafanaSession(readOnly bool) *cluster.Session {
	g := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "m5-grafana", Name: "m5-grafana", Labels: map[string]string{"app.kubernetes.io/name": "grafana"}},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "g"}, Ports: []corev1.ServicePort{{Name: "http", Port: 3000}}},
	}
	tls := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "m5-grafana", Name: "m5-grafana-tls", Labels: map[string]string{"app": "grafana"}},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "g"}, Ports: []corev1.ServicePort{{Name: "https", Port: 443}}},
	}
	web := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "web"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}, Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
	}
	return cluster.NewStaticSession(kube.Context{ID: "c1", Name: "c1"}, fake.NewSimpleClientset(g, tls, web), nil, nil, readOnly)
}

func grafanaPost(s *Server, sess *cluster.Session, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/clusters/c1/grafana/open", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.grafanaOpen(w, r, sess)
	return w
}

func TestGrafanaOpenRejectsUndetectedTargets(t *testing.T) {
	s := newTestServer(t)
	sess := grafanaSession(false)
	cases := []struct {
		name, body string
		want       int
	}{
		{"bad json", `{`, http.StatusBadRequest},
		{"missing fields", `{"namespace":"m5-grafana"}`, http.StatusBadRequest},
		{"arbitrary service", `{"namespace":"demo","service":"web","port":80}`, http.StatusNotFound},
		{"grafana on another port", `{"namespace":"m5-grafana","service":"m5-grafana","port":22}`, http.StatusNotFound},
		{"https target", `{"namespace":"m5-grafana","service":"m5-grafana-tls","port":443}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := grafanaPost(s, sess, c.body); w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	if n := len(s.forwards.List()); n != 0 {
		t.Fatalf("no forward may start: %d", n)
	}
	// A detected target still goes through the session's write guard (tunnels bypass read-only).
	if w := grafanaPost(s, grafanaSession(true), `{"namespace":"m5-grafana","service":"m5-grafana","port":3000}`); w.Code != http.StatusForbidden {
		t.Fatalf("read-only: got %d (%s)", w.Code, w.Body.String())
	}
}

func TestGrafanaTargetsShape(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/clusters/c1/grafana/targets", nil)
	w := httptest.NewRecorder()
	s.grafanaTargets(w, r, grafanaSession(true))
	var out struct {
		Targets []map[string]any `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %v %s", w.Code, err, w.Body.String())
	}
	if len(out.Targets) != 2 || out.Targets[0]["service"] != "m5-grafana" || out.Targets[0]["kind"] != "grafana" || out.Targets[0]["port"].(float64) != 3000 {
		t.Fatalf("targets: %+v", out.Targets)
	}
	if _, ok := out.Targets[0]["forward"]; ok {
		t.Fatal("no forward expected")
	}
}
