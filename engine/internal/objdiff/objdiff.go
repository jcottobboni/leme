// Package objdiff is the single source of the rules used to compare Kubernetes objects: the
// normalization that drops server bookkeeping before a side-by-side diff (between clusters or
// namespaces) and a structural diff that produces JSON Patch (RFC 6902) operations, used by the
// change history.
package objdiff

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ControlAnnotations are written by clients and controllers, not by the user: they differ between
// two copies of the same manifest and say nothing about the desired state.
var ControlAnnotations = []string{
	"kubectl.kubernetes.io/last-applied-configuration",
	"deployment.kubernetes.io/revision",
}

// NormalizeOptions tune Normalize.
type NormalizeOptions struct {
	// DropNamespace removes metadata.namespace (comparing the same object in two namespaces).
	DropNamespace bool
}

// Normalize returns a deep copy of u without what the API server or controllers maintain:
// managedFields, resourceVersion, uid, creationTimestamp, generation, selfLink, status, the uid of
// owner references and the control annotations (see ControlAnnotations). Empty label and annotation
// maps are dropped too, so "no annotations" and "only control annotations" compare equal.
func Normalize(u *unstructured.Unstructured, o NormalizeOptions) *unstructured.Unstructured {
	c := u.DeepCopy()
	c.SetManagedFields(nil)
	md, _ := c.Object["metadata"].(map[string]any)
	if md != nil {
		for _, k := range []string{"resourceVersion", "uid", "creationTimestamp", "generation", "selfLink", "managedFields"} {
			delete(md, k)
		}
		if o.DropNamespace {
			delete(md, "namespace")
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			for _, k := range ControlAnnotations {
				delete(ann, k)
			}
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
		if l, ok := md["labels"].(map[string]any); ok && len(l) == 0 {
			delete(md, "labels")
		}
		if refs, ok := md["ownerReferences"].([]any); ok {
			for _, r := range refs {
				if m, ok := r.(map[string]any); ok {
					delete(m, "uid")
				}
			}
		}
	}
	delete(c.Object, "status")
	return c
}

// Op is one JSON Patch operation. Old is not part of RFC 6902: it carries the previous value of a
// replace or remove, so a reader can show "from → to" without the full object.
type Op struct {
	Op    string `json:"op"` // add | remove | replace
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
	Old   any    `json:"old,omitempty"`
}

// Diff returns the operations that turn a into b. Maps are compared key by key (sorted, so the
// output is stable); arrays element by element, with trailing elements added or removed (removals
// from the highest index first, so the patch applies in order).
func Diff(a, b any) []Op {
	var out []Op
	diff("", a, b, &out)
	return out
}

func diff(path string, a, b any, out *[]Op) {
	if reflect.DeepEqual(a, b) {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			break
		}
		keys := make([]string, 0, len(av)+len(bv))
		for k := range av {
			keys = append(keys, k)
		}
		for k := range bv {
			if _, ok := av[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := path + "/" + Escape(k)
			x, inA := av[k]
			y, inB := bv[k]
			switch {
			case inA && !inB:
				*out = append(*out, Op{Op: "remove", Path: p, Old: x})
			case !inA && inB:
				*out = append(*out, Op{Op: "add", Path: p, Value: y})
			default:
				diff(p, x, y, out)
			}
		}
		return
	case []any:
		bv, ok := b.([]any)
		if !ok {
			break
		}
		n := min(len(av), len(bv))
		for i := 0; i < n; i++ {
			diff(path+"/"+strconv.Itoa(i), av[i], bv[i], out)
		}
		for i := n; i < len(bv); i++ {
			*out = append(*out, Op{Op: "add", Path: path + "/" + strconv.Itoa(i), Value: bv[i]})
		}
		for i := len(av) - 1; i >= n; i-- {
			*out = append(*out, Op{Op: "remove", Path: path + "/" + strconv.Itoa(i), Old: av[i]})
		}
		return
	}
	*out = append(*out, Op{Op: "replace", Path: path, Value: b, Old: a})
}

// Escape encodes one JSON Pointer reference token (RFC 6901).
func Escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func unescape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}

// Lookup returns the value at a JSON Pointer inside doc.
func Lookup(doc any, pointer string) (any, bool) {
	if pointer == "" {
		return doc, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	cur := doc
	for _, tok := range strings.Split(pointer[1:], "/") {
		tok = unescape(tok)
		switch v := cur.(type) {
		case map[string]any:
			x, ok := v[tok]
			if !ok {
				return nil, false
			}
			cur = x
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}
