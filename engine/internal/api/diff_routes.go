package api

import (
	"context"
	"net/http"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/objdiff"
	"github.com/jcottobboni/leme/engine/internal/ops"
)

// Diff of the same object between clusters and/or namespaces (M7, RF03). Read-only: both objects
// are read with the user's credentials, normalized here (single rule, objdiff.Normalize) and Secret
// values are masked on both sides; which hidden values differ is reported by key only.

func (s *Server) diffRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/diff", s.diffObjects)
}

type diffSide struct {
	Cluster   string `json:"cluster"`
	Group     string `json:"group"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type diffSideResult struct {
	YAML  string `json:"yaml"`
	Found bool   `json:"found"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error,omitempty"`

	norm map[string]any // the normalized (and masked) object, for Changes
}

// maxDiffChanges bounds the summary of changed paths.
const maxDiffChanges = 200

type diffResult struct {
	Left  diffSideResult `json:"left"`
	Right diffSideResult `json:"right"`
	// Equal: both exist and their normalized YAML (and hidden values) are the same.
	Equal bool `json:"equal"`
	// HiddenDiffer lists the Secret keys present on both sides whose (masked) values differ.
	HiddenDiffer []string `json:"hiddenDiffer,omitempty"`
	// Changes are the JSON Patch operations from left to right (normalized objects, values masked
	// like the YAML), at most 200.
	Changes []objdiff.Op `json:"changes,omitempty"`
	// NamespaceIgnored: the sides are in different namespaces, so metadata.namespace is not compared.
	NamespaceIgnored bool `json:"namespaceIgnored,omitempty"`
}

func (s *Server) diffObjects(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Left  diffSide `json:"left"`
		Right diffSide `json:"right"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Left.Name == "" || body.Right.Name == "" || body.Left.Resource == "" || body.Right.Resource == "" {
		httpError(w, http.StatusBadRequest, "left and right need resource and name")
		return
	}
	writeJSON(w, compareObjects(r.Context(), s.mgr.Get, body.Left, body.Right))
}

// compareObjects reads both sides from the connected sessions (get) and compares them.
func compareObjects(ctx context.Context, get func(string) (*cluster.Session, bool), left, right diffSide) diffResult {
	out := diffResult{NamespaceIgnored: left.Namespace != right.Namespace}
	norm := objdiff.NormalizeOptions{DropNamespace: out.NamespaceIgnored}
	lu, lres := diffFetch(ctx, get, left, norm)
	ru, rres := diffFetch(ctx, get, right, norm)
	out.Left, out.Right = lres, rres
	if lu != nil && ru != nil {
		out.HiddenDiffer = hiddenDiffer(lu, ru)
		out.Equal = lres.YAML == rres.YAML && len(out.HiddenDiffer) == 0
		out.Changes = objdiff.Diff(lres.norm, rres.norm)
		if len(out.Changes) > maxDiffChanges {
			out.Changes = out.Changes[:maxDiffChanges]
		}
	}
	return out
}

// diffFetch reads one side; the returned object is the raw one (for comparing hidden values).
func diffFetch(ctx context.Context, get func(string) (*cluster.Session, bool), d diffSide, norm objdiff.NormalizeOptions) (*unstructured.Unstructured, diffSideResult) {
	sess, ok := get(d.Cluster)
	if !ok {
		return nil, diffSideResult{Error: "cluster não conectado"}
	}
	res, ok := sess.Lookup(d.Group, d.Resource)
	if !ok {
		return nil, diffSideResult{Error: "este tipo de recurso não existe neste cluster"}
	}
	ns := d.Namespace
	if !res.Namespaced {
		ns = ""
	}
	u, err := sess.Dynamic.Resource(res.GVR()).Namespace(ns).Get(ctx, d.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, diffSideResult{Kind: res.Kind}
	}
	if err != nil {
		return nil, diffSideResult{Kind: res.Kind, Error: err.Error()}
	}
	n := objdiff.Normalize(u, norm)
	if isSecret(u) {
		ops.MaskSecret(n)
	}
	y, err := yaml.Marshal(n.Object)
	if err != nil {
		return nil, diffSideResult{Kind: res.Kind, Error: err.Error()}
	}
	return u, diffSideResult{YAML: string(y), Found: true, Kind: res.Kind, norm: n.Object}
}

// isSecret: a core Secret (a CRD named Secret in another group is not masked by this rule).
func isSecret(u *unstructured.Unstructured) bool {
	return u.GetKind() == "Secret" && u.GetAPIVersion() == "v1"
}

// hiddenDiffer compares the masked values of two Secrets key by key, without revealing them.
func hiddenDiffer(a, b *unstructured.Unstructured) []string {
	if !isSecret(a) || !isSecret(b) {
		return nil
	}
	var out []string
	for _, field := range []string{"data", "stringData"} {
		ma, _, _ := unstructured.NestedMap(a.Object, field)
		mb, _, _ := unstructured.NestedMap(b.Object, field)
		for k, va := range ma {
			if vb, ok := mb[k]; ok && va != vb {
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
