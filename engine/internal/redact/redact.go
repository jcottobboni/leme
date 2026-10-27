// Package redact prepares cluster data that leaves the machine (to an AI provider or an MCP agent):
// it drops bookkeeping noise and masks values that commonly hold credentials. It is defensive by
// design: when in doubt a value is masked, and Secret values never survive.
package redact

import (
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Mask is the only placeholder the engine writes for a value that is not shown: Secret values
// before an explicit reveal, and values that must not leave the machine (AI providers, MCP agents,
// external scanners). Anything that writes back to the cluster refuses it (see IsMask).
const Mask = "<hidden>"

// legacyMask is what versions before 0.6 sent to AI providers and agents; still refused on write.
const legacyMask = "<redacted>"

// IsMask reports whether v is a masked placeholder rather than a real value.
func IsMask(v string) bool { return v == Mask || v == legacyMask }

const maxAnnotation = 200

// Object returns a sanitized deep copy of u:
//   - no managedFields, no last-applied-configuration;
//   - Secret: only the keys of data/stringData, and every annotation value masked (tools such as
//     kapp embed the whole original manifest in an annotation);
//   - ConfigMap: only the keys of data/binaryData, annotations masked the same way;
//   - env[].value masked everywhere (env vars often carry passwords);
//   - every string value whose key looks like a credential (password, secret, token, apiKey,
//     privateKey...) masked, at any depth (e.g. OAuthClient.secret);
//   - every remaining string passed through Text (PEM private keys such as Route spec.tls.key,
//     "--db-password=x" arguments, bearer/JWT/cloud tokens, credentials in URLs);
//   - annotations longer than 200 characters truncated; annotations holding an embedded manifest dropped.
func Object(u *unstructured.Unstructured) *unstructured.Unstructured {
	c := u.DeepCopy()
	c.SetManagedFields(nil)
	sensitiveKind := c.GetKind() == "Secret" || c.GetKind() == "ConfigMap"
	if ann := c.GetAnnotations(); ann != nil {
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		for k, v := range ann {
			switch {
			case sensitiveKind && !safeAnnotations[k], secretKey.MatchString(k), embedsManifest(v):
				ann[k] = Mask
			case len(v) > maxAnnotation:
				ann[k] = Text(v[:maxAnnotation]) + "…"
			default:
				ann[k] = Text(v)
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
	case "Template": // template.openshift.io: parameter values are often generated passwords
		if ps, ok := c.Object["parameters"].([]any); ok {
			for _, p := range ps {
				if m, ok := p.(map[string]any); ok {
					if _, has := m["value"]; has {
						m["value"] = Mask
					}
				}
			}
		}
	}
	env(c.Object)
	leaves(c.Object)
	return c
}

// Annotations of Secrets/ConfigMaps that carry no secret material and help diagnosis.
var safeAnnotations = map[string]bool{
	"kubernetes.io/service-account.name": true, "kubernetes.io/service-account.uid": true,
	"kubernetes.io/description": true, "openshift.io/description": true,
}

func embedsManifest(v string) bool {
	t := strings.TrimSpace(v)
	return (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "apiVersion:")) && (strings.Contains(t, `"data"`) || strings.Contains(t, `"stringData"`) || strings.Contains(t, "\ndata:") || strings.Contains(t, "stringData:"))
}

// keyRefs are credential-looking keys whose values are references or settings, not secrets.
var keyRefs = regexp.MustCompile(`(?i)(name|namespace|ref|path|file|mode|seconds|expiration|audience|type|policy|items)$`)

// leaves masks credential-named string values and runs Text over every other string.
func leaves(o map[string]any) {
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				if s, ok := x.(string); ok {
					if s != Mask && secretKey.MatchString(k) && !keyRefs.MatchString(k) {
						t[k] = Mask
					} else {
						t[k] = Text(s)
					}
					continue
				}
				t[k] = walk(x)
			}
		case []any:
			for i, x := range t {
				t[i] = walk(x)
			}
		case string:
			return Text(t)
		}
		return v
	}
	meta, hasMeta := o["metadata"]
	delete(o, "metadata") // names, labels and annotations are handled above; Text on uids is noise
	walk(o)
	if hasMeta {
		o["metadata"] = meta
	}
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
		regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`),                                // AWS access key id
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
