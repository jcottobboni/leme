// Package rbac answers permission questions: my rules, can-i, subjects and who-can.
package rbac

import (
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
)

// Request is one access question, mirroring authorization.k8s.io ResourceAttributes.
type Request struct {
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Name        string `json:"name,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Allows reports whether a policy rule grants the request, following the RBAC authorizer's
// matching (wildcards, "resource/subresource", "*/subresource" and resourceNames).
func Allows(rule rbacv1.PolicyRule, r Request) bool {
	if !has(rule.Verbs, rbacv1.VerbAll) && !has(rule.Verbs, r.Verb) {
		return false
	}
	if !has(rule.APIGroups, rbacv1.APIGroupAll) && !has(rule.APIGroups, r.Group) {
		return false
	}
	combined := r.Resource
	if r.Subresource != "" {
		combined = r.Resource + "/" + r.Subresource
	}
	matched := false
	for _, res := range rule.Resources {
		if res == rbacv1.ResourceAll || res == combined {
			matched = true
			break
		}
		if r.Subresource != "" && strings.HasPrefix(res, "*/") && res[2:] == r.Subresource {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	if len(rule.ResourceNames) == 0 {
		return true
	}
	return r.Name != "" && has(rule.ResourceNames, r.Name)
}

// AllowsAny reports whether any rule grants the request.
func AllowsAny(rules []rbacv1.PolicyRule, r Request) bool {
	for _, rule := range rules {
		if Allows(rule, r) {
			return true
		}
	}
	return false
}
