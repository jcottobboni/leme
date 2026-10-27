package grafana

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	metafake "k8s.io/client-go/metadata/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

const marker = "m5-marker-S3cr3t-do-not-show"

func svc(ns, name string, labels map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": name}, Ports: ports},
	}
}

func port(name string, p int32) corev1.ServicePort { return corev1.ServicePort{Name: name, Port: p} }

func TestScoreGrafana(t *testing.T) {
	cases := []struct {
		name  string
		s     *corev1.Service
		ok    bool
		port  int
		score int
	}{
		{"recommended label + 3000", svc("m", "g", map[string]string{"app.kubernetes.io/name": "grafana"}, port("", 3000)), true, 3000, 8},
		{"legacy label + named http", svc("m", "g", map[string]string{"app": "grafana"}, port("http", 80)), true, 80, 6},
		{"3000 beats http", svc("m", "g", map[string]string{"app": "grafana"}, port("http", 80), port("x", 3000)), true, 3000, 7},
		{"name only is not enough", svc("m", "grafana", nil, port("", 3000)), false, 0, 0},
		{"operator is not grafana", svc("m", "op", map[string]string{"app.kubernetes.io/name": "grafana-operator"}, port("", 3000)), false, 0, 0},
		{"no web port", svc("m", "g", map[string]string{"app": "grafana"}, port("metrics", 9100)), false, 0, 0},
		{"headless", svc("m", "g-headless", map[string]string{"app": "grafana"}, port("", 3000)), false, 0, 0},
	}
	for _, c := range cases {
		got, ok := ScoreGrafana(*c.s)
		if ok != c.ok || got.Port != c.port || got.Score != c.score {
			t.Errorf("%s: got %+v ok=%v", c.name, got, ok)
		}
		if ok && (got.Kind != KindGrafana || got.Scheme != "http") {
			t.Errorf("%s: kind/scheme %+v", c.name, got)
		}
	}
	tls, ok := ScoreGrafana(*svc("m", "g", map[string]string{"app": "grafana"}, port("https", 443)))
	if !ok || tls.Scheme != "https" {
		t.Fatalf("https port: %+v %v", tls, ok)
	}
}

func TestAdminSecretFromPods(t *testing.T) {
	envPod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "m"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Env: []corev1.EnvVar{
			{Name: "GF_SECURITY_ADMIN_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "from-env"}, Key: "pw"}}},
		},
	}}}}
	if ref := AdminSecretFromPods([]corev1.Pod{envPod}); ref == nil || *ref != (SecretRef{"m", "from-env"}) {
		t.Fatalf("env ref: %+v", ref)
	}
	filePod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "m"}, Spec: corev1.PodSpec{
		Containers: []corev1.Container{{
			Env:          []corev1.EnvVar{{Name: "GF_SECURITY_ADMIN_PASSWORD__FILE", Value: "/etc/admin/password"}},
			VolumeMounts: []corev1.VolumeMount{{Name: "adm", MountPath: "/etc/admin"}},
		}},
		Volumes: []corev1.Volume{{Name: "adm", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "from-file"}}}},
	}}
	if ref := AdminSecretFromPods([]corev1.Pod{filePod}); ref == nil || ref.Name != "from-file" {
		t.Fatalf("file ref: %+v", ref)
	}
	// A literal value is a credential in plain text: it is neither a reference nor returned.
	literal := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Env: []corev1.EnvVar{{Name: "GF_SECURITY_ADMIN_PASSWORD", Value: marker}}}}}}
	if ref := AdminSecretFromPods([]corev1.Pod{literal}); ref != nil {
		t.Fatalf("literal must not yield a ref: %+v", ref)
	}
}

func session(t *testing.T, objs ...runtime.Object) (*cluster.Session, *fake.Clientset) {
	t.Helper()
	typed := fake.NewSimpleClientset(objs...)
	sess := cluster.NewStaticSession(kube.Context{ID: "c1", Name: "c1"}, typed, nil, nil, false)
	scheme := runtime.NewScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var metas []runtime.Object
	for _, o := range objs {
		if s, ok := o.(*corev1.Secret); ok {
			metas = append(metas, &metav1.PartialObjectMetadata{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
				ObjectMeta: metav1.ObjectMeta{Namespace: s.Namespace, Name: s.Name},
			})
		}
	}
	sess.Meta = metafake.NewSimpleMetadataClient(scheme, metas...)
	return sess, typed
}

func TestDetectReferencesAdminSecretWithoutValues(t *testing.T) {
	g := svc("m5-grafana", "m5-grafana", map[string]string{"app.kubernetes.io/name": "grafana"}, port("http", 3000))
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "m5-grafana", Name: "m5-grafana-admin"}, Data: map[string][]byte{"admin-password": []byte(marker)}, StringData: map[string]string{"x": marker}}
	p := svc("monitoring", "prometheus-server", map[string]string{"app.kubernetes.io/name": "prometheus"}, port("", 80))
	other := svc("demo", "web", map[string]string{"app": "web"}, port("http", 80))
	sess, typed := session(t, g, secret, p, other)

	ts, err := Detect(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Kind != KindGrafana || ts[1].Kind != KindPrometheus {
		t.Fatalf("targets: %+v", ts)
	}
	if ts[0].AdminSecret == nil || *ts[0].AdminSecret != (SecretRef{"m5-grafana", "m5-grafana-admin"}) {
		t.Fatalf("admin secret ref: %+v", ts[0].AdminSecret)
	}
	if ts[1].Service != "prometheus-server" || ts[1].Port != 80 || ts[1].Scheme != "http" || ts[1].AdminSecret != nil {
		t.Fatalf("prometheus: %+v", ts[1])
	}
	b, _ := json.Marshal(ts)
	if strings.Contains(string(b), marker) || strings.Contains(string(b), "admin-password") {
		t.Fatalf("response leaks secret data: %s", b)
	}
	// The typed client (which returns data) is never asked for Secrets.
	for _, a := range typed.Actions() {
		if a.GetResource().Resource == "secrets" {
			t.Fatalf("typed client read secrets: %v", a)
		}
	}
	if _, ok := Find(ts, "m5-grafana", "m5-grafana", 3000); !ok {
		t.Fatal("find detected")
	}
	if _, ok := Find(ts, "demo", "web", 80); ok {
		t.Fatal("find must not return undetected services")
	}
}

func TestDetectEmpty(t *testing.T) {
	sess, _ := session(t, svc("demo", "web", nil, port("http", 80)))
	ts, err := Detect(context.Background(), sess)
	if err != nil || len(ts) != 0 {
		t.Fatalf("%v %+v", err, ts)
	}
}
