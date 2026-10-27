// Package change turns an AI- or agent-proposed modification into something a human can review
// and apply safely: one existing object, an allowed kind, the same identity, a server-side dry-run
// when writes are allowed, an RBAC check, a before/after diff and a short-lived token bound to the
// object's resourceVersion. Delete is not expressible here on purpose.
package change

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

// TTL is how long a proposal can be applied.
const TTL = 10 * time.Minute

// fieldManager identifies changes made through proposals in managedFields.
const fieldManager = "leme"

// Ref identifies an existing object.
type Ref struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (r Ref) String() string { return r.Kind + "/" + r.Namespace + "/" + r.Name }

// allowed lists what proposals may modify (group/kind). Everything else is refused, including
// Secrets, RBAC, ServiceAccounts, Namespaces, CRDs, webhooks and cluster-scoped storage.
var allowed = map[string]bool{
	"apps/Deployment": true, "apps/StatefulSet": true, "apps/DaemonSet": true, "apps/ReplicaSet": true,
	"batch/Job": true, "batch/CronJob": true, "apps.openshift.io/DeploymentConfig": true,
	"/Service": true, "networking.k8s.io/Ingress": true, "route.openshift.io/Route": true,
	"/ConfigMap": true, "autoscaling/HorizontalPodAutoscaler": true, "policy/PodDisruptionBudget": true,
	"networking.k8s.io/NetworkPolicy": true,
}

// Allowed reports whether proposals may target a kind.
func Allowed(group, kind string) error {
	if allowed[group+"/"+kind] {
		return nil
	}
	return fmt.Errorf("changes to %s are not allowed through AI or agents", kind)
}

// Proposal is a validated, not yet applied change.
type Proposal struct {
	Token       string    `json:"token"`
	ClusterID   string    `json:"clusterId"`
	Ref         Ref       `json:"ref"`
	Summary     string    `json:"summary"`
	Source      string    `json:"source"`
	Before      string    `json:"before"` // YAML
	After       string    `json:"after"`  // YAML
	DryRun      bool      `json:"dryRun"` // the API server accepted it in dry-run
	DryRunError string    `json:"dryRunError,omitempty"`
	RBACAllowed bool      `json:"rbacAllowed"`
	Risks       []string  `json:"risks,omitempty"` // what the change adds that widens privileges or exposure
	Expires     time.Time `json:"expires"`

	rv     string
	after  *unstructured.Unstructured
	before *unstructured.Unstructured
}

// View renders the proposal. With redacted, values that must not reach an agent are masked.
func (p *Proposal) View(redacted bool) Proposal {
	v := *p
	if redacted {
		v.Before, v.After = render(redact.Object(p.before)), render(redact.Object(p.after))
	}
	return v
}

// Store holds pending proposals in memory.
type Store struct {
	mu sync.Mutex
	m  map[string]*Proposal
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{m: map[string]*Proposal{}} }

// AllowFunc decides which group/kind a caller may propose changes to.
type AllowFunc func(group, kind string) error

// Propose validates a JSON merge patch against the current object. Server-side dry-run only runs
// when canWrite is true (read-only clusters never receive write requests, not even dry-runs).
// The kinds are limited to Allowed (the AI and agent list).
func (st *Store) Propose(ctx context.Context, s *cluster.Session, ref Ref, patch map[string]any, summary, source string, canWrite bool) (*Proposal, error) {
	return st.ProposeWith(ctx, s, ref, patch, summary, source, canWrite, Allowed)
}

// ProposeWith is Propose with a caller-specific kind policy. It exists for engine features that
// build the patch themselves (M5 argocd: sync/refresh of argoproj.io/Application); the patch never
// comes from the UI, an AI or an agent, and the AI/MCP list is not widened.
func (st *Store) ProposeWith(ctx context.Context, s *cluster.Session, ref Ref, patch map[string]any, summary, source string, canWrite bool, allow AllowFunc) (*Proposal, error) {
	if allow == nil {
		allow = Allowed
	}
	if err := allow(ref.Group, ref.Kind); err != nil {
		return nil, err
	}
	if ref.Name == "" {
		return nil, errors.New("the object name is required")
	}
	res, ok := s.LookupKind(ref.Group, ref.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %s in group %q", ref.Kind, ref.Group)
	}
	if res.Namespaced && ref.Namespace == "" {
		return nil, errors.New("the namespace is required")
	}
	if !res.Namespaced {
		ref.Namespace = ""
	}
	rc := s.Dynamic.Resource(res.GVR()).Namespace(ref.Namespace)
	cur, err := rc.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if containsMask(patch) {
		return nil, errMasked
	}
	next, err := applyMergePatch(cur, patch)
	if err != nil {
		return nil, err
	}
	if err := checkIdentity(cur, next); err != nil {
		return nil, err
	}
	if reflect.DeepEqual(clean(cur).Object, clean(next).Object) {
		return nil, errors.New("the proposed patch does not change the object")
	}
	p := &Proposal{
		Token: token(), ClusterID: s.Context.ID, Ref: ref, Summary: summary, Source: source,
		Expires: time.Now().Add(TTL), rv: cur.GetResourceVersion(), before: clean(cur), after: clean(next),
		Risks: risks(cur, next),
	}
	p.Before, p.After = render(p.before), render(p.after)
	if r, err := s.Typed.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{
		ResourceAttributes: &authv1.ResourceAttributes{Namespace: ref.Namespace, Verb: "update", Group: res.Group, Resource: res.Resource, Name: ref.Name},
	}}, metav1.CreateOptions{}); err == nil {
		p.RBACAllowed = r.Status.Allowed
	}
	if canWrite {
		if _, err := rc.Update(ctx, next, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: fieldManager}); err != nil {
			p.DryRunError = err.Error()
		} else {
			p.DryRun = true
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.gc()
	st.m[p.Token] = p
	return p, nil
}

// Get returns a pending proposal of a cluster.
func (st *Store) Get(clusterID, tok string) (*Proposal, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.gc()
	p, ok := st.m[tok]
	if !ok || p.ClusterID != clusterID {
		return nil, errors.New("proposal not found or expired")
	}
	return p, nil
}

// Apply writes a proposal. gate decides whether this caller may write now (UI: the session's
// read-only guard; MCP: the agent access settings). The object must not have changed since the
// proposal; the token is single-use.
func (st *Store) Apply(ctx context.Context, s *cluster.Session, tok string, gate func() error) (*Proposal, error) {
	p, err := st.Get(s.Context.ID, tok)
	if err != nil {
		return nil, err
	}
	if err := gate(); err != nil {
		return nil, err
	}
	if p.Source == "mcp" && len(p.Risks) > 0 {
		return nil, errors.New("agents cannot apply changes that widen privileges or exposure")
	}
	res, ok := s.LookupKind(p.Ref.Group, p.Ref.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %s", p.Ref.Kind)
	}
	rc := s.Dynamic.Resource(res.GVR()).Namespace(p.Ref.Namespace)
	cur, err := rc.Get(ctx, p.Ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if cur.GetResourceVersion() != p.rv {
		st.drop(tok)
		return nil, errors.New("the object changed after the proposal was made: ask again")
	}
	next := p.after.DeepCopy()
	next.SetResourceVersion(p.rv)
	// p.after has no status (clean drops it from the diff). Kinds without a status subresource
	// (the Argo CD Application CRD) would lose it on Update, so the live status is sent back.
	// With a status subresource the API server ignores it in the body.
	if st, ok := cur.Object["status"]; ok {
		next.Object["status"] = st
	}
	if _, err := rc.Update(ctx, next, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: fieldManager}); err != nil {
		return nil, fmt.Errorf("dry-run failed: %w", err)
	}
	if _, err := rc.Update(ctx, next, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		return nil, err
	}
	st.drop(tok)
	return p, nil
}

func (st *Store) drop(tok string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.m, tok)
}

func (st *Store) gc() {
	now := time.Now()
	for k, p := range st.m {
		if now.After(p.Expires) {
			delete(st.m, k)
		}
	}
}

// applyMergePatch applies an RFC 7386 JSON merge patch. "status" is ignored: it is not writable
// through update and would only make the diff misleading.
func applyMergePatch(cur *unstructured.Unstructured, patch map[string]any) (*unstructured.Unstructured, error) {
	if len(patch) == 0 {
		return nil, errors.New("the patch is empty")
	}
	delete(patch, "status")
	orig, err := json.Marshal(cur.Object)
	if err != nil {
		return nil, err
	}
	pb, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	merged, err := jsonpatch.MergePatch(orig, pb)
	if err != nil {
		return nil, fmt.Errorf("invalid merge patch: %w", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(merged, &out); err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: out}, nil
}

// checkIdentity refuses changes to what the object is, who owns it and data that must not be
// edited through AI (ConfigMap contents can hold credentials the model never saw).
func checkIdentity(cur, next *unstructured.Unstructured) error {
	switch {
	case cur.GetAPIVersion() != next.GetAPIVersion() || cur.GetKind() != next.GetKind():
		return errors.New("the patch must not change apiVersion or kind")
	case cur.GetName() != next.GetName() || cur.GetNamespace() != next.GetNamespace():
		return errors.New("the patch must not rename or move the object")
	case cur.GetUID() != next.GetUID() || cur.GetResourceVersion() != next.GetResourceVersion():
		return errors.New("the patch must not change uid or resourceVersion")
	case !reflect.DeepEqual(cur.GetOwnerReferences(), next.GetOwnerReferences()):
		return errors.New("the patch must not change ownerReferences")
	case !reflect.DeepEqual(cur.GetFinalizers(), next.GetFinalizers()):
		return errors.New("the patch must not change finalizers")
	case next.GetDeletionTimestamp() != nil && cur.GetDeletionTimestamp() == nil:
		return errors.New("the patch must not delete the object")
	}
	if cur.GetKind() == "ConfigMap" {
		for _, f := range []string{"data", "binaryData"} {
			if !reflect.DeepEqual(cur.Object[f], next.Object[f]) {
				return errors.New("ConfigMap contents cannot be changed through AI or agents")
			}
		}
	}
	return nil
}

// clean drops server bookkeeping that would only add noise to diffs.
func clean(u *unstructured.Unstructured) *unstructured.Unstructured {
	c := u.DeepCopy()
	c.SetManagedFields(nil)
	unstructured.RemoveNestedField(c.Object, "status")
	if ann := c.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		if len(ann) == 0 {
			ann = nil
		}
		c.SetAnnotations(ann)
	}
	return c
}

func render(u *unstructured.Unstructured) string {
	b, _ := yaml.Marshal(u.Object)
	return string(b)
}

func token() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
