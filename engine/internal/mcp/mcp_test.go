package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/jcottobboni/leme/engine/internal/audit"
	"github.com/jcottobboni/leme/engine/internal/change"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

var (
	depRes = cluster.Resource{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true, ShortNames: []string{"deploy"}}
	secRes = cluster.Resource{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
	cmRes  = cluster.Resource{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	crRes  = cluster.Resource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles", Kind: "ClusterRole"}
)

func obj(m map[string]any) *unstructured.Unstructured { return &unstructured.Unstructured{Object: m} }

func fixtures() []runtime.Object {
	return []runtime.Object{
		obj(map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "web", "namespace": "demo", "uid": "u1", "resourceVersion": "7"},
			"spec": map[string]any{"replicas": int64(2), "template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "nginx", "image": "nginx", "env": []any{map[string]any{"name": "DB_PASSWORD", "value": "hunter2-super-secret"}}}},
			}}}}),
		obj(map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "creds", "namespace": "demo", "resourceVersion": "1"},
			"data":     map[string]any{"password": "aHVudGVyMg=="}}),
		obj(map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cfg", "namespace": "demo", "resourceVersion": "1"},
			"data":     map[string]any{"token": "cfg-value-xyz"}}),
		obj(map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole",
			"metadata": map[string]any{"name": "admin-ish", "resourceVersion": "1"}}),
	}
}

type harness struct {
	env      *Env
	settings *settings.Store
	dir      string
	in       io.WriteCloser
	out      *bufio.Reader
	next     int
}

// newHarness starts a server over pipes with a fake cluster. prod marks the only context as production.
func newHarness(t *testing.T, prod bool) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := settings.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gvrs := map[schema.GroupVersionResource]string{depRes.GVR(): "DeploymentList", secRes.GVR(): "SecretList", cmRes.GVR(): "ConfigMapList", crRes.GVR(): "ClusterRoleList"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvrs, fixtures()...)
	typed := fake.NewSimpleClientset(&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "demo"}, Reason: "BackOff",
		Message: "failed with password=hunter2-super-secret", InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-1"}})
	typed.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		return true, &authv1.SelfSubjectAccessReview{Status: authv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	kc := kube.Context{ID: "c1", Name: "kind-test", Server: "https://10.0.0.1:6443", Current: true, Prod: prod}
	sess := cluster.NewStaticSession(kc, typed, dyn, []cluster.Resource{depRes, secRes, cmRes, crRes}, false)
	env := &Env{
		Contexts: func() ([]kube.Context, error) { return []kube.Context{kc}, nil },
		Connect:  func(context.Context, kube.Context) (*cluster.Session, error) { return sess, nil },
		Settings: st, Audit: audit.Open(dir), Changes: change.NewStore(), Version: "test",
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = NewServer(env).Serve(ctx, inR, outW)
		outW.Close()
		close(done)
	}()
	t.Cleanup(func() { inW.Close(); cancel(); <-done })
	return &harness{env: env, settings: st, dir: dir, in: inW, out: bufio.NewReader(outR)}
}

func (h *harness) rpc(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	h.next++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": h.next, "method": method, "params": params})
	if _, err := h.in.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := h.out.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("stdout must carry only JSON-RPC: %q", line)
	}
	if int(resp["id"].(float64)) != h.next {
		t.Fatalf("id mismatch: %v", resp)
	}
	return resp
}

// tool calls a tool and returns its text and isError flag.
func (h *harness) tool(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()
	resp := h.rpc(t, "tools/call", map[string]any{"name": name, "arguments": args})
	if resp["error"] != nil {
		t.Fatalf("protocol error: %v", resp["error"])
	}
	res := resp["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func (h *harness) enable(t *testing.T, a settings.AgentAccess) {
	t.Helper()
	if a.Write { // granted from the app, which records what the context points to
		list, _ := h.env.Contexts()
		a.Fingerprint = list[0].Fingerprint()
	}
	if err := h.settings.SetAgentAccess("kind-test", a); err != nil {
		t.Fatal(err)
	}
}

var labelPatch = map[string]any{"metadata": map[string]any{"labels": map[string]any{"reviewed": "yes"}}}

func propose(t *testing.T, h *harness, kind string) (map[string]any, bool) {
	t.Helper()
	text, isErr := h.tool(t, "propose_change", map[string]any{"kind": kind, "namespace": "demo", "name": "web", "merge_patch": labelPatch, "summary": "label it"})
	if isErr {
		return map[string]any{"error": text}, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("bad propose output: %s", text)
	}
	return out, false
}

func TestInitializeAndToolsList(t *testing.T) {
	h := newHarness(t, false)
	r := h.rpc(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t"}})
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" || res["serverInfo"].(map[string]any)["name"] != "leme" {
		t.Fatalf("bad initialize: %v", res)
	}
	r = h.rpc(t, "initialize", map[string]any{"protocolVersion": "1999-01-01"})
	if r["result"].(map[string]any)["protocolVersion"] != protocolVersions[0] {
		t.Fatal("unknown version must fall back to the newest supported")
	}
	if _, ok := h.rpc(t, "ping", nil)["result"]; !ok {
		t.Fatal("ping")
	}
	names := map[string]bool{}
	for _, x := range h.rpc(t, "tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
		names[x.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"list_contexts", "list_resources", "get_resource", "get_events", "get_logs", "diagnose", "can_i", "propose_change", "apply_change"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	for n := range names {
		if strings.Contains(n, "delete") || strings.Contains(n, "exec") || strings.Contains(n, "secret") {
			t.Errorf("forbidden tool %s", n)
		}
	}
	if r := h.rpc(t, "nope/nope", nil); r["error"].(map[string]any)["code"].(float64) != codeNoMethod {
		t.Fatalf("unknown method: %v", r)
	}
	if r := h.rpc(t, "tools/call", map[string]any{"name": "nope"}); r["error"] == nil {
		t.Fatal("unknown tool must be a protocol error")
	}
}

func TestReadToolsAreSanitized(t *testing.T) {
	h := newHarness(t, false)
	text, isErr := h.tool(t, "get_resource", map[string]any{"kind": "deploy", "namespace": "demo", "name": "web"})
	if isErr || !strings.Contains(text, "DB_PASSWORD") || strings.Contains(text, "hunter2") {
		t.Fatalf("env value leaked or missing name: %s", text)
	}
	text, _ = h.tool(t, "get_resource", map[string]any{"kind": "secrets", "namespace": "demo", "name": "creds"})
	if strings.Contains(text, "aHVudGVyMg") || !strings.Contains(text, "password") {
		t.Fatalf("secret value leaked: %s", text)
	}
	text, _ = h.tool(t, "get_resource", map[string]any{"kind": "ConfigMap", "namespace": "demo", "name": "cfg"})
	if strings.Contains(text, "cfg-value-xyz") {
		t.Fatalf("configmap value leaked: %s", text)
	}
	text, _ = h.tool(t, "get_events", map[string]any{"namespace": "demo"})
	if strings.Contains(text, "hunter2") || !strings.Contains(text, "BackOff") {
		t.Fatalf("event not masked: %s", text)
	}
	text, _ = h.tool(t, "list_resources", map[string]any{"kind": "Deployment", "namespace": "demo"})
	if !strings.Contains(text, `"web"`) || !strings.Contains(text, "0/2 ready") {
		t.Fatalf("list: %s", text)
	}
	if text, _ = h.tool(t, "can_i", map[string]any{"verb": "get", "resource": "pods"}); !strings.Contains(text, `"allowed": true`) {
		t.Fatalf("can_i: %s", text)
	}
	if text, _ = h.tool(t, "diagnose", map[string]any{"namespace": "demo"}); !strings.Contains(text, "findings") {
		t.Fatalf("diagnose: %s", text)
	}
	if text, isErr = h.tool(t, "get_resource", map[string]any{"kind": "Nope", "name": "x"}); !isErr {
		t.Fatalf("unknown kind must be an error result: %s", text)
	}
	if text, isErr = h.tool(t, "list_resources", map[string]any{"kind": "Deployment", "context": "other"}); !isErr {
		t.Fatalf("unknown context must be an error result: %s", text)
	}
}

func TestListContextsShowsWriteFlag(t *testing.T) {
	h := newHarness(t, false)
	text, _ := h.tool(t, "list_contexts", nil)
	if !strings.Contains(text, `"agentWritesEnabled": false`) || !strings.Contains(text, "10.0.0.1:6443") || strings.Contains(text, "https://") {
		t.Fatalf("list_contexts: %s", text)
	}
	h.enable(t, settings.AgentAccess{Write: true})
	if text, _ = h.tool(t, "list_contexts", nil); !strings.Contains(text, `"agentWritesEnabled": true`) {
		t.Fatalf("settings change must be seen at once: %s", text)
	}
}

func TestProposeRefusedForSecretsAndRBAC(t *testing.T) {
	h := newHarness(t, false)
	h.enable(t, settings.AgentAccess{Write: true})
	for _, c := range []struct{ kind, name string }{{"Secret", "creds"}, {"ClusterRole", "admin-ish"}} {
		_, isErr := h.tool(t, "propose_change", map[string]any{"kind": c.kind, "namespace": "demo", "name": c.name, "merge_patch": labelPatch, "summary": "x"})
		if !isErr {
			t.Errorf("%s must be refused", c.kind)
		}
	}
	// ConfigMap contents are protected as well.
	_, isErr := h.tool(t, "propose_change", map[string]any{"kind": "ConfigMap", "namespace": "demo", "name": "cfg", "merge_patch": map[string]any{"data": map[string]any{"token": "x"}}, "summary": "x"})
	if !isErr {
		t.Error("configmap data change must be refused")
	}
}

func TestWritesDisabledGivesDiffOnly(t *testing.T) {
	h := newHarness(t, false)
	out, isErr := propose(t, h, "Deployment")
	if isErr {
		t.Fatal(out)
	}
	if out["writesEnabled"] != false || out["dryRun"] != false || out["token"] != nil {
		t.Fatalf("no token or dry-run when disabled: %v", out)
	}
	if !strings.Contains(out["note"].(string), "Acesso de agentes") || !strings.Contains(out["diff"].(string), "+") || strings.Contains(out["diff"].(string), "hunter2") {
		t.Fatalf("note/diff: %v", out)
	}
	if text, isErr := h.tool(t, "apply_change", map[string]any{"token": "deadbeef"}); !isErr {
		t.Fatalf("apply must fail: %s", text)
	}
}

func TestApplyFlowAndTokenReuse(t *testing.T) {
	h := newHarness(t, false)
	h.enable(t, settings.AgentAccess{Write: true})
	out, isErr := propose(t, h, "Deployment")
	if isErr || out["dryRun"] != true || out["token"] == nil {
		t.Fatalf("propose: %v", out)
	}
	tok := out["token"].(string)
	if text, isErr := h.tool(t, "apply_change", map[string]any{"token": tok}); isErr || !strings.Contains(text, `"applied": true`) {
		t.Fatalf("apply: %s", text)
	}
	got, _ := h.env.sessions["c1"].Dynamic.Resource(depRes.GVR()).Namespace("demo").Get(context.Background(), "web", metav1.GetOptions{})
	if got.GetLabels()["reviewed"] != "yes" {
		t.Fatalf("label not applied: %v", got.GetLabels())
	}
	if _, isErr := h.tool(t, "apply_change", map[string]any{"token": tok}); !isErr {
		t.Fatal("token reuse must be refused")
	}
	entries, _ := h.env.Audit.Recent(10)
	var applyOK, proposeOK bool
	for _, e := range entries {
		if e.Source != "mcp" {
			t.Errorf("audit source: %+v", e)
		}
		applyOK = applyOK || e.Action == "apply" && e.Result == "ok"
		proposeOK = proposeOK || e.Action == "propose" && e.Result == "ok"
	}
	if !applyOK || !proposeOK {
		t.Fatalf("audit incomplete: %+v", entries)
	}
}

func TestApplyRefusedWhenAccessRevoked(t *testing.T) {
	h := newHarness(t, false)
	h.enable(t, settings.AgentAccess{Write: true})
	out, _ := propose(t, h, "Deployment")
	h.enable(t, settings.AgentAccess{}) // the user turns it off in the app
	text, isErr := h.tool(t, "apply_change", map[string]any{"token": out["token"]})
	if !isErr || !strings.Contains(text, "disabled") {
		t.Fatalf("apply must be refused: %s", text)
	}
	// (The fake dynamic client persists dry-runs, so the denial is asserted through the gate and audit.)
	entries, _ := h.env.Audit.Recent(10)
	if entries[0].Action != "apply" || entries[0].Result != "denied" {
		t.Fatalf("denied apply must be audited: %+v", entries[0])
	}
}

func TestProductionNeedsConfirmation(t *testing.T) {
	h := newHarness(t, true)
	h.enable(t, settings.AgentAccess{Write: true}) // not confirmed
	out, isErr := propose(t, h, "Deployment")
	if isErr || out["writesEnabled"] != false || out["token"] != nil || !strings.Contains(out["note"].(string), "production") {
		t.Fatalf("prod without confirmation must be diff only: %v", out)
	}
	h.enable(t, settings.AgentAccess{Write: true, ProdConfirmed: true})
	out, _ = propose(t, h, "Deployment")
	if out["token"] == nil || out["dryRun"] != true {
		t.Fatalf("prod confirmed: %v", out)
	}
	if text, isErr := h.tool(t, "apply_change", map[string]any{"token": out["token"]}); isErr {
		t.Fatalf("apply: %s", text)
	}
}

func TestMalformedInputDoesNotCrash(t *testing.T) {
	h := newHarness(t, false)
	if _, isErr := h.tool(t, "get_logs", map[string]any{}); !isErr {
		t.Fatal("missing args must be an error result")
	}
	if _, isErr := h.tool(t, "propose_change", map[string]any{"kind": "Deployment", "name": "web", "namespace": "demo", "merge_patch": map[string]any{}, "summary": "x"}); !isErr {
		t.Fatal("empty patch must be an error result")
	}
	if _, ok := h.rpc(t, "ping", nil)["result"]; !ok {
		t.Fatal("server must keep working")
	}
}

func TestLineDiff(t *testing.T) {
	d := lineDiff("a\nb\nc\nd\ne\nf", "a\nb\nc\nX\ne\nf")
	if !strings.Contains(d, "-d") || !strings.Contains(d, "+X") || !strings.Contains(d, " c") {
		t.Fatal(d)
	}
}

func TestGrantDoesNotFollowAnotherClusterWithTheSameName(t *testing.T) {
	h := newHarness(t, false)
	h.enable(t, settings.AgentAccess{Write: true})
	a := h.settings.AgentAccess("kind-test")
	a.Fingerprint = "granted-to-another-cluster"
	if err := h.settings.SetAgentAccess("kind-test", a); err != nil {
		t.Fatal(err)
	}
	if ok, why := h.env.writesAllowed(kube.Context{Name: "kind-test", Server: "https://10.0.0.1:6443"}); ok || !strings.Contains(why, "another cluster") {
		t.Fatalf("a grant made for another cluster must not apply: %v %s", ok, why)
	}
}

// A proposal made by an engine feature with its own kind policy (Argo CD) must not be applied by
// an agent, even with write access: the agent kind list stays the AI/agent one.
func TestApplyRefusesProposalsOutsideAgentKinds(t *testing.T) {
	h := newHarness(t, false)
	h.enable(t, settings.AgentAccess{Write: true})
	sess, err := h.env.Connect(context.Background(), kube.Context{})
	if err != nil {
		t.Fatal(err)
	}
	allowAll := func(string, string) error { return nil }
	p, err := h.env.Changes.ProposeWith(context.Background(), sess, change.Ref{Group: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "admin-ish"}, labelPatch, "feature change", "argocd", false, allowAll) // no dry-run: the fake client would persist it
	if err != nil {
		t.Fatal(err)
	}
	if text, isErr := h.tool(t, "apply_change", map[string]any{"token": p.Token}); !isErr {
		t.Fatalf("agent applied a feature proposal: %s", text)
	}
	got, _ := sess.Dynamic.Resource(crRes.GVR()).Get(context.Background(), "admin-ish", metav1.GetOptions{})
	if got.GetLabels()["reviewed"] != "" {
		t.Fatalf("object changed: %v", got.GetLabels())
	}
}
