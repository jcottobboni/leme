package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Prometheus("kind"); ok {
		t.Fatal("empty store must have no config")
	}
	want := Prometheus{Mode: "service", Namespace: "monitoring", Service: "prometheus-server", Port: "80"}
	if err := s.SetPrometheus("kind", want); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json mode %v, want 0600", st.Mode().Perm())
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s2.Prometheus("kind"); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if err := s2.SetPrometheus("kind", Prometheus{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Prometheus("kind"); ok {
		t.Fatal("empty mode must clear the config")
	}
}
