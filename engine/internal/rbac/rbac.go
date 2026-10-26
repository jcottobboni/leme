package rbac

import (
	"context"
	"sort"
	"sync"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Verbs shown in the permission matrix.
var Verbs = []string{"get", "list", "watch", "create", "update", "patch", "delete", "deletecollection"}

// Resource is the minimal resource identity the matrix needs.
type Resource struct {
	Group      string `json:"group"`
	Resource   string `json:"resource"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
}

// MatrixRow lists the verbs allowed on one resource.
type MatrixRow struct {
	Resource
	Verbs map[string]bool `json:"verbs"`
}

// MyRules is the answer of SelfSubjectRulesReview plus the derived matrix.
type MyRules struct {
	Namespace       string                   `json:"namespace"`
	Incomplete      bool                     `json:"incomplete"`
	EvaluationError string                   `json:"evaluationError,omitempty"`
	Rules           []authv1.ResourceRule    `json:"rules"`
	NonResource     []authv1.NonResourceRule `json:"nonResource"`
	Matrix          []MatrixRow              `json:"matrix"`
}

// Mine runs a SelfSubjectRulesReview in namespace and evaluates every resource against it.
func Mine(ctx context.Context, c kubernetes.Interface, namespace string, resources []Resource) (*MyRules, error) {
	if namespace == "" {
		namespace = "default"
	}
	rv, err := c.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, &authv1.SelfSubjectRulesReview{Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: namespace}}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	out := &MyRules{Namespace: namespace, Incomplete: rv.Status.Incomplete, EvaluationError: rv.Status.EvaluationError,
		Rules: rv.Status.ResourceRules, NonResource: rv.Status.NonResourceRules}
	rules := make([]rbacv1.PolicyRule, 0, len(rv.Status.ResourceRules))
	for _, r := range rv.Status.ResourceRules {
		rules = append(rules, rbacv1.PolicyRule{Verbs: r.Verbs, APIGroups: r.APIGroups, Resources: r.Resources, ResourceNames: r.ResourceNames})
	}
	for _, res := range resources {
		row := MatrixRow{Resource: res, Verbs: map[string]bool{}}
		for _, v := range Verbs {
			row.Verbs[v] = AllowsAny(rules, Request{Verb: v, Group: res.Group, Resource: res.Resource})
		}
		out.Matrix = append(out.Matrix, row)
	}
	return out, nil
}

// CanResult is the SelfSubjectAccessReview answer.
type CanResult struct {
	Allowed bool   `json:"allowed"`
	Denied  bool   `json:"denied"`
	Reason  string `json:"reason,omitempty"`
}

// Can asks the API server whether the current user may perform r.
func Can(ctx context.Context, c kubernetes.Interface, r Request) (*CanResult, error) {
	rv, err := c.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{
		ResourceAttributes: &authv1.ResourceAttributes{Namespace: r.Namespace, Verb: r.Verb, Group: r.Group, Resource: r.Resource, Subresource: r.Subresource, Name: r.Name},
	}}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return &CanResult{Allowed: rv.Status.Allowed, Denied: rv.Status.Denied, Reason: rv.Status.Reason}, nil
}

// Snapshot is the set of RBAC objects visible to the user.
type Snapshot struct {
	ClusterRoles        []rbacv1.ClusterRole
	Roles               []rbacv1.Role
	ClusterRoleBindings []rbacv1.ClusterRoleBinding
	RoleBindings        []rbacv1.RoleBinding
	// Errors holds partial-list failures (e.g. no permission to list cluster-wide bindings).
	Errors map[string]string
}

// Load lists RBAC objects. namespace "" lists namespaced objects in every namespace.
func Load(ctx context.Context, c kubernetes.Interface, namespace string) *Snapshot {
	s := &Snapshot{Errors: map[string]string{}}
	r := c.RbacV1()
	if l, err := r.ClusterRoles().List(ctx, metav1.ListOptions{}); err == nil {
		s.ClusterRoles = l.Items
	} else {
		s.Errors["clusterroles"] = err.Error()
	}
	if l, err := r.ClusterRoleBindings().List(ctx, metav1.ListOptions{}); err == nil {
		s.ClusterRoleBindings = l.Items
	} else {
		s.Errors["clusterrolebindings"] = err.Error()
	}
	if l, err := r.Roles(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		s.Roles = l.Items
	} else {
		s.Errors["roles"] = err.Error()
	}
	if l, err := r.RoleBindings(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		s.RoleBindings = l.Items
	} else {
		s.Errors["rolebindings"] = err.Error()
	}
	return s
}

func (s *Snapshot) rulesFor(ref rbacv1.RoleRef, namespace string) ([]rbacv1.PolicyRule, bool) {
	switch ref.Kind {
	case "ClusterRole":
		for _, cr := range s.ClusterRoles {
			if cr.Name == ref.Name {
				return cr.Rules, true
			}
		}
	case "Role":
		for _, r := range s.Roles {
			if r.Name == ref.Name && r.Namespace == namespace {
				return r.Rules, true
			}
		}
	}
	return nil, false
}

// Grant ties a subject to one binding and the rules it receives.
type Grant struct {
	BindingKind      string              `json:"bindingKind"`
	BindingName      string              `json:"bindingName"`
	BindingNamespace string              `json:"bindingNamespace,omitempty"`
	RoleKind         string              `json:"roleKind"`
	RoleName         string              `json:"roleName"`
	RoleFound        bool                `json:"roleFound"`
	Rules            []rbacv1.PolicyRule `json:"rules,omitempty"`
}

// Subject is a user, group or service account and everything bound to it.
type Subject struct {
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Namespace string  `json:"namespace,omitempty"`
	Grants    []Grant `json:"grants"`
}

func subjectKey(sub rbacv1.Subject, bindingNS string) (string, rbacv1.Subject) {
	if sub.Kind == rbacv1.ServiceAccountKind && sub.Namespace == "" {
		sub.Namespace = bindingNS
	}
	if sub.Kind != rbacv1.ServiceAccountKind {
		sub.Namespace = ""
	}
	return sub.Kind + "|" + sub.Namespace + "|" + sub.Name, sub
}

// Subjects groups every binding by subject.
func (s *Snapshot) Subjects() []Subject {
	by := map[string]*Subject{}
	add := func(sub rbacv1.Subject, bindingNS string, g Grant) {
		k, sub := subjectKey(sub, bindingNS)
		e, ok := by[k]
		if !ok {
			e = &Subject{Kind: sub.Kind, Name: sub.Name, Namespace: sub.Namespace}
			by[k] = e
		}
		e.Grants = append(e.Grants, g)
	}
	for _, b := range s.ClusterRoleBindings {
		rules, ok := s.rulesFor(b.RoleRef, "")
		g := Grant{BindingKind: "ClusterRoleBinding", BindingName: b.Name, RoleKind: b.RoleRef.Kind, RoleName: b.RoleRef.Name, RoleFound: ok, Rules: rules}
		for _, sub := range b.Subjects {
			add(sub, "", g)
		}
	}
	for _, b := range s.RoleBindings {
		rules, ok := s.rulesFor(b.RoleRef, b.Namespace)
		g := Grant{BindingKind: "RoleBinding", BindingName: b.Name, BindingNamespace: b.Namespace, RoleKind: b.RoleRef.Kind, RoleName: b.RoleRef.Name, RoleFound: ok, Rules: rules}
		for _, sub := range b.Subjects {
			add(sub, b.Namespace, g)
		}
	}
	out := make([]Subject, 0, len(by))
	for _, e := range by {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// WhoCanEntry is a subject allowed to perform the request and the binding that grants it.
type WhoCanEntry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Namespace   string `json:"namespace,omitempty"`
	BindingKind string `json:"bindingKind"`
	BindingName string `json:"bindingName"`
	BindingNS   string `json:"bindingNamespace,omitempty"`
	RoleKind    string `json:"roleKind"`
	RoleName    string `json:"roleName"`
}

// WhoCan lists subjects allowed to perform r. Cluster role bindings always apply; role bindings
// only in r.Namespace (a request without namespace is cluster-scoped and only matches cluster bindings).
func (s *Snapshot) WhoCan(r Request) []WhoCanEntry {
	var out []WhoCanEntry
	seen := map[string]bool{}
	emit := func(sub rbacv1.Subject, bindingNS string, e WhoCanEntry) {
		k, sub := subjectKey(sub, bindingNS)
		k += "|" + e.BindingKind + "|" + e.BindingNS + "|" + e.BindingName
		if seen[k] {
			return
		}
		seen[k] = true
		e.Kind, e.Name, e.Namespace = sub.Kind, sub.Name, sub.Namespace
		out = append(out, e)
	}
	for _, b := range s.ClusterRoleBindings {
		if rules, ok := s.rulesFor(b.RoleRef, ""); ok && AllowsAny(rules, r) {
			for _, sub := range b.Subjects {
				emit(sub, "", WhoCanEntry{BindingKind: "ClusterRoleBinding", BindingName: b.Name, RoleKind: b.RoleRef.Kind, RoleName: b.RoleRef.Name})
			}
		}
	}
	if r.Namespace != "" {
		for _, b := range s.RoleBindings {
			if b.Namespace != r.Namespace {
				continue
			}
			if rules, ok := s.rulesFor(b.RoleRef, b.Namespace); ok && AllowsAny(rules, r) {
				for _, sub := range b.Subjects {
					emit(sub, b.Namespace, WhoCanEntry{BindingKind: "RoleBinding", BindingName: b.Name, BindingNS: b.Namespace, RoleKind: b.RoleRef.Kind, RoleName: b.RoleRef.Name})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Namespace+"/"+out[i].Name < out[j].Namespace+"/"+out[j].Name
	})
	return out
}

// Cache keeps one snapshot per (client, namespace) for a short time; RBAC lists can be large.
type Cache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[cacheKey]cached
}

type cacheKey struct {
	c  kubernetes.Interface
	ns string
}

type cached struct {
	at   time.Time
	snap *Snapshot
}

// NewCache creates a cache.
func NewCache(ttl time.Duration) *Cache { return &Cache{ttl: ttl, m: map[cacheKey]cached{}} }

// Get returns a fresh or cached snapshot.
func (c *Cache) Get(ctx context.Context, client kubernetes.Interface, namespace string, refresh bool) *Snapshot {
	k := cacheKey{client, namespace}
	c.mu.Lock()
	e, ok := c.m[k]
	c.mu.Unlock()
	if ok && !refresh && time.Since(e.at) < c.ttl {
		return e.snap
	}
	snap := Load(ctx, client, namespace)
	c.mu.Lock()
	for key, v := range c.m {
		if time.Since(v.at) > 10*c.ttl {
			delete(c.m, key)
		}
	}
	c.m[k] = cached{at: time.Now(), snap: snap}
	c.mu.Unlock()
	return snap
}
