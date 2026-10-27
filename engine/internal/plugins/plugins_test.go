package plugins

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/jcottobboni/leme/engine/internal/project"
)

const lemeYAML = `
apiVersion: leme.dev/v1
name: rails
actions:
- id: console
  title: Rails console
  scopes: [pods]
  command: kubectl
  args: [exec, -it, $NAME, -n, $NAMESPACE, --, bin/rails, console]
  mode: terminal
- id: wipe
  scopes: [deployments]
  command: kubectl
  args: [delete, deploy, $NAME]
  mutates: true
links:
- id: grafana
  title: Grafana
  scopes: [deployments]
  url: https://grafana.example/d/x?var-ns=${NAMESPACE}&var-app=${NAME}
views:
- group: longhorn.io
  kind: Volume
  columns:
  - name: Estado
    jsonPath: .status.state
  - name: Réplicas
    expr: object.spec.numberOfReplicas
  fields:
  - name: Robustez
    jsonPath: .status.robustness
  health:
  - when: object.status.robustness == "faulted"
    status: error
  - when: object.status.robustness == "healthy"
    status: ok
rules:
- id: no-limits
  title: Contêiner sem limits
  severity: low
  kinds: [apps/Deployment]
  match: object.spec.template.spec.containers.exists(c, !has(c.resources) || !has(c.resources.limits))
  evidence: 'object.spec.template.spec.containers.filter(c, !has(c.resources) || !has(c.resources.limits)).map(c, "sem limits: " + c.name)'
`

func TestParseLeme(t *testing.T) {
	p, err := Parse([]byte(lemeYAML), "x", "/p/rails.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != FormatLeme || len(p.Actions) != 2 || len(p.Links) != 1 || len(p.Views) != 1 || len(p.Rules) != 1 {
		t.Fatalf("parsed %+v", p)
	}
	if a := p.Actions[1]; a.ID != "rails/wipe" || a.Mode != ModeOutput || !a.Mutates || a.Title != "wipe" {
		t.Fatalf("defaults: %+v", a)
	}
	bad := []string{
		"apiVersion: leme.dev/v2\nname: x\n",
		"apiVersion: leme.dev/v1\nname: k9s\n",
		"apiVersion: leme.dev/v1\nname: x\nactions:\n- {id: a, scopes: [pods]}\n",
		"apiVersion: leme.dev/v1\nname: x\nlinks:\n- {id: a, scopes: [pods], url: 'javascript:alert(1)'}\n",
		"apiVersion: leme.dev/v1\nname: x\nunknown: 1\n",
		"apiVersion: leme.dev/v1\nname: x\nactions:\n- {id: a, scopes: [pods], command: ls, mode: weird}\n",
	}
	for _, b := range bad {
		if _, err := Parse([]byte(b), "x", "s"); err == nil {
			t.Errorf("accepted %q", b)
		}
	}
}

func TestParseK9sShapes(t *testing.T) {
	full := `plugins:
  stern:
    shortCut: Ctrl-L
    description: Logs (stern)
    scopes: [pods]
    command: stern
    args: [--tail, "50", $FILTER, -n, $NAMESPACE, --context, $CONTEXT]
  nuke:
    scopes: [all]
    command: kubectl
    background: true
    dangerous: true
    confirm: true
    args: [delete, $RESOURCE_NAME, $NAME]
  piped:
    scopes: [pods]
    command: kubectl
    pipes: [less]
  helmonly:
    scopes: [helm]
    command: helm
`
	p, err := Parse([]byte(full), "plugins", "/k9s/plugins.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 {
		t.Fatalf("actions %+v warnings %v", p.Actions, p.Warnings)
	}
	nuke, stern := p.Actions[0], p.Actions[1]
	if nuke.ID != "k9s/nuke" || nuke.Mode != ModeBackground || !nuke.Mutates || !nuke.Confirm {
		t.Fatalf("nuke %+v", nuke)
	}
	if stern.Mode != ModeTerminal || stern.ShortCut != "Ctrl-L" || stern.Title != "Logs (stern)" {
		t.Fatalf("stern %+v", stern)
	}
	if len(p.Warnings) != 3 { // pipes, helm scope, helm-only ignored
		t.Fatalf("warnings %v", p.Warnings)
	}

	bare := "debug:\n  scopes: [containers]\n  command: kubectl\n  args: [debug, $POD]\n"
	if p, err := Parse([]byte(bare), "f", "s"); err != nil || len(p.Actions) != 1 || p.Actions[0].ID != "k9s/debug" {
		t.Fatalf("bare map: %v %+v", err, p)
	}
	single := "scopes: [pods]\ncommand: kubectl\nargs: [describe, pod, $NAME]\n"
	if p, err := Parse([]byte(single), "describe-pod", "s"); err != nil || len(p.Actions) != 1 || p.Actions[0].ID != "k9s/describe-pod" {
		t.Fatalf("single: %v %+v", err, p)
	}
	if _, err := Parse([]byte("foo: bar\n"), "x", "s"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestExpand(t *testing.T) {
	vars := Vars(Input{Resource: "pods", Name: "web-1", Namespace: "demo", Cols: map[string]string{"Ready": "1/1", "Node Name": "kind node"}}, "kind-x", "c", "u", "/tmp/k")
	got, err := Expand("$NAME.${NAMESPACE}:$POD $COL-READY $UNKNOWN", vars, true, nil)
	if err != nil || got != "web-1.demo:web-1 1/1 $UNKNOWN" {
		t.Fatalf("%q %v", got, err)
	}
	// Strict (shell): a value with a space is refused; non-strict passes it as one argv element.
	if _, err := Expand("echo $COL-NODE_NAME", vars, true, nil); err == nil {
		t.Fatal("unsafe value accepted in a shell script")
	}
	if got, err := Expand("$COL-NODE_NAME", vars, false, nil); err != nil || got != "kind node" {
		t.Fatalf("%q %v", got, err)
	}
	vars["KUBECONFIG"] = `C:\Users\A B\kc`
	if _, err := Expand("--kubeconfig=$KUBECONFIG", vars, true, map[string]bool{"KUBECONFIG": true}); err != nil {
		t.Fatal(err)
	}
	if !Strict("kubectl", []string{"exec", "x", "--", "sh", "-c", "echo"}) || !Strict("/bin/bash", nil) || !Strict("watch", nil) || Strict("kubectl", []string{"get", "pods"}) {
		t.Fatal("Strict")
	}
}

func TestTrustTiedToDefinition(t *testing.T) {
	dir := t.TempDir()
	a := Action{ID: "p/a", Command: "kubectl", Args: []string{"get", "pods"}, Mode: ModeOutput, Source: "/x.yaml"}
	a.Hash = hashAction(a)
	tr := OpenTrust(dir)
	if tr.Trusted(a) {
		t.Fatal("trusted by default")
	}
	if err := tr.Grant(a, "other"); err != ErrStale {
		t.Fatalf("grant with stale hash: %v", err)
	}
	if err := tr.Grant(a, a.Hash); err != nil || !OpenTrust(dir).Trusted(a) {
		t.Fatalf("grant: %v", err)
	}
	b := a
	b.Args = []string{"delete", "pods", "--all"}
	b.Hash = hashAction(b)
	if OpenTrust(dir).Trusted(b) {
		t.Fatal("changed command kept the trust")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(filepath.Join(dir, "plugins-trust.json"))
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
	_ = tr.Revoke("p/a")
	if OpenTrust(dir).Trusted(a) {
		t.Fatal("revoke")
	}
}

func TestSubresource(t *testing.T) {
	cases := map[string]string{
		"/api/v1/namespaces/n/pods/p/exec":                       "exec",
		"/api/v1/namespaces/n/pods/exec":                         "",
		"/api/v1/namespaces/n/pods":                              "",
		"/api/v1/namespaces/n":                                   "",
		"/api/v1/nodes/n1/proxy/metrics":                         "proxy",
		"/api/v1/namespaces/n/services/https:s:443/proxy/x":      "proxy",
		"/apis/apps/v1/namespaces/n/deployments/d/scale":         "scale",
		"/api/v1/proxy/namespaces/n/pods/p":                      "proxy",
		"/apis/apps/v1/namespaces/n/deployments":                 "",
		"/version":                                               "",
		"/api/v1/namespaces/n/pods/p/portforward":                "portforward",
		"/apis/authorization.k8s.io/v1/selfsubjectaccessreviews": "",
	}
	for p, want := range cases {
		if got := subresource(p); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
}

// TestProxyPolicy runs kubectl-like requests through the proxy against a fake API server.
func TestProxyPolicy(t *testing.T) {
	var seenAuth []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = append(seenAuth, r.Header.Get("Authorization"))
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"kind":"Status"}`)
	}))
	defer api.Close()
	locked, shell := true, false
	p, err := StartProxy(&rest.Config{Host: api.URL, BearerToken: "real-token"}, Policy{ReadOnly: func() bool { return locked }, Shell: func() bool { return shell }})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(p.CAData) || !strings.HasPrefix(p.URL, "https://127.0.0.1:") {
		t.Fatalf("proxy must serve HTTPS with its own CA: %s", p.URL)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	do := func(method, path, tok string) int {
		req, _ := http.NewRequest(method, p.URL+path, strings.NewReader("{}"))
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	checks := []struct {
		method, path, tok string
		want              int
	}{
		{"GET", "/api/v1/pods", "", 401},
		{"GET", "/api/v1/pods", "wrong", 401},
		{"GET", "/api/v1/namespaces/n/pods?watch=1", p.Token, 200},
		{"DELETE", "/api/v1/namespaces/n/pods/p", p.Token, 403},
		{"PATCH", "/apis/apps/v1/namespaces/n/deployments/d", p.Token, 403},
		{"POST", "/api/v1/namespaces/n/pods/p/exec", p.Token, 403},
		{"GET", "/api/v1/namespaces/n/pods/p/exec?command=sh", p.Token, 403}, // websocket exec is a GET
		{"GET", "/api/v1/namespaces/n/services/s/proxy/admin", p.Token, 403},
		{"GET", "/api/v1/namespaces/n/pods//p/exec", p.Token, 403},
		{"POST", "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", p.Token, 200},
	}
	for _, c := range checks {
		if got := do(c.method, c.path, c.tok); got != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, got, c.want)
		}
	}
	shell = true
	if got := do("POST", "/api/v1/namespaces/n/pods/p/exec", p.Token); got != 200 {
		t.Errorf("exec with shell: %d", got)
	}
	if got := do("DELETE", "/api/v1/namespaces/n/pods/p", p.Token); got != 403 {
		t.Errorf("delete with shell only: %d", got)
	}
	locked = false
	if got := do("DELETE", "/api/v1/namespaces/n/pods/p", p.Token); got != 200 {
		t.Errorf("delete unlocked: %d", got)
	}
	for _, a := range seenAuth {
		if a != "Bearer real-token" {
			t.Fatalf("API server saw %q: the plugin token leaked or credentials were missing", a)
		}
	}
}

func TestKubeconfigPointsToProxyOnly(t *testing.T) {
	dir := t.TempDir()
	p := &Proxy{URL: "https://127.0.0.1:1", Token: "tok", CAData: []byte("-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n")}
	path, err := WriteKubeconfig(dir, p, "prod-ctx", "prod-cluster", "admin", "ns1")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentContext != "prod-ctx" || len(cfg.Contexts) != 1 || cfg.Clusters["prod-cluster"].Server != p.URL || cfg.AuthInfos["admin"].Token != "tok" || len(cfg.Clusters["prod-cluster"].CertificateAuthorityData) == 0 || cfg.Contexts["prod-ctx"].Namespace != "ns1" {
		t.Fatalf("%+v", cfg)
	}
}

func TestPrepareRefusesInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	api := httptest.NewServer(http.NotFoundHandler())
	defer api.Close()
	tg := Target{Config: &rest.Config{Host: api.URL}, Context: "c", Policy: Policy{ReadOnly: func() bool { return true }, Shell: func() bool { return false }}}
	a := Action{ID: "p/a", Command: "sh", Args: []string{"-c", "echo $COL-X"}, Mode: ModeOutput}
	if _, err := Prepare(context.Background(), a, Input{Name: "n", Cols: map[string]string{"X": "a; rm -rf /"}}, tg); err == nil {
		t.Fatal("shell injection accepted")
	}
	b := Action{ID: "p/b", Command: "echo", Args: []string{"$COL-X"}, Mode: ModeOutput}
	if _, err := Prepare(context.Background(), b, Input{Name: "n", Cols: map[string]string{"X": "--server=http://evil"}}, tg); err == nil {
		t.Fatal("value turned into an option")
	}
}

func TestRunOutputAndEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	api := httptest.NewServer(http.NotFoundHandler())
	defer api.Close()
	t.Setenv("KUBECONFIG", "/real/config")
	tg := Target{Config: &rest.Config{Host: api.URL}, Context: "kind-x", Policy: Policy{ReadOnly: func() bool { return true }, Shell: func() bool { return false }}}
	a := Action{ID: "p/a", Command: "sh", Args: []string{"-c", `echo "$NAME|$NAMESPACE|$CONTEXT|$POD"; test "$KUBECONFIG" != /real/config && test -f "$KUBECONFIG" && echo kc-ok; exit 3`}, Mode: ModeOutput}
	p, err := Prepare(context.Background(), a, Input{Resource: "pods", Name: "web-1", Namespace: "demo"}, tg)
	if err != nil {
		t.Fatal(err)
	}
	kc := filepath.Join(p.dir, "kubeconfig")
	res := Run(p)
	if res.ExitCode != 3 || !strings.Contains(res.Output, "web-1|demo|kind-x|web-1") || !strings.Contains(res.Output, "kc-ok") {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(kc); !os.IsNotExist(err) {
		t.Fatal("temporary kubeconfig left behind")
	}
}

func TestRunCancelKillsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	api := httptest.NewServer(http.NotFoundHandler())
	defer api.Close()
	tg := Target{Config: &rest.Config{Host: api.URL}, Context: "c", Policy: Policy{ReadOnly: func() bool { return true }, Shell: func() bool { return false }}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	p, err := Prepare(ctx, Action{Command: "sh", Args: []string{"-c", "sleep 30 & sleep 30"}, Mode: ModeOutput}, Input{Name: "n"}, tg)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	Run(p)
	if time.Since(t0) > 5*time.Second {
		t.Fatal("cancel did not kill the process group")
	}
}

func TestViewAndRules(t *testing.T) {
	p, err := Parse([]byte(lemeYAML), "x", "s")
	if err != nil {
		t.Fatal(err)
	}
	v, err := compileView("rails", p.Views[0])
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "longhorn.io/v1beta2", "kind": "Volume",
		"metadata": map[string]any{"name": "pvc-1"}, "spec": map[string]any{"numberOfReplicas": int64(3)}, "status": map[string]any{"state": "attached", "robustness": "faulted"}}}
	r := project.Row{Health: "neutral"}
	v.Apply(u, &r)
	if r.Health != "error" || r.Cells["pl:Estado"] != "attached" || r.Cells["pl:Réplicas"] != int64(3) {
		t.Fatalf("%+v", r)
	}
	if f := v.Fields(u); len(f) != 1 || f[0].Value != "faulted" {
		t.Fatalf("fields %+v", f)
	}
	rule, err := compileRule("rails", p.Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	dep := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "web", "namespace": "demo"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": "app", "resources": map[string]any{"limits": map[string]any{"cpu": "1"}}},
			map[string]any{"name": "sidecar"},
		}}}}}}
	fs := RuleSet{rule}.Check(dep)
	if len(fs) != 1 || fs[0].ID != "plugin:rails/no-limits" || fs[0].Severity != "low" || len(fs[0].Evidence) != 1 || fs[0].Evidence[0] != "sem limits: sidecar" {
		t.Fatalf("%+v", fs)
	}
	sts := dep.DeepCopy()
	sts.SetKind("StatefulSet")
	if len(RuleSet{rule}.Check(sts)) != 0 {
		t.Fatal("rule applied to another kind")
	}
	if _, err := compile("object.spec.("); err == nil {
		t.Fatal("bad CEL compiled")
	}
	if _, err := compile(`[1,2,3].map(x, [1,2,3,4,5,6,7,8,9,10].map(y, [1,2,3,4,5,6,7,8,9,10].map(z, [1,2,3,4,5,6,7,8,9,10].map(w, x+y+z+w))))`); err != nil {
		t.Fatal(err)
	}
}

func TestCELCostLimit(t *testing.T) {
	prg, err := compile(`[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].all(a, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].all(b, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].all(c, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].all(d, [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20].all(e, a+b+c+d+e > 0)))))`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prg.Bool(map[string]any{}); err == nil {
		t.Fatal("expensive expression was not stopped")
	}
}

func TestRegistryReloadAndK9sToggle(t *testing.T) {
	cfg := t.TempDir()
	k9s := t.TempDir()
	t.Setenv("K9S_CONFIG_DIR", k9s)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_ = os.MkdirAll(filepath.Join(cfg, "plugins"), 0o700)
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg, "plugins", "rails.yaml"), lemeYAML)
	write(filepath.Join(cfg, "plugins", "broken.yaml"), "apiVersion: leme.dev/v1\nname: broken\nrules:\n- {id: r, title: t, kinds: [Pod], match: 'object.('}\n")
	write(filepath.Join(k9s, "plugins.yaml"), "plugins:\n  logs:\n    scopes: [po]\n    command: kubectl\n    args: [logs, $NAME]\n")
	on := true
	r := NewRegistry(cfg, func() bool { return on })
	s := r.Current()
	if len(s.Actions) != 3 || len(s.Views) != 1 || len(s.Rules) != 1 || len(s.Files) != 3 {
		t.Fatalf("snapshot %+v", s.Files)
	}
	var broken File
	for _, f := range s.Files {
		if strings.HasSuffix(f.Path, "broken.yaml") {
			broken = f
		}
	}
	if broken.Error == "" {
		t.Fatal("CEL error not reported on its file")
	}
	if a, ok := s.Action("k9s/logs"); !ok || a.Hash == "" || a.Format != FormatK9s {
		t.Fatalf("k9s action %+v", a)
	}
	on = false
	if s := r.Reload(); len(s.Actions) != 2 {
		t.Fatalf("k9s off: %d actions", len(s.Actions))
	}
	write(filepath.Join(cfg, "plugins", "rails.yaml"), strings.Replace(lemeYAML, "bin/rails, console", "bin/rails, dbconsole", 1))
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(cfg, "plugins", "rails.yaml"), future, future)
	r.checked = time.Time{}
	if a, _ := r.Current().Action("rails/console"); !strings.Contains(strings.Join(a.Args, " "), "dbconsole") {
		t.Fatalf("not reloaded: %v", a.Args)
	}
}
