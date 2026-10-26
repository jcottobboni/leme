package security

import (
	"fmt"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/jcottobboni/leme/engine/internal/rbac"
)

// RBACRisk is a dangerous grant to a subject.
type RBACRisk struct {
	Severity         string `json:"severity"`
	Title            string `json:"title"`
	Detail           string `json:"detail"`
	SubjectKind      string `json:"subjectKind"`
	SubjectName      string `json:"subjectName"`
	SubjectNamespace string `json:"subjectNamespace,omitempty"`
	BindingKind      string `json:"bindingKind"`
	BindingName      string `json:"bindingName"`
	BindingNamespace string `json:"bindingNamespace,omitempty"`
	RoleKind         string `json:"roleKind"`
	RoleName         string `json:"roleName"`
	System           bool   `json:"system"` // built-in Kubernetes/OpenShift subject or binding
}

type riskRule struct {
	sev, title, detail string
	req                rbac.Request
	clusterOnly        bool // only meaningful through a ClusterRoleBinding
}

var riskRules = []riskRule{
	{Critical, "Acesso total (verbos e recursos *)", "Pode fazer qualquer coisa nos recursos alcançados pelo binding.", rbac.Request{Verb: "*", Group: "*", Resource: "*"}, false},
	{High, "Lê Secrets do cluster inteiro", "Ler todos os Secrets dá acesso a tokens de ServiceAccount e credenciais de todos os namespaces.", rbac.Request{Verb: "list", Resource: "secrets"}, true},
	{High, "Executa comandos em pods (pods/exec)", "Permite abrir shell em containers e ler seus segredos e dados.", rbac.Request{Verb: "create", Resource: "pods", Subresource: "exec"}, false},
	{High, "Pode criar ou alterar bindings (bind)", "Com bind é possível conceder a si mesmo papéis que ainda não tem.", rbac.Request{Verb: "bind", Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, false},
	{High, "Pode escalar papéis (escalate)", "Permite editar roles para incluir permissões que o próprio subject não tem.", rbac.Request{Verb: "escalate", Group: "rbac.authorization.k8s.io", Resource: "clusterroles"}, false},
	{High, "Pode se passar por outros usuários (impersonate)", "Impersonate de users ou groups contorna qualquer restrição de RBAC.", rbac.Request{Verb: "impersonate", Resource: "users"}, false},
	{Medium, "Pode criar pods", "Quem cria pods pode montar Secrets e ServiceAccounts do namespace e rodar código com eles.", rbac.Request{Verb: "create", Resource: "pods"}, false},
}

func isSystem(name string) bool {
	return strings.HasPrefix(name, "system:") || strings.HasPrefix(name, "kubeadm:")
}

// broadGroups contain every (authenticated) user or service account.
var broadGroups = map[string]bool{"system:authenticated": true, "system:unauthenticated": true, "system:anonymous": true, "system:serviceaccounts": true}

// onlyNonResource reports rules that only grant URLs like /healthz or /version (public by design).
func onlyNonResource(rules []rbacv1.PolicyRule) bool {
	for _, r := range rules {
		if len(r.Resources) > 0 {
			return false
		}
	}
	return true
}

// allows checks a request, treating "*" in the request as "the rule grants the wildcard itself".
func allows(rules []rbacv1.PolicyRule, r rbac.Request) bool {
	if r.Verb != "*" {
		return rbac.AllowsAny(rules, r)
	}
	for _, rule := range rules {
		if contains(rule.Verbs, "*") && contains(rule.Resources, "*") && contains(rule.APIGroups, "*") {
			return true
		}
	}
	return false
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// RBACRisks scans every binding of the snapshot. Only the most severe risk per binding/subject is kept.
func RBACRisks(snap *rbac.Snapshot) []RBACRisk {
	var out []RBACRisk
	check := func(sub rbacv1.Subject, bindingKind, bindingName, bindingNS string, ref rbacv1.RoleRef, rules []rbacv1.PolicyRule) {
		// "System" hides built-in grants by default. A custom binding cannot hide itself by its name alone:
		// the subject must be a control-plane identity too, and grants to everyone are never system.
		systemSubject := (isSystem(sub.Name) && !broadGroups[sub.Name]) || (sub.Kind == "ServiceAccount" && (sub.Namespace == "kube-system" || strings.HasPrefix(sub.Namespace, "openshift")))
		// system:masters is the built-in superuser group (client certificates only); its binding is expected.
		system := systemSubject && (isSystem(bindingName) || isSystem(ref.Name) || sub.Kind == "ServiceAccount" || sub.Name == "system:masters")
		base := RBACRisk{SubjectKind: sub.Kind, SubjectName: sub.Name, SubjectNamespace: sub.Namespace, BindingKind: bindingKind, BindingName: bindingName, BindingNamespace: bindingNS, RoleKind: ref.Kind, RoleName: ref.Name, System: system}
		if sub.Kind == "ServiceAccount" && base.SubjectNamespace == "" {
			base.SubjectNamespace = bindingNS
		}
		if sub.Name == "system:anonymous" || sub.Name == "system:unauthenticated" {
			if onlyNonResource(rules) {
				return
			}
			r := base
			r.Severity, r.Title = Critical, "Permissão para quem não está autenticado"
			r.System = isSystem(bindingName)
			r.Detail = fmt.Sprintf("O binding concede %s/%s a %s: qualquer pessoa que alcance o API server.", ref.Kind, ref.Name, sub.Name)
			out = append(out, r)
			return
		}
		for _, rr := range riskRules {
			if rr.clusterOnly && bindingKind != "ClusterRoleBinding" {
				continue
			}
			if allows(rules, rr.req) {
				r := base
				r.Severity, r.Title, r.Detail = rr.sev, rr.title, rr.detail
				if bindingKind == "RoleBinding" && r.Severity == Critical {
					// Full access limited to one namespace is serious but not cluster takeover.
					r.Severity, r.Title = High, rr.title+" no namespace "+bindingNS
				}
				out = append(out, r)
				return
			}
		}
	}
	rulesFor := func(ref rbacv1.RoleRef, ns string) []rbacv1.PolicyRule {
		switch ref.Kind {
		case "ClusterRole":
			for _, cr := range snap.ClusterRoles {
				if cr.Name == ref.Name {
					return cr.Rules
				}
			}
		case "Role":
			for _, r := range snap.Roles {
				if r.Name == ref.Name && r.Namespace == ns {
					return r.Rules
				}
			}
		}
		return nil
	}
	for _, b := range snap.ClusterRoleBindings {
		rules := rulesFor(b.RoleRef, "")
		for _, s := range b.Subjects {
			check(s, "ClusterRoleBinding", b.Name, "", b.RoleRef, rules)
		}
	}
	for _, b := range snap.RoleBindings {
		rules := rulesFor(b.RoleRef, b.Namespace)
		for _, s := range b.Subjects {
			check(s, "RoleBinding", b.Name, b.Namespace, b.RoleRef, rules)
		}
	}
	rank := map[string]int{Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return rank[out[i].Severity] < rank[out[j].Severity]
	})
	return out
}
