package anonymize

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	m := New(true)
	m.Learn(Namespace, "payroll-prd")
	m.Learn(Object, "payroll-api")
	in := `Deployment payroll-api in payroll-prd pulls registry.acme.corp/team/payroll-api:1.2.3,
calls db.payroll.internal at 10.20.30.40, owner ops@acme.com.br. Labels app.kubernetes.io/name=payroll-api.
ReplicaSet payroll-api-86b9596b76 and Pod payroll-api-86b9596b76-h4hht derive from it; webhook payroll-apiary is a different name.`
	out := m.Text(in)
	for _, real := range []string{"payroll-prd", "registry.acme.corp", "db.payroll.internal", "10.20.30.40", "ops@acme.com.br"} {
		if strings.Contains(out, real) {
			t.Errorf("%q leaked:\n%s", real, out)
		}
	}
	if strings.Contains(out, " payroll-api ") || strings.Contains(out, "=payroll-api.") {
		t.Errorf("object name leaked:\n%s", out)
	}
	if strings.Contains(out, "payroll-api-86b9596b76") {
		t.Errorf("derived (generated) names must not leak the owner's name:\n%s", out)
	}
	for _, kept := range []string{"app.kubernetes.io/name", "payroll-apiary", "1.2.3", "-86b9596b76-h4hht"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q must be kept (public or a different identifier):\n%s", kept, out)
		}
	}
	if back := m.Restore(out); back != in {
		t.Errorf("round trip failed:\n%s\n---\n%s", in, back)
	}
}

func TestDisabledAndStopNames(t *testing.T) {
	off := New(false)
	off.Learn(Namespace, "secret-ns")
	if off.Text("secret-ns") != "secret-ns" {
		t.Fatal("disabled map must pass through")
	}
	on := New(true)
	on.Learn(Namespace, "default")
	on.Learn(Object, "v1.2")
	if got := on.Text("namespace default, version v1.2"); got != "namespace default, version v1.2" {
		t.Fatalf("common names must stay: %q", got)
	}
}

func TestStreamingRestore(t *testing.T) {
	m := New(true)
	alias := m.Learn(Object, "payroll-api")
	reply := "The Deployment " + alias + " is failing; check " + alias + " now."
	// Split the reply at every possible point, including inside the alias.
	for cut := 1; cut < len(reply); cut++ {
		r := m.NewRestorer()
		got := r.Push(reply[:cut]) + r.Push(reply[cut:]) + r.Flush()
		if want := "The Deployment payroll-api is failing; check payroll-api now."; got != want {
			t.Fatalf("cut %d: %q", cut, got)
		}
	}
	// Many tiny deltas.
	r := m.NewRestorer()
	var b strings.Builder
	for _, c := range reply {
		b.WriteString(r.Push(string(c)))
	}
	b.WriteString(r.Flush())
	if !strings.Contains(b.String(), "payroll-api is failing") || strings.Contains(b.String(), alias) {
		t.Fatalf("char stream: %q", b.String())
	}
}
