// Package redact prepares cluster data that leaves the machine (to an AI provider or an MCP agent):
// it drops bookkeeping noise and masks values that commonly hold credentials. It is defensive by
// design: when in doubt a value is masked, and Secret values never survive.
package redact

import (
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Mask replaces a value that must not leave the machine.
const Mask = "<redacted>"

const maxAnnotation = 200

// Object returns a sanitized deep copy of u:
//   - no managedFields, no last-applied-configuration;
//   - Secret: only the keys of data/stringData;
//   - ConfigMap: only the keys of data/binaryData;
//   - env[].value masked everywhere (env vars often carry passwords);
//   - annotations longer than 200 characters truncated, and annotations that look like credentials masked.
func Object(u *unstructured.Unstructured) *unstructured.Unstructured {
	c := u.DeepCopy()
	c.SetManagedFields(nil)
	if ann := c.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		for k, v := range ann {
			switch {
			case secretKey.MatchString(k):
				ann[k] = Mask
			case len(v) > maxAnnotation:
				ann[k] = v[:maxAnnotation] + "…"
			}
		}
		if len(ann) == 0 {
			ann = nil
		}
		c.SetAnnotations(ann)
	}
	switch c.GetKind() {
	case "Secret":
		maskValues(c.Object, "data", "stringData")
	case "ConfigMap":
		maskValues(c.Object, "data", "binaryData")
	}
	env(c.Object)
	return c
}

func maskValues(o map[string]any, fields ...string) {
	for _, f := range fields {
		if m, ok := o[f].(map[string]any); ok {
			for k := range m {
				m[k] = Mask
			}
		}
	}
}

// env masks literal env values in any pod template inside o.
func env(o map[string]any) {
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				if k == "env" {
					if list, ok := x.([]any); ok {
						for _, e := range list {
							if m, ok := e.(map[string]any); ok {
								if _, has := m["value"]; has {
									m["value"] = Mask
								}
							}
						}
					}
					continue
				}
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(o)
}

var (
	secretKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[-_]?key|credential|private[-_]?key)`)
	// Patterns that commonly carry credentials in free text (logs, event messages).
	textPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(authorization:\s*(bearer|basic)\s+)[^\s"']+`),
		regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{12,}`),
		regexp.MustCompile(`(?i)((?:password|passwd|pwd|secret|token|api[-_]?key|access[-_]?key|client[-_]?secret)["']?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&"']+)`),
		regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), // JWT
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),                                   // AWS access key id
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s:/@]+:([^\s@/]+)@`), // credentials in URLs
		regexp.MustCompile(`\bsha256~[A-Za-z0-9_-]{20,}`),                     // OpenShift tokens
	}
)

// Text masks obvious credentials in free text such as logs and event messages.
func Text(s string) string {
	for i, re := range textPatterns {
		switch i {
		case 0, 2:
			s = re.ReplaceAllString(s, "${1}"+Mask)
		case 6:
			s = re.ReplaceAllStringFunc(s, func(m string) string {
				at := strings.LastIndex(m, "@")
				colon := strings.LastIndex(m[:at], ":")
				return m[:colon+1] + Mask + "@"
			})
		default:
			s = re.ReplaceAllString(s, Mask)
		}
	}
	return s
}
