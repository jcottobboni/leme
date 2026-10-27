package redact

import (
	"strings"
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

func TestObject(t *testing.T) {
	in := obj(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  managedFields: [{manager: kubectl}]
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"big":"json"}'
    vault.example.com/token: s.abcdef
    note: `+strings.Repeat("x", 300)+`
spec:
  template:
    spec:
      containers:
      - name: app
        image: nginx
        env:
        - {name: DB_PASSWORD, value: hunter2}
        - name: FROM_SECRET
          valueFrom: {secretKeyRef: {name: s, key: k}}
`)
	out := Object(in)
	y, _ := yaml.Marshal(out.Object)
	s := string(y)
	for _, bad := range []string{"hunter2", "managedFields", "last-applied", "s.abcdef", strings.Repeat("x", 250)} {
		if strings.Contains(s, bad) {
			t.Errorf("leaked %q:\n%s", bad, s)
		}
	}
	if !strings.Contains(s, "secretKeyRef") || !strings.Contains(s, "DB_PASSWORD") {
		t.Errorf("structure lost:\n%s", s)
	}
	if len(in.GetManagedFields()) == 0 {
		t.Error("input must not be modified")
	}
}

func TestSecretAndConfigMap(t *testing.T) {
	sec := Object(obj(t, "apiVersion: v1\nkind: Secret\nmetadata: {name: s}\ndata: {password: aHVudGVyMg==}\nstringData: {token: abc}\n"))
	cm := Object(obj(t, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\ndata: {app.properties: 'db.password=hunter2'}\n"))
	for _, o := range []*unstructured.Unstructured{sec, cm} {
		y, _ := yaml.Marshal(o.Object)
		if strings.Contains(string(y), "aHVudGVyMg") || strings.Contains(string(y), "hunter2") || strings.Contains(string(y), "abc") {
			t.Errorf("value leaked:\n%s", y)
		}
		if !strings.Contains(string(y), Mask) {
			t.Errorf("keys must stay with masked values:\n%s", y)
		}
	}
}

func TestText(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdefghijklmnop":                         "abcdefghijklmnop",
		"connecting with password=hunter2&user=x":                         "hunter2",
		`{"api_key": "sk-123456789"}`:                                      "sk-123456789",
		"token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijk": "eyJhbGciOiJIUzI1NiJ9",
		"key AKIAABCDEFGHIJKLMNOP used":                                    "AKIAABCDEFGHIJKLMNOP",
		"dsn postgres://app:hunter2@db:5432/x":                             "hunter2",
		"oc token sha256~abcdefghijklmnopqrstuvwxyz":                        "sha256~abcdef",
	}
	for in, secret := range cases {
		if out := Text(in); strings.Contains(out, secret) {
			t.Errorf("Text(%q) = %q still contains %q", in, out, secret)
		}
	}
	if Text("pod web-1 started in 3s") != "pod web-1 started in 3s" {
		t.Error("plain text must not change")
	}
}
