package flux

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	j, err := yaml.YAMLToJSON([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(j); err != nil {
		t.Fatal(err)
	}
	return u
}

const ks = `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: apps, namespace: flux-system}
spec:
  interval: 10m
  path: ./apps
  targetNamespace: web
  sourceRef: {kind: GitRepository, name: repo}
status:
  lastAppliedRevision: main@sha1:abc
  lastAttemptedRevision: main@sha1:def
  lastHandledReconcileAt: "2026-10-27T10:00:00Z"
  conditions:
  - {type: Ready, status: "False", reason: BuildFailed, message: "kustomize build failed", lastTransitionTime: "2026-10-27T10:01:00Z"}
`

const hr = `apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata: {name: podinfo, namespace: web}
spec:
  suspend: true
  chart:
    spec: {chart: podinfo, version: "6.x", sourceRef: {kind: HelmRepository, name: podinfo, namespace: flux-system}}
status:
  history: [{chartName: podinfo, chartVersion: 6.9.0, status: deployed}]
  conditions: [{type: Ready, status: "True", reason: InstallSucceeded}]
`

const git = `apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata: {name: repo, namespace: flux-system}
spec: {url: "https://example.com/repo.git", ref: {branch: main}}
status:
  artifact: {revision: main@sha1:abc, lastUpdateTime: "2026-10-27T09:00:00Z"}
  conditions: [{type: Ready, status: "True"}]
`

const bucket = `apiVersion: source.toolkit.fluxcd.io/v1
kind: Bucket
metadata: {name: b, namespace: flux-system}
spec: {endpoint: "minio.example.com", bucketName: manifests}
`

func TestParse(t *testing.T) {
	k := Parse(obj(t, ks))
	if k.Ready != "False" || k.Reason != "BuildFailed" || k.Revision != "main@sha1:abc" || k.Attempted != "main@sha1:def" || k.Path != "./apps" ||
		k.TargetNamespace != "web" || k.Source == nil || *k.Source != (Ref{Kind: "GitRepository", Namespace: "flux-system", Name: "repo"}) ||
		k.HandledAt == "" || k.Suspended || len(k.Conditions) != 1 || k.Group != KustomizeGroup {
		t.Fatalf("kustomization %+v", k)
	}
	h := Parse(obj(t, hr))
	if !h.Suspended || h.Ready != "True" || h.Chart != "podinfo@6.x" || h.Revision != "podinfo@6.9.0" || h.Source.Namespace != "flux-system" || h.Source.Kind != "HelmRepository" {
		t.Fatalf("helmrelease %+v", h)
	}
	g := Parse(obj(t, git))
	if g.Revision != "main@sha1:abc" || g.URL != "https://example.com/repo.git" || g.RefName != "main" || g.Ready != "True" || g.UpdatedAt == "" {
		t.Fatalf("git %+v", g)
	}
	b := Parse(obj(t, bucket))
	if b.URL != "minio.example.com/manifests" || b.Ready != "Unknown" {
		t.Fatalf("bucket %+v", b)
	}
}

func TestPatches(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.FixedZone("x", -3*3600))
	p, err := ReconcilePatch(obj(t, ks), now)
	if err != nil {
		t.Fatal(err)
	}
	ann := p["metadata"].(map[string]any)["annotations"].(map[string]any)
	if ann[ReconcileAnnotation] != "2026-10-27T15:00:00Z" || len(p) != 1 {
		t.Fatalf("reconcile %+v", p)
	}
	if _, err := ReconcilePatch(obj(t, hr), now); !errors.Is(err, ErrSuspended) {
		t.Fatalf("reconcile of a suspended object: %v", err)
	}
	if p, err := SuspendPatch(obj(t, ks)); err != nil || p["spec"].(map[string]any)["suspend"] != true {
		t.Fatalf("suspend %v %v", p, err)
	}
	if _, err := SuspendPatch(obj(t, hr)); !errors.Is(err, ErrAlreadySusp) {
		t.Fatalf("suspend twice: %v", err)
	}
	p, err = ResumePatch(obj(t, hr), now)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p["spec"].(map[string]any)["suspend"]; !ok || v != nil || p["metadata"] == nil {
		t.Fatalf("resume must remove spec.suspend and request a reconcile: %+v", p)
	}
	if _, err := ResumePatch(obj(t, ks), now); !errors.Is(err, ErrNotSuspended) {
		t.Fatalf("resume of a running object: %v", err)
	}
}

func TestAllow(t *testing.T) {
	for _, k := range []string{"Kustomization", "HelmRelease", "GitRepository", "OCIRepository", "HelmRepository", "Bucket"} {
		g, _ := GroupOf(k)
		if Allow(g, k) != nil {
			t.Errorf("%s refused", k)
		}
	}
	for _, c := range [][2]string{{"apps", "Deployment"}, {"", "Secret"}, {SourceGroup, "Kustomization"}, {"kustomize.config.k8s.io", "Kustomization"}, {SourceGroup, "HelmChart"}} {
		if Allow(c[0], c[1]) == nil {
			t.Errorf("%s/%s allowed", c[0], c[1])
		}
	}
}

var resources = []cluster.Resource{
	{Group: KustomizeGroup, Version: "v1", Resource: "kustomizations", Kind: "Kustomization", Namespaced: true},
	{Group: HelmGroup, Version: "v2", Resource: "helmreleases", Kind: "HelmRelease", Namespaced: true},
	{Group: SourceGroup, Version: "v1", Resource: "gitrepositories", Kind: "GitRepository", Namespaced: true},
	{Group: SourceGroup, Version: "v1", Resource: "ocirepositories", Kind: "OCIRepository", Namespaced: true},
	{Group: SourceGroup, Version: "v1", Resource: "helmrepositories", Kind: "HelmRepository", Namespaced: true},
	{Group: SourceGroup, Version: "v1", Resource: "buckets", Kind: "Bucket", Namespaced: true},
}

func session(t *testing.T, res []cluster.Resource, objs ...*unstructured.Unstructured) *cluster.Session {
	lists := map[schema.GroupVersionResource]string{}
	for _, r := range resources {
		lists[r.GVR()] = r.Kind + "List"
	}
	var rt []runtime.Object
	for _, u := range objs {
		rt = append(rt, u)
	}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), lists, rt...)
	return cluster.NewStaticSession(kube.Context{Name: "t"}, fake.NewSimpleClientset(), dyn, res, false)
}

func TestListAndDetect(t *testing.T) {
	s := session(t, resources, obj(t, ks), obj(t, hr), obj(t, git), obj(t, bucket))
	ov, err := List(context.Background(), s, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ov.Installed || len(ov.Kustomizations) != 1 || len(ov.HelmReleases) != 1 || len(ov.Sources) != 2 || ov.Sources[0].Kind != "Bucket" || ov.Hint != "" {
		t.Fatalf("overview %+v", ov)
	}
	ov, _ = List(context.Background(), s, "web")
	if len(ov.Kustomizations) != 0 || len(ov.HelmReleases) != 1 {
		t.Fatalf("namespace filter %+v", ov)
	}
	// Only the source controller: still installed, the other groups are reported absent.
	partial := session(t, resources[2:], obj(t, git))
	ov, _ = List(context.Background(), partial, "")
	if !ov.Installed || ov.Groups[KustomizeGroup] || !ov.Groups[SourceGroup] || len(ov.Sources) != 1 {
		t.Fatalf("partial %+v", ov)
	}
	// No Flux CRDs at all: the hint teaches what is missing.
	none := session(t, nil)
	ov, _ = List(context.Background(), none, "")
	if ov.Installed || ov.Hint == "" || Installed(none) {
		t.Fatalf("absent %+v", ov)
	}
}
