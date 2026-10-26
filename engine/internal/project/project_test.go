package project

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func TestPodImagePullBackOff(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: broken, namespace: demo, uid: u1}
spec: {containers: [{name: c}]}
status:
  phase: Pending
  containerStatuses:
  - name: c
    ready: false
    restartCount: 0
    state: {waiting: {reason: ImagePullBackOff}}
`))
	if r.Cells["status"] != "ImagePullBackOff" || r.Health != "error" || r.Cells["ready"] != "0/1" {
		t.Fatalf("got %+v", r)
	}
	if c := r.Cells["containers"].([]ContainerState); len(c) != 1 || c[0].State != "error" {
		t.Fatalf("containers %+v", c)
	}
}

func TestPodContainerStates(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: p, namespace: demo, uid: u3}
spec:
  initContainers: [{name: setup}]
  containers: [{name: app}, {name: side}]
status:
  phase: Pending
  initContainerStatuses:
  - {name: setup, ready: false, restartCount: 0, state: {running: {}}}
  containerStatuses:
  - {name: app, ready: false, restartCount: 0, state: {waiting: {reason: PodInitializing}}}
`))
	c := r.Cells["containers"].([]ContainerState)
	want := []ContainerState{{"setup", "init"}, {"app", "waiting"}, {"side", "waiting"}}
	if len(c) != len(want) {
		t.Fatalf("got %+v", c)
	}
	for i := range want {
		if c[i] != want[i] {
			t.Fatalf("got %+v, want %+v", c, want)
		}
	}
}

func TestPodRunning(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: web, namespace: demo, uid: u2}
spec: {containers: [{name: a}, {name: b}], nodeName: n1}
status:
  phase: Running
  containerStatuses:
  - {name: a, ready: true, restartCount: 2, state: {running: {}}}
  - {name: b, ready: true, restartCount: 1, state: {running: {}}}
`))
	if r.Cells["status"] != "Running" || r.Health != "ok" || r.Cells["restarts"] != int64(3) || r.Cells["ready"] != "2/2" {
		t.Fatalf("got %+v", r)
	}
}

func TestDeploymentStates(t *testing.T) {
	cases := map[string]string{
		`{kind: Deployment, metadata: {name: a, uid: a}, spec: {replicas: 3}, status: {readyReplicas: 3, updatedReplicas: 3, availableReplicas: 3}}`:                           "Healthy",
		`{kind: Deployment, metadata: {name: b, uid: b}, spec: {replicas: 1}, status: {}}`:                                                                                     "Pending",
		`{kind: Deployment, metadata: {name: c, uid: c}, spec: {replicas: 3}, status: {readyReplicas: 1, updatedReplicas: 3, availableReplicas: 1}}`:                           "Scaling",
		`{kind: Deployment, metadata: {name: d, uid: d}, spec: {replicas: 1}, status: {conditions: [{type: Progressing, status: "False", reason: ProgressDeadlineExceeded}]}}`: "Blocked",
		`{kind: Deployment, metadata: {name: e, uid: e}, spec: {replicas: 0}, status: {}}`:                                                                                     "Scaled to 0",
	}
	for y, want := range cases {
		if got := Project(obj(t, y)).Cells["status"]; got != want {
			t.Errorf("%s: got %v want %v", y, got, want)
		}
	}
}

func TestRouteURL(t *testing.T) {
	mk := func(api, host, path string, tls bool) *unstructured.Unstructured {
		spec := map[string]any{"host": host}
		if path != "" {
			spec["path"] = path
		}
		if tls {
			spec["tls"] = map[string]any{"termination": "edge"}
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": api, "kind": "Route", "spec": spec}}
	}
	cases := []struct {
		u    *unstructured.Unstructured
		want string
	}{
		{mk("route.openshift.io/v1", "web.apps.example.com", "/app", true), "https://web.apps.example.com/app"},
		{mk("route.openshift.io/v1", "web.apps.example.com", "", false), "http://web.apps.example.com"},
		{mk("route.openshift.io/v1", "bank.com@evil.com", "", true), ""},
		{mk("route.openshift.io/v1", "evil.com/x y", "", true), ""},
		{mk("route.openshift.io/v1", "ok.example.com", " javascript:alert(1)", true), "https://ok.example.com"},
		{mk("serving.knative.dev/v1", "web.example.com", "", true), ""},
	}
	for _, c := range cases {
		if got := RouteURL(c.u); got != c.want {
			t.Errorf("%v: got %q want %q", c.u.Object["spec"], got, c.want)
		}
	}
}

func TestAppOf(t *testing.T) {
	cases := []struct {
		labels, ann map[string]string
		want        string
	}{
		{map[string]string{"app.kubernetes.io/part-of": "shop", "app": "web"}, nil, "shop"},
		{map[string]string{"app": "web"}, map[string]string{"meta.helm.sh/release-name": "rel"}, "rel"},
		{map[string]string{"app.kubernetes.io/instance": "i", "app": "web"}, nil, "i"},
		{map[string]string{"k8s-app": "kube-dns"}, nil, "kube-dns"},
		{nil, nil, ""},
	}
	for _, c := range cases {
		if got := AppOf(c.labels, c.ann); got != c.want {
			t.Errorf("AppOf(%v, %v) = %q, want %q", c.labels, c.ann, got, c.want)
		}
	}
}
