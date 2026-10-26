package rbac

import (
	"context"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAllows(t *testing.T) {
	pods := rbacv1.PolicyRule{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}
	logs := rbacv1.PolicyRule{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods/log"}}
	anySub := rbacv1.PolicyRule{Verbs: []string{"get"}, APIGroups: []string{"*"}, Resources: []string{"*/status"}}
	named := rbacv1.PolicyRule{Verbs: []string{"get", "update"}, APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{"app-config"}}
	all := rbacv1.PolicyRule{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}
	cases := []struct {
		name string
		rule rbacv1.PolicyRule
		req  Request
		want bool
	}{
		{"verb ok", pods, Request{Verb: "list", Resource: "pods"}, true},
		{"verb denied", pods, Request{Verb: "delete", Resource: "pods"}, false},
		{"other group", pods, Request{Verb: "get", Group: "apps", Resource: "pods"}, false},
		{"resource rule does not grant subresource", pods, Request{Verb: "get", Resource: "pods", Subresource: "log"}, false},
		{"subresource rule", logs, Request{Verb: "get", Resource: "pods", Subresource: "log"}, true},
		{"subresource rule does not grant parent", logs, Request{Verb: "get", Resource: "pods"}, false},
		{"*/status", anySub, Request{Verb: "get", Group: "apps", Resource: "deployments", Subresource: "status"}, true},
		{"*/status not other subresource", anySub, Request{Verb: "get", Resource: "pods", Subresource: "log"}, false},
		{"named ok", named, Request{Verb: "get", Resource: "configmaps", Name: "app-config"}, true},
		{"named other", named, Request{Verb: "get", Resource: "configmaps", Name: "other"}, false},
		{"named list needs no name match", named, Request{Verb: "get", Resource: "configmaps"}, false},
		{"wildcard", all, Request{Verb: "deletecollection", Group: "x.io", Resource: "foos"}, true},
	}
	for _, c := range cases {
		if got := Allows(c.rule, c.req); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSubjectsAndWhoCan(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientset(
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "secret-reader"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"secrets"}}}},
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "admin-all"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "pod-viewer", Namespace: "demo"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "admins"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "admin-all"},
			Subjects: []rbacv1.Subject{{Kind: "Group", Name: "ops"}}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "read-secrets", Namespace: "demo"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"},
			Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "ci"}, {Kind: "User", Name: "ana"}}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "view-pods", Namespace: "demo"}, RoleRef: rbacv1.RoleRef{Kind: "Role", Name: "pod-viewer"},
			Subjects: []rbacv1.Subject{{Kind: "User", Name: "ana"}}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "other"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"},
			Subjects: []rbacv1.Subject{{Kind: "User", Name: "bob"}}},
	)
	snap := Load(ctx, c, "")
	if len(snap.Errors) != 0 {
		t.Fatalf("unexpected errors %v", snap.Errors)
	}
	subs := snap.Subjects()
	var ana, ci *Subject
	for i := range subs {
		switch {
		case subs[i].Kind == "User" && subs[i].Name == "ana":
			ana = &subs[i]
		case subs[i].Kind == "ServiceAccount" && subs[i].Name == "ci":
			ci = &subs[i]
		}
	}
	if ana == nil || len(ana.Grants) != 2 {
		t.Fatalf("ana grants: %+v", ana)
	}
	if ci == nil || ci.Namespace != "demo" {
		t.Fatalf("service account must default to the binding namespace: %+v", ci)
	}

	who := snap.WhoCan(Request{Verb: "get", Resource: "secrets", Namespace: "demo"})
	got := map[string]bool{}
	for _, w := range who {
		got[w.Kind+":"+w.Namespace+"/"+w.Name] = true
	}
	for _, want := range []string{"Group:/ops", "ServiceAccount:demo/ci", "User:/ana"} {
		if !got[want] {
			t.Errorf("who-can get secrets in demo is missing %s (got %v)", want, got)
		}
	}
	if got["User:/bob"] {
		t.Error("bob's binding is in another namespace and must not count")
	}
	clusterWide := snap.WhoCan(Request{Verb: "get", Resource: "secrets"})
	if len(clusterWide) != 1 || clusterWide[0].Name != "ops" {
		t.Fatalf("cluster-scoped who-can must only use cluster bindings: %+v", clusterWide)
	}
}
