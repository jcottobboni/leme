package kube

import "testing"

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

func TestSharedProdServer(t *testing.T) {
	ctxs := []Context{
		{Name: "acme/prd-portal", Server: "https://api.ocp-c01.acme:6443", Prod: true},
		{Name: "acme/stg-portal", Server: "https://api.ocp-c01.acme:6443/"},
		{Name: "acme/hmg-portal", Server: "https://api.ocp-hmg.acme:6443"},
	}
	markSharedProd(ctxs)
	if !ctxs[1].Prod || !ctxs[1].SharedProd {
		t.Fatalf("stg on the prod server must be production: %+v", ctxs[1])
	}
	if ctxs[2].Prod || ctxs[0].SharedProd {
		t.Fatalf("other server untouched, real prod not marked shared: %+v %+v", ctxs[2], ctxs[0])
	}
}

func TestContextIDStable(t *testing.T) {
	if ContextID("a/b:6443/c") != ContextID("a/b:6443/c") || len(ContextID("x")) != 12 {
		t.Fatal("unstable id")
	}
}
