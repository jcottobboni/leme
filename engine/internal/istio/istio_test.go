package istio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func ns(name string, labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace"}}
	u.SetName(name)
	u.SetLabels(labels)
	return u
}

func pa(namespace, name, mode string, age time.Duration, selector map[string]string, ports map[string]string) *unstructured.Unstructured {
	spec := map[string]any{}
	if mode != "" {
		spec["mtls"] = map[string]any{"mode": mode}
	}
	if selector != nil {
		ml := map[string]any{}
		for k, v := range selector {
			ml[k] = v
		}
		spec["selector"] = map[string]any{"matchLabels": ml}
	}
	if ports != nil {
		pl := map[string]any{}
		for k, v := range ports {
			pl[k] = map[string]any{"mode": v}
		}
		spec["portLevelMtls"] = pl
	}
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "security.istio.io/v1", "kind": "PeerAuthentication", "spec": spec}}
	u.SetNamespace(namespace)
	u.SetName(name)
	u.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(age)))
	return u
}

func TestResolveMTLSPrecedence(t *testing.T) {
	namespaces := []*unstructured.Unstructured{
		ns("istio-system", nil), ns("strict", map[string]string{"istio-injection": "enabled"}), ns("inherit", map[string]string{"istio.io/rev": "canary"}),
		ns("unset", nil), ns("plain", map[string]string{"istio-injection": "disabled"}),
	}
	peer := []*unstructured.Unstructured{
		pa("istio-system", "default", "PERMISSIVE", 0, nil, nil),
		// A workload policy in the root namespace is not mesh-wide.
		pa("istio-system", "gw", "DISABLE", -time.Hour, map[string]string{"app": "gw"}, nil),
		pa("strict", "default", "STRICT", 0, nil, nil),
		pa("strict", "newer", "DISABLE", time.Hour, nil, nil), // the oldest namespace-wide policy wins
		pa("strict", "reviews", "", 0, map[string]string{"app": "reviews"}, map[string]string{"9080": "DISABLE", "8080": "UNSET"}),
		pa("unset", "default", "UNSET", 0, nil, nil), // UNSET inherits the mesh
		pa("plain", "db", "DISABLE", 0, map[string]string{"app": "db"}, nil),
	}
	got := map[string]NamespaceMTLS{}
	for _, r := range ResolveMTLS(namespaces, peer, "") {
		got[r.Name] = r
	}
	check := func(n, mode, source, policy, inj string) {
		t.Helper()
		r := got[n]
		if r.MTLS != mode || r.Source != source || r.Policy != policy || r.Injection != inj {
			t.Errorf("%s: got %s/%s/%s/%q want %s/%s/%s/%q", n, r.MTLS, r.Source, r.Policy, r.Injection, mode, source, policy, inj)
		}
	}
	check("strict", Strict, SourceNamespace, "strict/default", "enabled")
	check("inherit", Permissive, SourceMesh, "istio-system/default", "rev:canary")
	check("unset", Permissive, SourceMesh, "istio-system/default", "")
	check("istio-system", Permissive, SourceMesh, "istio-system/default", "")
	check("plain", Permissive, SourceMesh, "istio-system/default", "disabled")

	ex := got["strict"].Exceptions
	if len(ex) != 1 || ex[0].Policy != "strict/reviews" || ex[0].Mode != Unset || ex[0].Effective != Strict {
		t.Fatalf("strict exceptions: %+v", ex)
	}
	if len(ex[0].Ports) != 2 || ex[0].Ports[0].Port != 8080 || ex[0].Ports[0].Effective != Strict || ex[0].Ports[1].Port != 9080 || ex[0].Ports[1].Effective != Disable {
		t.Fatalf("port exceptions: %+v", ex[0].Ports)
	}
	if e := got["istio-system"].Exceptions; len(e) != 1 || e[0].Effective != Disable {
		t.Fatalf("root workload exception: %+v", e)
	}

	// Nothing set anywhere: UNSET from the default (Istio then accepts plaintext and mTLS).
	none := ResolveMTLS([]*unstructured.Unstructured{ns("a", nil)}, nil, "")
	if none[0].MTLS != Unset || none[0].Source != SourceDefault {
		t.Fatalf("default: %+v", none[0])
	}
	// The namespace wins over a STRICT mesh.
	r := ResolveMTLS([]*unstructured.Unstructured{ns("a", nil)}, []*unstructured.Unstructured{pa("istio-system", "default", "STRICT", 0, nil, nil), pa("a", "x", "PERMISSIVE", 0, nil, nil)}, "")
	if r[0].MTLS != Permissive || r[0].Source != SourceNamespace {
		t.Fatalf("namespace over mesh: %+v", r[0])
	}
	// A custom root namespace.
	r = ResolveMTLS([]*unstructured.Unstructured{ns("a", nil)}, []*unstructured.Unstructured{pa("mesh-root", "default", "STRICT", 0, nil, nil)}, "mesh-root")
	if r[0].MTLS != Strict || r[0].Source != SourceMesh {
		t.Fatalf("custom root: %+v", r[0])
	}
}

const reviewsVS = `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata: {name: reviews, namespace: shop}
spec:
  hosts: [reviews]
  gateways: [shop-gw, mesh]
  http:
  - name: jason
    match:
    - headers: {end-user: {exact: jason}}
      uri: {prefix: /api}
    - port: 8080
    route:
    - destination: {host: reviews, subset: v3}
    timeout: 5s
  - route:
    - destination: {host: reviews, subset: v1, port: {number: 9080}}
      weight: 80
    - destination: {host: reviews.shop.svc.cluster.local, subset: v2}
      weight: 20
  tcp:
  - match: [{port: 3306}]
    route:
    - destination: {host: mysql.db}
  tls:
  - match: [{sniHosts: [a.example.com], port: 443}]
    route:
    - destination: {host: external.example.com}
`

func TestParseVirtualServiceWeights(t *testing.T) {
	svcs := Services{"shop/reviews": true, "db/mysql": true}
	vs := ParseVirtualService(obj(t, reviewsVS), svcs)
	if strings.Join(vs.Hosts, ",") != "reviews" || strings.Join(vs.Gateways, ",") != "shop-gw,mesh" {
		t.Fatalf("hosts/gateways: %+v", vs)
	}
	if len(vs.HTTP) != 2 || len(vs.TCP) != 1 || len(vs.TLS) != 1 {
		t.Fatalf("routes: %+v", vs)
	}
	j := vs.HTTP[0]
	if j.Name != "jason" || len(j.Match) != 2 || j.Match[0] != "uri prefix /api · header end-user exact jason" || j.Match[1] != "porta 8080" {
		t.Fatalf("match: %q", j.Match)
	}
	if len(j.Destinations) != 1 || j.Destinations[0].Weight != 100 || j.Destinations[0].Subset != "v3" {
		t.Fatalf("single destination gets 100: %+v", j.Destinations)
	}
	if len(j.Notes) != 1 || j.Notes[0] != "timeout 5s" {
		t.Fatalf("notes: %q", j.Notes)
	}
	w := vs.HTTP[1].Destinations
	if len(w) != 2 || w[0].Weight != 80 || w[1].Weight != 20 || w[0].Subset != "v1" || w[1].Subset != "v2" || w[0].Port != 9080 {
		t.Fatalf("80/20: %+v", w)
	}
	for _, d := range w {
		if d.Service != (ServiceRef{Namespace: "shop", Name: "reviews", Found: true}) {
			t.Fatalf("service link: %+v", d.Service)
		}
	}
	if d := vs.TCP[0].Destinations[0]; d.Weight != 100 || d.Service != (ServiceRef{Namespace: "db", Name: "mysql", Found: true}) || vs.TCP[0].Match[0] != "porta 3306" {
		t.Fatalf("tcp: %+v", vs.TCP[0])
	}
	if d := vs.TLS[0].Destinations[0]; d.Service.Found || d.Service.Name != "" || vs.TLS[0].Match[0] != "porta 443 · sni a.example.com" {
		t.Fatalf("tls external: %+v", vs.TLS[0])
	}
}

func TestDestinationRuleServiceLink(t *testing.T) {
	svcs := Services{"shop/reviews": true}
	cases := []struct {
		host string
		want ServiceRef
	}{
		{"reviews", ServiceRef{"shop", "reviews", true}},
		{"reviews.shop", ServiceRef{"shop", "reviews", true}},
		{"reviews.shop.svc", ServiceRef{"shop", "reviews", true}},
		{"reviews.shop.svc.cluster.local", ServiceRef{"shop", "reviews", true}},
		{"ratings", ServiceRef{"shop", "ratings", false}},
		{"ratings.other.svc.cluster.local", ServiceRef{"other", "ratings", false}},
		{"www.example.com", ServiceRef{}},
		{"example.com", ServiceRef{}},
		{"*.shop.svc.cluster.local", ServiceRef{}},
	}
	for _, c := range cases {
		u := obj(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata: {name: dr, namespace: shop}
spec:
  host: "`+c.host+`"
  trafficPolicy: {tls: {mode: ISTIO_MUTUAL}}
  subsets:
  - {name: v1, labels: {version: v1}}
  - name: v2
    labels: {version: v2}
    trafficPolicy: {tls: {mode: DISABLE}}
`)
		dr := ParseDestinationRule(u, svcs)
		if dr.Service != c.want {
			t.Errorf("%s: got %+v want %+v", c.host, dr.Service, c.want)
		}
		if dr.TLSMode != "ISTIO_MUTUAL" || len(dr.Subsets) != 2 || dr.Subsets[1].Labels["version"] != "v2" || dr.Subsets[1].TLSMode != "DISABLE" {
			t.Fatalf("dr: %+v", dr)
		}
	}
}

func TestParseGateway(t *testing.T) {
	g := ParseGateway(obj(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata: {name: gw, namespace: shop}
spec:
  selector: {istio: ingressgateway}
  servers:
  - port: {number: 80, name: http, protocol: HTTP}
    hosts: ["shop.example.com"]
  - port: {number: 443, name: https, protocol: HTTPS}
    hosts: ["*"]
    tls: {mode: SIMPLE, credentialName: shop-cert}
`))
	if g.Selector["istio"] != "ingressgateway" || len(g.Servers) != 2 || g.Servers[1].Port != 443 || g.Servers[1].TLSMode != "SIMPLE" || g.Servers[0].Protocol != "HTTP" {
		t.Fatalf("gateway: %+v", g)
	}
}

func TestParseAnalyze(t *testing.T) {
	out := []byte(`[
  {"code":"IST0101","documentationUrl":"https://istio.io/latest/docs/reference/config/analysis/ist0101/","level":"Error","message":"Referenced host not found: \"ratings\"","origin":"VirtualService shop/reviews"},
  {"code":"IST0102","level":"Info","message":"The namespace is not enabled for Istio injection.","origin":"Namespace shop"}
]`)
	msgs, err := ParseAnalyze(out)
	if err != nil || len(msgs) != 2 || msgs[0].Code != "IST0101" || msgs[0].Level != "Error" || msgs[0].Resource != "VirtualService shop/reviews" || msgs[1].Resource != "Namespace shop" {
		t.Fatalf("parse: %+v %v", msgs, err)
	}
	if msgs, err := ParseAnalyze([]byte("  \n")); err != nil || len(msgs) != 0 {
		t.Fatalf("empty: %+v %v", msgs, err)
	}
	if msgs, err := ParseAnalyze([]byte("[]")); err != nil || msgs == nil || len(msgs) != 0 {
		t.Fatalf("empty array: %+v %v", msgs, err)
	}
	if msgs, err := ParseAnalyze([]byte(`{"messages":[{"code":"IST0118","level":"Warning","message":"m","reference":"Service shop/x"}]}`)); err != nil || msgs[0].Resource != "Service shop/x" {
		t.Fatalf("wrapped: %+v %v", msgs, err)
	}
	if _, err := ParseAnalyze([]byte("Error: boom")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func TestExportExcludesSecretsAndMasksEnv(t *testing.T) {
	dir := t.TempDir()
	objs := []*unstructured.Unstructured{
		obj(t, "apiVersion: v1\nkind: Secret\nmetadata: {name: s, namespace: shop}\ndata: {password: aHVudGVyMg==}\n"),
		obj(t, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c, namespace: shop}\ndata: {k: v}\n"),
		obj(t, `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
  annotations: {kubectl.kubernetes.io/last-applied-configuration: "{\"env\":\"hunter2\"}", keep: "yes"}
  managedFields: [{manager: kubectl}]
spec:
  template:
    spec:
      containers:
      - name: app
        env:
        - {name: DB_PASSWORD, value: hunter2}
        - name: FROM_SECRET
          valueFrom: {secretKeyRef: {name: s, key: password}}
status: {replicas: 1}
`),
		obj(t, reviewsVS),
	}
	n, err := Export(objs, dir)
	if err != nil || n != 2 {
		t.Fatalf("export: n=%d err=%v", n, err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(dir, f.Name()))
		s := string(b)
		if strings.Contains(s, "kind: Secret") || strings.Contains(s, "kind: ConfigMap") || strings.Contains(s, "hunter2") || strings.Contains(s, "managedFields") || strings.Contains(s, "status:") {
			t.Fatalf("%s leaks: %s", f.Name(), s)
		}
		if strings.Contains(f.Name(), "deployment") && (!strings.Contains(s, "DB_PASSWORD") || !strings.Contains(s, "<redacted>") || !strings.Contains(s, "keep:")) {
			t.Fatalf("deployment export: %s", s)
		}
		if st, _ := f.Info(); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
}

type fakeLookup struct{ res []cluster.Resource }

func (f fakeLookup) LookupKind(g, k string) (cluster.Resource, bool) {
	for _, r := range f.res {
		if r.Group == g && r.Kind == k {
			return r, true
		}
	}
	return cluster.Resource{}, false
}

func (f fakeLookup) Lookup(g, rs string) (cluster.Resource, bool) {
	for _, r := range f.res {
		if r.Group == g && r.Resource == rs {
			return r, true
		}
	}
	return cluster.Resource{}, false
}

func (f fakeLookup) Resources() []cluster.Resource { return f.res }

func TestLoadAndExportInput(t *testing.T) {
	if rep := Load(context.Background(), fakeLookup{}, nil, ""); rep.Installed || rep.Namespaces == nil || rep.VirtualServices == nil {
		t.Fatalf("without CRDs: %+v", rep)
	}
	res := []cluster.Resource{
		{Group: "security.istio.io", Version: "v1beta1", Resource: "peerauthentications", Kind: "PeerAuthentication", Namespaced: true},
		{Group: "networking.istio.io", Version: "v1", Resource: "virtualservices", Kind: "VirtualService", Namespaced: true},
		{Group: "networking.istio.io", Version: "v1", Resource: "destinationrules", Kind: "DestinationRule", Namespaced: true},
		{Group: "", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true},
		{Group: "", Version: "v1", Resource: "namespaces", Kind: "Namespace"},
		{Group: "", Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true},
		{Group: "", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
	}
	svc := obj(t, "apiVersion: v1\nkind: Service\nmetadata: {name: reviews, namespace: shop}\n")
	data := map[string][]*unstructured.Unstructured{
		"peerauthentications": {pa("istio-system", "default", "STRICT", 0, nil, nil), pa("shop", "default", "PERMISSIVE", 0, nil, nil)},
		"virtualservices":     {obj(t, reviewsVS)},
		"destinationrules":    {obj(t, "apiVersion: networking.istio.io/v1\nkind: DestinationRule\nmetadata: {name: reviews, namespace: shop}\nspec: {host: reviews}\n")},
		"services":            {svc},
		"namespaces":          {ns("shop", nil), ns("istio-system", nil), ns("other", nil)},
		"secrets":             {obj(t, "apiVersion: v1\nkind: Secret\nmetadata: {name: s, namespace: shop}\n")},
		"configmaps":          {obj(t, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c, namespace: shop}\n")},
	}
	var calls []string
	list := func(_ context.Context, r cluster.Resource, n string) ([]*unstructured.Unstructured, error) {
		calls = append(calls, r.Resource+"@"+n)
		var out []*unstructured.Unstructured
		for _, o := range data[r.Resource] {
			if n == "" || o.GetNamespace() == n {
				out = append(out, o)
			}
		}
		return out, nil
	}
	rep := Load(context.Background(), fakeLookup{res}, func(_ context.Context, r cluster.Resource, n string) ([]*unstructured.Unstructured, error) {
		var out []*unstructured.Unstructured
		for _, o := range data[r.Resource] {
			if n == "" || o.GetNamespace() == n {
				out = append(out, o)
			}
		}
		return out, nil
	}, "shop")
	if !rep.Installed || len(rep.Kinds) != 3 || rep.Kinds[0] != "security.istio.io/v1beta1 PeerAuthentication" {
		t.Fatalf("kinds: %+v", rep.Kinds)
	}
	if len(rep.Namespaces) != 1 || rep.Namespaces[0].MTLS != Permissive || rep.Namespaces[0].Source != SourceNamespace {
		t.Fatalf("namespaces: %+v", rep.Namespaces)
	}
	if len(rep.VirtualServices) != 1 || len(rep.DestinationRules) != 1 || !rep.DestinationRules[0].Service.Found || len(rep.Gateways) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	objs, errs := ExportInput(context.Background(), fakeLookup{res}, list, "shop")
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	kinds := map[string]int{}
	for _, o := range objs {
		kinds[o.GetKind()]++
	}
	if kinds["Secret"]+kinds["ConfigMap"] != 0 || kinds["VirtualService"] != 1 || kinds["Service"] != 1 || kinds["Namespace"] != 2 || kinds["PeerAuthentication"] != 1 {
		t.Fatalf("export input: %v", kinds)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "secrets@") || strings.HasPrefix(c, "configmaps@") {
			t.Fatalf("listed %s", c)
		}
	}
}

// TestAnalyzeIsolated runs a fake istioctl to check the arguments, the environment and cleanup.
func TestAnalyzeIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	bin := filepath.Join(t.TempDir(), "istioctl")
	log := filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\nenv >> " + log + "\nls \"$7\" >> " + log + "\n" +
		`echo '[{"code":"IST0101","level":"Error","message":"Referenced host not found","origin":"VirtualService shop/reviews"}]'` + "\nexit 79\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", "/home/someone/.kube/config")
	msgs, err := Analyze(context.Background(), bin, []*unstructured.Unstructured{obj(t, reviewsVS)}, "shop")
	if err != nil || len(msgs) != 1 || msgs[0].Code != "IST0101" {
		t.Fatalf("analyze: %+v %v", msgs, err)
	}
	b, _ := os.ReadFile(log)
	s := string(b)
	if !strings.HasPrefix(s, "analyze --use-kube=false -o json --namespace shop ") || !strings.Contains(s, "KUBECONFIG="+os.DevNull) || strings.Contains(s, ".kube/config") || !strings.Contains(s, "virtualservice-shop-reviews.yaml") {
		t.Fatalf("invocation: %s", s)
	}
	dir := strings.Fields(strings.SplitN(s, "\n", 2)[0])[6]
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("export dir %s not removed", dir)
	}
	if _, err := Analyze(context.Background(), bin, nil, ""); err == nil {
		t.Fatal("empty export must fail")
	}
}
