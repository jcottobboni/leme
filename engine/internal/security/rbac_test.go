package security

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jcottobboni/leme/engine/internal/rbac"
)

func TestRBACRisks(t *testing.T) {
	snap := &rbac.Snapshot{
		ClusterRoles: []rbacv1.ClusterRole{
			{ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "secret-reader"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"secrets"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "system:public-info-viewer"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get"}, NonResourceURLs: []string{"/healthz", "/version"}}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "view"}, Rules: []rbacv1.PolicyRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}}},
		},
		ClusterRoleBindings: []rbacv1.ClusterRoleBinding{
			{ObjectMeta: metav1.ObjectMeta{Name: "ci-admin"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: "ci", Namespace: "demo"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:masters"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "anon"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "view"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "system:anonymous"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "system:public-info-viewer"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "system:public-info-viewer"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:unauthenticated"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "system:sneaky"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "mallory"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "system:everyone-admin"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "system:authenticated"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "readers"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "auditors"}}},
		},
		RoleBindings: []rbacv1.RoleBinding{
			{ObjectMeta: metav1.ObjectMeta{Name: "ns-admin", Namespace: "demo"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "ana"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ns-secrets", Namespace: "demo"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: "secret-reader"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "bob"}}},
		},
	}
	risks := RBACRisks(snap)
	find := func(binding string) *RBACRisk {
		for i := range risks {
			if risks[i].BindingName == binding {
				return &risks[i]
			}
		}
		return nil
	}
	if r := find("ci-admin"); r == nil || r.Severity != Critical || r.System || r.SubjectNamespace != "demo" {
		t.Errorf("ci-admin: %+v", r)
	}
	if r := find("cluster-admin"); r == nil || !r.System {
		t.Errorf("system:masters must be flagged as system: %+v", r)
	}
	if r := find("anon"); r == nil || r.Severity != Critical || r.System {
		t.Errorf("anonymous binding: %+v", r)
	}
	if r := find("system:public-info-viewer"); r != nil {
		t.Errorf("health/version endpoints for anonymous users are public by design: %+v", r)
	}
	if r := find("system:sneaky"); r == nil || r.System {
		t.Errorf("a binding named system:* for a normal user must not be hidden: %+v", r)
	}
	if r := find("system:everyone-admin"); r == nil || r.System || r.Severity != Critical {
		t.Errorf("cluster-admin for all authenticated users is never system: %+v", r)
	}
	if r := find("readers"); r == nil || r.Severity != High {
		t.Errorf("cluster-wide secret reading: %+v", r)
	}
	if r := find("ns-admin"); r == nil || r.Severity != High {
		t.Errorf("namespace admin is high, not critical: %+v", r)
	}
	if r := find("ns-secrets"); r != nil {
		t.Errorf("reading secrets in one namespace is not a cluster-wide risk: %+v", r)
	}
	if risks[len(risks)-1].System != true {
		t.Error("system risks are sorted last")
	}
}
