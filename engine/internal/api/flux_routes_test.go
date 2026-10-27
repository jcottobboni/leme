package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/flux"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

var (
	fluxKsRes  = cluster.Resource{Group: flux.KustomizeGroup, Version: "v1", Resource: "kustomizations", Kind: "Kustomization", Namespaced: true}
	fluxGitRes = cluster.Resource{Group: flux.SourceGroup, Version: "v1", Resource: "gitrepositories", Kind: "GitRepository", Namespaced: true}
)

func fluxKs(suspend bool) *unstructured.Unstructured {
	spec := map[string]any{"interval": "10m", "path": "./apps", "sourceRef": map[string]any{"kind": "GitRepository", "name": "repo"}}
	if suspend {
		spec["suspend"] = true
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization",
		"metadata": map[string]any{"name": "apps", "namespace": "flux-system", "resourceVersion": "3", "uid": "k1"},
		"spec":     spec,
		"status":   map[string]any{"lastAppliedRevision": "main@sha1:abc", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
}

func fluxFixture(t *testing.T, readOnly, suspended bool) (*Server, *cluster.Session, *dynfake.FakeDynamicClient) {
	t.Helper()
	gvrs := map[k8sschema.GroupVersionResource]string{fluxKsRes.GVR(): "KustomizationList", fluxGitRes.GVR(): "GitRepositoryList", argoDepRes.GVR(): "DeploymentList"}
	dep := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "web", "namespace": "flux-system", "resourceVersion": "1"}}}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, fluxKs(suspended), dep)
	typed := fake.NewSimpleClientset()
	typed.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	sess := cluster.NewStaticSession(kube.Context{ID: "c1", Name: "kind-test"}, typed, dyn, []cluster.Resource{fluxKsRes, fluxGitRes, argoDepRes}, readOnly)
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, sess, dyn
}

var appsKs = map[string]string{"kind": "Kustomization", "ns": "flux-system", "name": "apps"}

func fixClock(t *testing.T) {
	old := fluxNow
	fluxNow = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { fluxNow = old })
}

func TestFluxReconcileProposeAndApply(t *testing.T) {
	fixClock(t)
	s, sess, dyn := fluxFixture(t, false, false)
	w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"reconcile"}`, appsKs)
	if w.Code != 200 {
		t.Fatalf("propose: %d %s", w.Code, w.Body)
	}
	var p fluxProposal
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Source != "flux" || p.Ref.Kind != "Kustomization" || !strings.Contains(p.After, "reconcile.fluxcd.io/requestedAt: \"2026-10-27T12:00:00Z\"") ||
		strings.Contains(p.Before, "reconcile.fluxcd.io") || !p.DryRun || len(p.Notes) < 2 {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	if w := argoCall(s.fluxApply, sess, http.MethodPost, "", map[string]string{"token": p.Token}); w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	got, _ := dyn.Resource(fluxKsRes.GVR()).Namespace("flux-system").Get(context.Background(), "apps", metav1.GetOptions{})
	if got.GetAnnotations()[flux.ReconcileAnnotation] != "2026-10-27T12:00:00Z" {
		t.Fatalf("annotation not written: %v", got.GetAnnotations())
	}
	entries, _ := s.audit.Recent(10)
	var actions []string
	for _, e := range entries {
		if e.Source == "flux" && e.Result == "ok" {
			actions = append(actions, e.Action)
		}
	}
	if strings.Join(actions, ",") != "apply,propose" {
		t.Fatalf("audit: %+v", entries)
	}
	// The token is single-use.
	if w := argoCall(s.fluxApply, sess, http.MethodPost, "", map[string]string{"token": p.Token}); w.Code != http.StatusNotFound {
		t.Fatalf("reused token: %d", w.Code)
	}
}

func TestFluxSuspendResume(t *testing.T) {
	fixClock(t)
	s, sess, dyn := fluxFixture(t, false, false)
	if w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"resume"}`, appsKs); w.Code != http.StatusConflict {
		t.Fatalf("resume of a running object: %d %s", w.Code, w.Body)
	}
	w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"suspend"}`, appsKs)
	var p fluxProposal
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || !strings.Contains(p.After, "suspend: true") {
		t.Fatalf("suspend: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.fluxApply, sess, http.MethodPost, "", map[string]string{"token": p.Token}); w.Code != 200 {
		t.Fatalf("apply suspend: %d %s", w.Code, w.Body)
	}
	got, _ := dyn.Resource(fluxKsRes.GVR()).Namespace("flux-system").Get(context.Background(), "apps", metav1.GetOptions{})
	if v, _, _ := unstructured.NestedBool(got.Object, "spec", "suspend"); !v {
		t.Fatal("not suspended")
	}
	// Suspended: reconcile is refused, resume removes the field and requests a reconcile.
	if w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"reconcile"}`, appsKs); w.Code != http.StatusConflict {
		t.Fatalf("reconcile while suspended: %d", w.Code)
	}
	w = argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"resume"}`, appsKs)
	p = fluxProposal{}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || strings.Contains(p.After, "suspend:") || !strings.Contains(p.After, flux.ReconcileAnnotation) {
		t.Fatalf("resume: %d %s", w.Code, p.After)
	}
	if w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"delete"}`, appsKs); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", w.Code)
	}
}

func TestFluxReadOnly(t *testing.T) {
	s, sess, dyn := fluxFixture(t, true, false)
	w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"reconcile"}`, appsKs)
	var p fluxProposal
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.DryRun {
		t.Fatalf("read-only propose must work without dry-run: %d %s", w.Code, w.Body)
	}
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "update" || a.GetVerb() == "patch" {
			t.Fatalf("read-only session received a write: %v", a)
		}
	}
	if w := argoCall(s.fluxApply, sess, http.MethodPost, "", map[string]string{"token": p.Token}); w.Code != http.StatusForbidden {
		t.Fatalf("apply on read-only must be 403: %d %s", w.Code, w.Body)
	}
	entries, _ := s.audit.Recent(10)
	if len(entries) == 0 || entries[0].Source != "flux" || entries[0].Action != "apply" || entries[0].Result != "denied" {
		t.Fatalf("denied apply must be audited: %+v", entries)
	}
}

func TestFluxRefusesOtherKindsAndProposals(t *testing.T) {
	s, sess, _ := fluxFixture(t, false, false)
	other, err := s.changes.Propose(context.Background(), sess, change.Ref{Group: "apps", Kind: "Deployment", Namespace: "flux-system", Name: "web"},
		map[string]any{"metadata": map[string]any{"labels": map[string]any{"x": "y"}}}, "label", "copilot", true)
	if err != nil {
		t.Fatal(err)
	}
	if w := argoCall(s.fluxApply, sess, http.MethodPost, "", map[string]string{"token": other.Token}); w.Code == 200 {
		t.Fatalf("foreign proposal applied: %s", w.Body)
	}
	if w := argoCall(s.fluxPropose, sess, http.MethodPost, `{"action":"reconcile"}`, map[string]string{"kind": "Deployment", "ns": "flux-system", "name": "web"}); w.Code != http.StatusBadRequest {
		t.Fatalf("non-Flux kind: %d", w.Code)
	}
	if _, err := s.changes.ProposeWith(context.Background(), sess, change.Ref{Group: "apps", Kind: "Deployment", Namespace: "flux-system", Name: "web"},
		map[string]any{"spec": map[string]any{"suspend": true}}, "x", "flux", true, flux.Allow); err == nil {
		t.Fatal("flux policy must refuse Deployments")
	}
	if change.Allowed(flux.KustomizeGroup, "Kustomization") == nil {
		t.Fatal("Flux kinds must stay out of the AI/MCP list")
	}
}

func TestFluxListAndAbsent(t *testing.T) {
	s, sess, _ := fluxFixture(t, false, false)
	w := argoCall(s.fluxList, sess, http.MethodGet, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":true`) || !strings.Contains(w.Body.String(), `"revision":"main@sha1:abc"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	bare := cluster.NewStaticSession(kube.Context{ID: "c2"}, fake.NewSimpleClientset(), sess.Dynamic, []cluster.Resource{argoDepRes}, false)
	w = argoCall(s.fluxList, bare, http.MethodGet, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":false`) || !strings.Contains(w.Body.String(), "kustomize.toolkit.fluxcd.io") {
		t.Fatalf("no CRDs: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.fluxPropose, bare, http.MethodPost, `{"action":"reconcile"}`, appsKs); w.Code != http.StatusNotFound {
		t.Fatalf("propose without CRDs: %d", w.Code)
	}
}
