package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jcottobboni/leme/engine/internal/argocd"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

var (
	argoAppRes  = cluster.Resource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications", Kind: "Application", Namespaced: true}
	argoProjRes = cluster.Resource{Group: "argoproj.io", Version: "v1alpha1", Resource: "appprojects", Kind: "AppProject", Namespaced: true}
	argoDepRes  = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
)

func argoApp(phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": "guestbook", "namespace": "argocd", "resourceVersion": "9", "uid": "u1"},
		"spec": map[string]any{
			"project": "default", "source": map[string]any{"repoURL": "https://example.com/g.git", "path": "guestbook", "targetRevision": "main"},
			"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": "guestbook"},
		},
		"status": map[string]any{
			"sync": map[string]any{"status": "OutOfSync", "revision": "abc123"}, "health": map[string]any{"status": "Healthy"},
			"operationState": map[string]any{"phase": phase},
		},
	}}
}

func argoFixture(t *testing.T, readOnly bool, phase string) (*Server, *cluster.Session, *dynfake.FakeDynamicClient) {
	t.Helper()
	gvrs := map[k8sschema.GroupVersionResource]string{argoAppRes.GVR(): "ApplicationList", argoProjRes.GVR(): "AppProjectList", argoDepRes.GVR(): "DeploymentList"}
	dep := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "web", "namespace": "guestbook", "resourceVersion": "1"}}}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, argoApp(phase), dep)
	typed := fake.NewSimpleClientset()
	typed.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	sess := cluster.NewStaticSession(kube.Context{ID: "c1", Name: "kind-test"}, typed, dyn, []cluster.Resource{argoAppRes, argoProjRes, argoDepRes}, readOnly)
	s := New(cluster.NewManager(kube.NewLoader(nil)), Options{Token: "secret", ConfigDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, sess, dyn
}

func argoCall(h sessionHandler, sess *cluster.Session, method, body string, path map[string]string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, "/x", rd)
	for k, v := range path {
		r.SetPathValue(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r, sess)
	return w
}

var guestbook = map[string]string{"ns": "argocd", "name": "guestbook"}

func TestArgoSyncProposeAndApply(t *testing.T) {
	s, sess, dyn := argoFixture(t, false, "Succeeded")
	w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"sync","prune":true}`, guestbook)
	if w.Code != 200 {
		t.Fatalf("propose: %d %s", w.Code, w.Body)
	}
	var p argoProposal
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Source != "argocd" || p.Ref.Kind != "Application" || !strings.Contains(p.After, "username: leme") || !strings.Contains(p.After, "revision: abc123") ||
		!strings.Contains(p.After, "prune: true") || strings.Contains(p.Before, "operation:") || len(p.Notes) < 2 || !p.DryRun {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	w = argoCall(s.argoApply, sess, http.MethodPost, "", map[string]string{"token": p.Token})
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	got, _ := dyn.Resource(argoAppRes.GVR()).Namespace("argocd").Get(context.Background(), "guestbook", metav1.GetOptions{})
	if u, _, _ := unstructured.NestedString(got.Object, "operation", "initiatedBy", "username"); u != "leme" {
		t.Fatalf("operation not written: %v", got.Object["operation"])
	}
	entries, _ := s.audit.Recent(10)
	var actions []string
	for _, e := range entries {
		if (e.Source == "argocd" || e.Source == "ui") && e.Result == "ok" {
			actions = append(actions, e.Source+":"+e.Action)
		}
	}
	if strings.Join(actions, ",") != "argocd:apply,argocd:propose" {
		t.Fatalf("audit: %+v", entries)
	}
	// A second sync while the requested operation is pending is refused.
	if w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"sync"}`, guestbook); w.Code != http.StatusConflict {
		t.Fatalf("pending operation must be refused: %d %s", w.Code, w.Body)
	}
}

func TestArgoRefreshPropose(t *testing.T) {
	s, sess, _ := argoFixture(t, false, "Succeeded")
	w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"refresh","hard":true}`, guestbook)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `argocd.argoproj.io/refresh: hard`) {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"delete"}`, guestbook); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown action: %d", w.Code)
	}
}

func TestArgoSyncRefusedWhileRunning(t *testing.T) {
	s, sess, _ := argoFixture(t, false, "Running")
	if w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"sync"}`, guestbook); w.Code != http.StatusConflict {
		t.Fatalf("running: %d %s", w.Code, w.Body)
	}
}

func TestArgoReadOnly(t *testing.T) {
	s, sess, dyn := argoFixture(t, true, "Succeeded")
	w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"sync"}`, guestbook)
	if w.Code != 200 {
		t.Fatalf("propose must work read-only (without dry-run): %d %s", w.Code, w.Body)
	}
	var p argoProposal
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p.DryRun {
		t.Fatal("no dry-run on read-only sessions")
	}
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "update" || a.GetVerb() == "patch" {
			t.Fatalf("read-only session received a write: %v", a)
		}
	}
	if w := argoCall(s.argoApply, sess, http.MethodPost, "", map[string]string{"token": p.Token}); w.Code != http.StatusForbidden {
		t.Fatalf("apply on read-only must be 403: %d %s", w.Code, w.Body)
	}
}

func TestArgoApplyRefusesOtherProposals(t *testing.T) {
	s, sess, _ := argoFixture(t, false, "Succeeded")
	// A proposal from another source (e.g. the Copilot) cannot be applied through this route.
	other, err := s.changes.Propose(context.Background(), sess, change.Ref{Group: "apps", Kind: "Deployment", Namespace: "guestbook", Name: "web"},
		map[string]any{"metadata": map[string]any{"labels": map[string]any{"x": "y"}}}, "label", "copilot", true)
	if err != nil {
		t.Fatal(err)
	}
	if w := argoCall(s.argoApply, sess, http.MethodPost, "", map[string]string{"token": other.Token}); w.Code == 200 {
		t.Fatalf("foreign proposal applied: %s", w.Body)
	}
	// The Argo CD policy refuses any other kind, even through ProposeWith.
	if _, err := s.changes.ProposeWith(context.Background(), sess, change.Ref{Group: "apps", Kind: "Deployment", Namespace: "guestbook", Name: "web"},
		map[string]any{"metadata": map[string]any{"labels": map[string]any{"x": "y"}}}, "x", "argocd", true, argocd.Allow); err == nil {
		t.Fatal("argocd policy must refuse Deployments")
	}
	// The AI/MCP list is not widened by this feature.
	if change.Allowed("argoproj.io", "Application") == nil {
		t.Fatal("Applications must stay out of the AI/MCP list")
	}
}

func TestArgoListAndDetail(t *testing.T) {
	s, sess, _ := argoFixture(t, false, "Succeeded")
	w := argoCall(s.argoList, sess, http.MethodGet, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":true`) || !strings.Contains(w.Body.String(), `"sync":"OutOfSync"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.argoDetail, sess, http.MethodGet, "", guestbook); w.Code != 200 || !strings.Contains(w.Body.String(), `"resources":[]`) {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.argoDetail, sess, http.MethodGet, "", map[string]string{"ns": "argocd", "name": "missing"}); w.Code != http.StatusNotFound {
		t.Fatalf("missing app: %d", w.Code)
	}
	// Without the CRDs the list explains and the other routes refuse.
	bare := cluster.NewStaticSession(kube.Context{ID: "c2"}, fake.NewSimpleClientset(), sess.Dynamic, []cluster.Resource{argoDepRes}, false)
	w = argoCall(s.argoList, bare, http.MethodGet, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"installed":false`) || !strings.Contains(w.Body.String(), `"hint"`) {
		t.Fatalf("no CRDs: %d %s", w.Code, w.Body)
	}
	if w := argoCall(s.argoPropose, bare, http.MethodPost, `{"action":"sync"}`, guestbook); w.Code != http.StatusNotFound {
		t.Fatalf("propose without CRDs: %d", w.Code)
	}
}

func TestArgoSyncRemoteDestinationNote(t *testing.T) {
	s, sess, dyn := argoFixture(t, false, "Succeeded")
	app, _ := dyn.Resource(argoAppRes.GVR()).Namespace("argocd").Get(context.Background(), "guestbook", metav1.GetOptions{})
	_ = unstructured.SetNestedField(app.Object, "https://prod.example.com:6443", "spec", "destination", "server")
	if _, err := dyn.Resource(argoAppRes.GVR()).Namespace("argocd").Update(context.Background(), app, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	w := argoCall(s.argoPropose, sess, http.MethodPost, `{"action":"sync"}`, guestbook)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Destino remoto (https://prod.example.com:6443)") {
		t.Fatalf("remote note: %d %s", w.Code, w.Body)
	}
}
