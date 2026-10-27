package api

import (
	"net/http"
	"testing"
)

// Writes made in the app are audited with source "ui", including the refused ones.
func TestUIWritesAreAudited(t *testing.T) {
	s, ro, _ := argoFixture(t, true, "Succeeded")
	body := `{"group":"apps","version":"v1","resource":"deployments","namespace":"guestbook","name":"web","replicas":2}`
	if w := argoCall(s.scale, ro, http.MethodPost, body, nil); w.Code != http.StatusForbidden {
		t.Fatalf("read-only scale: %d %s", w.Code, w.Body)
	}
	s2, rw, _ := argoFixture(t, false, "Succeeded")
	s2.audit = s.audit // one log for both sessions
	if w := argoCall(s2.scale, rw, http.MethodPost, body, nil); w.Code != 200 {
		t.Fatalf("scale: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s2.restart, rw, http.MethodPost, body, nil); w.Code != 200 {
		t.Fatalf("restart: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s2.apply, rw, http.MethodPost, `{"yaml":"apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web, namespace: guestbook}\n","dryRun":true}`, nil); w.Code == 0 {
		t.Fatal("no answer")
	}
	entries, err := s.audit.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.Source != "ui" {
			t.Errorf("unexpected source %+v", e)
		}
		got = append(got, e.Action+":"+e.Result+":"+e.Object)
	}
	want := []string{"restart:ok:Deployment/guestbook/web", "scale:ok:Deployment/guestbook/web", "scale:denied:Deployment/guestbook/web"}
	if len(got) != len(want) {
		t.Fatalf("audit (dry-runs are not changes): %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("audit = %v, want %v", got, want)
		}
	}
	if entries[0].Context != "kind-test" || entries[1].Detail != "replicas=2" {
		t.Fatalf("entry fields: %+v", entries[:2])
	}
}

func TestYAMLObjectName(t *testing.T) {
	if got := yamlObjectName("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: c\n  namespace: demo\n", ""); got != "ConfigMap/demo/c" {
		t.Fatal(got)
	}
	if got := yamlObjectName(": not yaml [", ""); got != "" {
		t.Fatal(got)
	}
}
