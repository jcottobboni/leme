package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsProduction(t *testing.T) {
	cases := []struct {
		server string
		names  []string
		want   bool
	}{
		{"https://api.ocp-c01.example:6443", []string{"inventory-prd/api-ocp-c01-acme-dc1:6443/alice"}, true},
		{"https://api-ocp-prd-acme-dc1:6443", []string{"portal-stag/api-x:6443/alice"}, true},
		{"https://10.0.0.1", []string{"my-prod-cluster"}, true},
		{"", []string{"production"}, true},
		{"https://10.0.0.1:6443", []string{"prod01"}, true},
		{"https://10.0.0.1:6443", []string{"acme-prd2"}, true},
		{"https://10.0.0.1:6443", []string{"admin", "payroll-prod", "admin-user", "default"}, true},
		{"https://10.0.0.1:6443", []string{"ctx", "c", "u", "app-producao"}, true},
		{"https://10.0.0.1:6443", []string{"ocpprd"}, true},
		{"https://10.0.0.1:6443", []string{"k8sprod01"}, true},
		{"https://10.0.0.1:6443", []string{"app-produccion"}, true},
		{"https://api.k8sprd.example:6443", []string{"ctx"}, true},
		{"https://api-ocp-dev-acme-example:6443", []string{"hr-dsv/api-ocp-dev:6443/alice"}, false},
		{"https://127.0.0.1:41234", []string{"kind-leme-dev"}, false},
		{"https://10.0.0.2", []string{"product-catalog-dev"}, false},
		{"https://10.0.0.3", []string{"sprdx", "delivery"}, false},
		{"https://10.0.0.4", []string{"products", "reproducible", "olive"}, false},
	}
	for _, c := range cases {
		if got := IsProduction(c.server, c.names...); got != c.want {
			t.Errorf("IsProduction(%q,%v)=%v want %v", c.server, c.names, got, c.want)
		}
	}
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		name, server string
		want         bool
	}{
		{"kind-dev", "https://127.0.0.1:40000", true},
		{"minikube", "https://192.168.49.2:8443", true},
		{"minikube", "https://127.0.0.1:32771", true},
		{"colima", "https://172.17.0.2:6443", true},
		{"docker-desktop", "https://kubernetes.docker.internal:6443", true},
		{"hr-dsv/api:6443/alice", "https://api-ocp-dev-acme-example:6443", false},
		{"kind-fake", "https://10.1.2.3:6443", false},
		// Loopback alone may be an SSH tunnel or kubectl proxy to a remote cluster.
		{"anything", "https://localhost:6443", false},
		{"remote-via-tunnel", "https://127.0.0.1:6443", false},
		// 172.* outside 172.16.0.0/12 is public address space.
		{"minikube-x", "https://172.217.1.1:6443", false},
		{"kind-x", "https://kind.example.com:6443", false},
	}
	for _, c := range cases {
		if got := IsLocal(c.name, c.server); got != c.want {
			t.Errorf("IsLocal(%q,%q)=%v want %v", c.name, c.server, got, c.want)
		}
	}
}

func TestContextIDStable(t *testing.T) {
	if ContextID("a/b:6443/c") != ContextID("a/b:6443/c") || len(ContextID("x")) != 12 {
		t.Fatal("unstable id")
	}
}

func TestLoaderSources(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, ctx string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		cfg := "apiVersion: v1\nkind: Config\nclusters:\n- {name: " + ctx + ", cluster: {server: \"https://10.0.0.1:6443\"}}\n" +
			"users:\n- {name: " + ctx + ", user: {token: x}}\ncontexts:\n- {name: " + ctx + ", context: {cluster: " + ctx + ", user: " + ctx + "}}\n"
		if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("base/config", "base")
	single := write("single.kubeconfig", "single")
	clients := filepath.Join(dir, "clients")
	write("clients/acme/prd.yaml", "acme-prd")
	write("clients/acme/stg.yml", "acme-stg")
	write("clients/other/config", "other")
	write("clients/acme/prd.yaml.bak-2026-10-26", "stale")
	write("clients/.hidden/config", "hidden")
	write("clients/notes.txt", "notes")

	t.Setenv("KUBECONFIG", base)
	l := NewLoader(nil)
	l.SetSources([]string{clients, single, filepath.Join(dir, "gone")})

	ctxs, err := l.Contexts()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range ctxs {
		names = append(names, c.Name)
	}
	if got, want := strings.Join(names, ","), "acme-prd,acme-stg,base,other,single"; got != want {
		t.Fatalf("contexts = %s, want %s", got, want)
	}
	if got := l.Paths(); len(got) != 5 || got[0] != base {
		t.Fatalf("paths must keep the base first and skip backups, hidden and missing entries: %v", got)
	}
	l.SetSources(nil)
	if got := l.Paths(); len(got) != 1 {
		t.Fatalf("clearing the sources leaves the base only: %v", got)
	}
}

func TestExpandSourceBounds(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "a", "ok.yaml"), filepath.Join(deep, "too-deep.yaml")} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := ExpandSource(dir); len(got) != 1 || filepath.Base(got[0]) != "ok.yaml" {
		t.Fatalf("depth limit: %v", got)
	}
	l := NewLoader(nil)
	l.SetSources([]string{dir})
	first := l.Paths()
	if err := os.WriteFile(filepath.Join(dir, "new.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := l.Paths(); len(got) != len(first) {
		t.Fatalf("a scan must be reused within its TTL: %v -> %v", first, got)
	}
	l.SetSources([]string{dir}) // saving the sources drops the cache
	if got := l.Paths(); len(got) != len(first)+1 {
		t.Fatalf("SetSources must rescan: %v", got)
	}
}
