package change

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

var (
	depRes = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	secRes = cluster.Resource{Group: "", Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
	cmRes  = cluster.Resource{Group: "", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
)

func deployment() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "broken", "namespace": "demo", "uid": "u1", "resourceVersion": "7"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"containers": []any{map[string]any{"name": "nginx", "image": "nginx:does-not-exist"}},
		}}},
	}}
}

func configMap() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "cfg", "namespace": "demo", "resourceVersion": "3"},
		"data":     map[string]any{"LOG_LEVEL": "debug"},
	}}
}

func session(t *testing.T, allowed bool) (*cluster.Session, *dynfake.FakeDynamicClient) {
	t.Helper()
	gvrs := map[schema.GroupVersionResource]string{depRes.GVR(): "DeploymentList", cmRes.GVR(): "ConfigMapList", secRes.GVR(): "SecretList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, deployment(), configMap())
	typed := fake.NewSimpleClientset()
	typed.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: allowed}}, nil
	})
	s := cluster.NewStaticSession(kube.Context{ID: "c1", Name: "kind"}, typed, dyn, []cluster.Resource{depRes, secRes, cmRes}, false)
	return s, dyn
}

var imagePatch = map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
	"containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine"}},
}}}}

func TestProposeAndApply(t *testing.T) {
	s, dyn := session(t, true)
	st := NewStore()
	ref := Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}
	p, err := st.Propose(context.Background(), s, ref, imagePatch, "fix image", "copilot", false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.RBACAllowed || p.DryRun || !strings.Contains(p.Before, "does-not-exist") || !strings.Contains(p.After, "1.27-alpine") {
		t.Fatalf("unexpected proposal: %+v", p)
	}
	// Gate refuses: nothing is written.
	if _, err := st.Apply(context.Background(), s, p.Token, func() error { return errors.New("read-only") }); err == nil {
		t.Fatal("gate must block")
	}
	if _, err := st.Apply(context.Background(), s, p.Token, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := dyn.Resource(depRes.GVR()).Namespace("demo").Get(context.Background(), "broken", metav1.GetOptions{})
	img, _, _ := unstructured.NestedSlice(got.Object, "spec", "template", "spec", "containers")
	if img[0].(map[string]any)["image"] != "nginx:1.27-alpine" {
		t.Fatalf("not applied: %v", img)
	}
	// Single use.
	if _, err := st.Apply(context.Background(), s, p.Token, func() error { return nil }); err == nil {
		t.Fatal("token must be single-use")
	}
}

func TestApplyKeepsStatus(t *testing.T) {
	s, dyn := session(t, true)
	ctx := context.Background()
	rc := dyn.Resource(depRes.GVR()).Namespace("demo")
	cur, _ := rc.Get(ctx, "broken", metav1.GetOptions{})
	cur.Object["status"] = map[string]any{"history": []any{map[string]any{"id": int64(1)}}}
	if _, err := rc.Update(ctx, cur, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	st := NewStore()
	p, err := st.Propose(ctx, s, Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, imagePatch, "fix image", "copilot", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(ctx, s, p.Token, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := rc.Get(ctx, "broken", metav1.GetOptions{})
	if h, _, _ := unstructured.NestedSlice(got.Object, "status", "history"); len(h) != 1 {
		t.Fatalf("status lost on apply: %v", got.Object["status"])
	}
}

func TestRefusals(t *testing.T) {
	s, _ := session(t, true)
	st := NewStore()
	ctx := context.Background()
	cases := map[string]struct {
		ref   Ref
		patch map[string]any
	}{
		"secret":     {Ref{Kind: "Secret", Namespace: "demo", Name: "s"}, map[string]any{"data": map[string]any{"a": "b"}}},
		"rbac":       {Ref{Group: "rbac.authorization.k8s.io", Kind: "ClusterRoleBinding", Name: "x"}, map[string]any{"subjects": []any{}}},
		"rename":     {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{"metadata": map[string]any{"name": "other"}}},
		"owner":      {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{"metadata": map[string]any{"ownerReferences": []any{map[string]any{"name": "x"}}}}},
		"delete":     {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{"metadata": map[string]any{"deletionTimestamp": "2026-01-01T00:00:00Z"}}},
		"kind":       {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{"kind": "StatefulSet"}},
		"noop":       {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{"status": map[string]any{"replicas": 9}}},
		"cm-data":    {Ref{Kind: "ConfigMap", Namespace: "demo", Name: "cfg"}, map[string]any{"data": map[string]any{"LOG_LEVEL": "info"}}},
		"empty":      {Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, map[string]any{}},
		"missing ns": {Ref{Group: "apps", Kind: "Deployment", Name: "broken"}, imagePatch},
	}
	for name, c := range cases {
		if _, err := st.Propose(ctx, s, c.ref, c.patch, "", "copilot", true); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
	}
	// ConfigMap labels are fine.
	if _, err := st.Propose(ctx, s, Ref{Kind: "ConfigMap", Namespace: "demo", Name: "cfg"}, map[string]any{"metadata": map[string]any{"labels": map[string]any{"a": "b"}}}, "", "copilot", false); err != nil {
		t.Errorf("configmap label: %v", err)
	}
}

func TestStaleAndExpired(t *testing.T) {
	s, dyn := session(t, true)
	st := NewStore()
	ctx := context.Background()
	ref := Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}
	p, err := st.Propose(ctx, s, ref, imagePatch, "", "mcp", false)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else changes the object: the proposal is void.
	cur, _ := dyn.Resource(depRes.GVR()).Namespace("demo").Get(ctx, "broken", metav1.GetOptions{})
	cur.SetResourceVersion("8")
	if _, err := dyn.Resource(depRes.GVR()).Namespace("demo").Update(ctx, cur, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Apply(ctx, s, p.Token, func() error { return nil }); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale proposal must fail, got %v", err)
	}
	p2, _ := st.Propose(ctx, s, ref, imagePatch, "", "mcp", false)
	st.m[p2.Token].Expires = time.Now().Add(-time.Second)
	if _, err := st.Apply(ctx, s, p2.Token, func() error { return nil }); err == nil {
		t.Fatal("expired token must fail")
	}
	// Tokens are bound to their cluster.
	p3, _ := st.Propose(ctx, s, ref, imagePatch, "", "mcp", false)
	if _, err := st.Get("other-cluster", p3.Token); err == nil {
		t.Fatal("token must be bound to its cluster")
	}
}

func TestRedactedView(t *testing.T) {
	s, _ := session(t, true)
	st := NewStore()
	patch := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine", "env": []any{map[string]any{"name": "PASS", "value": "hunter2"}}}},
	}}}}
	p, err := st.Propose(context.Background(), s, Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}, patch, "", "mcp", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.After, "hunter2") {
		t.Fatal("the local (UI) view keeps values")
	}
	if strings.Contains(p.View(true).After, "hunter2") {
		t.Fatal("the agent view must mask env values")
	}
	// M7 "Exportar proposta": the patch travels with the proposal, masked the same way for agents.
	if !strings.Contains(p.Patch, "image: nginx:1.27-alpine") || !strings.Contains(p.Patch, "hunter2") {
		t.Fatalf("the UI patch is the merge patch as YAML: %q", p.Patch)
	}
	if v := p.View(true).Patch; strings.Contains(v, "hunter2") || !strings.Contains(v, "nginx:1.27-alpine") {
		t.Fatalf("the agent view must mask env values in the patch: %q", v)
	}
	// The stored copy does not follow later edits of the caller's map.
	patch["spec"] = "changed"
	if strings.Contains(p.View(true).Patch, "changed") {
		t.Fatal("the patch must be copied")
	}
}

func TestMaskedValuesAndRisks(t *testing.T) {
	s, _ := session(t, true)
	st := NewStore()
	ctx := context.Background()
	ref := Ref{Group: "apps", Kind: "Deployment", Namespace: "demo", Name: "broken"}
	masked := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine", "env": []any{map[string]any{"name": "DB_PASS", "value": "<redacted>"}}}},
	}}}}
	if _, err := st.Propose(ctx, s, ref, masked, "", "copilot", true); err == nil || !strings.Contains(err.Error(), "masked") {
		t.Fatalf("a patch carrying the legacy mask must be refused: %v", err)
	}
	// The unified placeholder (also used for Secret values in the editor) is refused too.
	unified := map[string]any{"metadata": map[string]any{"annotations": map[string]any{"db": redact.Mask}}}
	if _, err := st.Propose(ctx, s, ref, unified, "", "mcp", true); err == nil || !strings.Contains(err.Error(), "masked") {
		t.Fatalf("a patch carrying redact.Mask must be refused: %v", err)
	}
	p, err := st.Propose(ctx, s, ref, imagePatch, "", "mcp", true)
	if err != nil || len(p.Risks) != 0 {
		t.Fatalf("an image fix carries no risk: %v %v", err, p.Risks)
	}
	esc := map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"hostNetwork":        true,
		"serviceAccountName": "admin",
		"containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine",
			"securityContext": map[string]any{"privileged": true},
			"envFrom":         []any{map[string]any{"secretRef": map[string]any{"name": "db-creds"}}}}},
	}}}}
	p, err = st.Propose(ctx, s, ref, esc, "", "mcp", true)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(p.Risks, "\n")
	for _, want := range []string{"hostNetwork", "ServiceAccount", "db-creds", "rivileg"} {
		if !strings.Contains(all, want) {
			t.Errorf("risk %q not flagged:\n%s", want, all)
		}
	}
	if _, err := st.Apply(ctx, s, p.Token, func() error { return nil }); err == nil {
		t.Fatal("an agent must not apply an escalation")
	}
}
