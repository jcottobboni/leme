package helm

import (
	"strings"
	"testing"
)

const manifest = `---
# Source: hello/templates/config.yaml
apiVersion: v1
kind: Secret
metadata:
  name: h1-hello
type: Opaque
stringData:
  password: "s3cr3t-value"
data:
  token: c2VjcmV0
---
# Source: hello/templates/config.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: h1-hello
data:
  MESSAGE: "hello"
`

func TestMaskManifest(t *testing.T) {
	out := MaskManifest(manifest)
	if strings.Contains(out, "s3cr3t-value") || strings.Contains(out, "c2VjcmV0") {
		t.Fatalf("secret values leaked:\n%s", out)
	}
	if !strings.Contains(out, "password: <hidden>") || !strings.Contains(out, "token: <hidden>") {
		t.Fatalf("secret keys must stay visible:\n%s", out)
	}
	// Non-secret documents and Helm's source comments are untouched.
	if !strings.Contains(out, "# Source: hello/templates/config.yaml\napiVersion: v1\nkind: ConfigMap") || !strings.Contains(out, `MESSAGE: "hello"`) {
		t.Fatalf("non-secret content changed:\n%s", out)
	}
	if MaskManifest("kind: ConfigMap\n") != "kind: ConfigMap\n" {
		t.Fatal("manifest without secrets must be returned unchanged")
	}
	for _, variant := range []string{
		"kind: Secret # comment\nmetadata: {name: a}\ndata: {k: c2VjcmV0}\n",
		`{"apiVersion":"v1","kind":"Secret","metadata":{"name":"a"},"data":{"k":"c2VjcmV0"}}`,
		"apiVersion: v1\nkind: List\nitems:\n  - {apiVersion: v1, kind: Secret, metadata: {name: a}, data: {k: c2VjcmV0}}\n",
		"kind: 'Secret'\nmetadata: {name: a}\nstringData: {k: secret}\n",
	} {
		if got := MaskManifest("---\n" + variant); strings.Contains(got, "c2VjcmV0") || strings.Contains(got, "k: secret") {
			t.Errorf("variant leaked:\n%s\n→\n%s", variant, got)
		}
	}
	broken := "---\nkind: Secret\nstringData: {password: [unterminated\n"
	if got := MaskManifest(broken); strings.Contains(got, "unterminated") {
		t.Fatalf("unparseable secret must be hidden entirely: %q", got)
	}
}

func TestObjects(t *testing.T) {
	objs := objects(manifest, "demo", nil)
	if len(objs) != 2 || objs[0].Kind != "Secret" || objs[1].Name != "h1-hello" {
		t.Fatalf("%+v", objs)
	}
}
