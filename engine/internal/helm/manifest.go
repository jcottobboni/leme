package helm

import (
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/ops"
	masking "github.com/jcottobboni/leme/engine/internal/redact"
)

var docSep = regexp.MustCompile(`(?m)^---[ \t]*$`)

// maskObject masks a Secret, or Secrets inside a *List; it reports whether anything changed.
func maskObject(m map[string]any) bool {
	kind, _ := m["kind"].(string)
	changed := false
	if kind == "Secret" {
		ops.MaskSecret(&unstructured.Unstructured{Object: m})
		changed = true
	}
	if items, ok := m["items"].([]any); ok && strings.HasSuffix(kind, "List") {
		for _, it := range items {
			if im, ok := it.(map[string]any); ok && maskObject(im) {
				changed = true
			}
		}
	}
	return changed
}

// MaskManifest hides Secret values in a rendered multi-document manifest (YAML or JSON documents,
// including Secrets inside List kinds). Documents without Secrets are returned byte-for-byte,
// including Helm's "# Source:" comments, so diffs stay readable.
func MaskManifest(manifest string) string {
	docs := docSep.Split(manifest, -1)
	for i, d := range docs {
		if strings.TrimSpace(d) == "" {
			continue
		}
		var comments []string
		for _, line := range strings.Split(strings.TrimLeft(d, "\n"), "\n") {
			if !strings.HasPrefix(line, "#") {
				break
			}
			comments = append(comments, line)
		}
		head := ""
		if len(comments) > 0 {
			head = strings.Join(comments, "\n") + "\n"
		}
		m := map[string]any{}
		if err := yaml.Unmarshal([]byte(d), &m); err != nil {
			if !strings.Contains(d, "Secret") {
				continue
			}
			// Unparseable document that mentions Secret: hide it entirely rather than risk a leak.
			docs[i] = "\n" + head + "# " + masking.Mask + " (document could not be parsed)\n"
			continue
		}
		if !maskObject(m) {
			continue
		}
		b, _ := yaml.Marshal(m)
		docs[i] = "\n" + head + string(b)
	}
	return strings.Join(docs, "---")
}

// objects lists the resources rendered in a manifest, defaulting namespaced ones to the release namespace.
func objects(manifest, releaseNS string, sess *cluster.Session) []Object {
	var out []Object
	for _, d := range docSep.Split(manifest, -1) {
		if strings.TrimSpace(d) == "" {
			continue
		}
		var o struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if yaml.Unmarshal([]byte(d), &o) != nil || o.Kind == "" || o.Metadata.Name == "" {
			continue
		}
		obj := Object{APIVersion: o.APIVersion, Kind: o.Kind, Name: o.Metadata.Name, Namespace: o.Metadata.Namespace}
		if obj.Namespace == "" && sess != nil {
			if gv, err := schema.ParseGroupVersion(o.APIVersion); err == nil {
				if r, ok := sess.LookupKind(gv.Group, o.Kind); ok && r.Namespaced {
					obj.Namespace = releaseNS
				}
			}
		}
		out = append(out, obj)
	}
	return out
}
