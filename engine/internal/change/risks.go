package change

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/jcottobboni/leme/engine/internal/redact"
	"github.com/jcottobboni/leme/engine/internal/security"
)

// errMasked refuses patches that would write a masked placeholder over a real value: the model and
// agents only ever see redact.Mask, so sending it back would replace a password with that text.
var errMasked = errors.New(`the patch contains "` + redact.Mask + `": masked values are placeholders, not the real ones; ` +
	"leave those fields out of the patch (for containers, do not resend env or the fields you did not change)")

func containsMask(v any) bool {
	switch t := v.(type) {
	case string:
		return redact.IsMask(t)
	case map[string]any:
		for _, x := range t {
			if containsMask(x) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if containsMask(x) {
				return true
			}
		}
	}
	return false
}

// risks lists what the change adds that widens privileges or exposure, so the person approving
// sees it next to the diff (MCP agents cannot apply such changes at all).
func risks(cur, next *unstructured.Unstructured) []string {
	var out []string
	if a, ok := security.PodSpec(next); ok {
		b, _ := security.PodSpec(cur)
		had := map[string]bool{}
		for _, f := range security.CheckPodSpec(security.Finding{}, b, false) {
			had[f.ID+"/"+f.Container] = true
		}
		for _, f := range security.CheckPodSpec(security.Finding{}, a, false) {
			if (f.Severity == security.Critical || f.Severity == security.High) && !had[f.ID+"/"+f.Container] {
				out = append(out, f.Title+containerSuffix(f.Container))
			}
		}
		if str(b, "serviceAccountName") != str(a, "serviceAccountName") {
			out = append(out, fmt.Sprintf("Troca a ServiceAccount (%q → %q)", str(b, "serviceAccountName"), str(a, "serviceAccountName")))
		}
		if !reflect.DeepEqual(b["automountServiceAccountToken"], a["automountServiceAccountToken"]) && a["automountServiceAccountToken"] != false {
			out = append(out, "Passa a montar o token da ServiceAccount")
		}
		for s := range diffSet(secretRefs(b), secretRefs(a)) {
			out = append(out, "Passa a ler o Secret "+s)
		}
	}
	if cur.GetKind() == "Service" {
		for _, f := range []string{"externalIPs", "type", "externalName", "loadBalancerSourceRanges"} {
			b, _, _ := unstructured.NestedFieldNoCopy(cur.Object, "spec", f)
			a, _, _ := unstructured.NestedFieldNoCopy(next.Object, "spec", f)
			if !reflect.DeepEqual(a, b) {
				out = append(out, "Muda a exposição do Service (spec."+f+")")
			}
		}
	}
	if hiddenChange(cur, next) {
		out = append(out, "Altera valores ocultos (env, anotações longas ou campos de credencial): o diff mascarado não mostra o quê")
	}
	sort.Strings(out)
	return out
}

func containerSuffix(c string) string {
	if c == "" {
		return ""
	}
	return " (container " + c + ")"
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// hiddenChange is true when the objects differ but their redacted renderings do not.
func hiddenChange(cur, next *unstructured.Unstructured) bool {
	return render(redact.Object(clean(cur))) == render(redact.Object(clean(next))) && render(clean(cur)) != render(clean(next))
}

// secretRefs collects every Secret a pod spec reads (volumes, env, envFrom, projected sources).
func secretRefs(spec map[string]any) map[string]bool {
	out := map[string]bool{}
	var walk func(k string, v any)
	walk = func(k string, v any) {
		switch t := v.(type) {
		case map[string]any:
			switch k {
			case "secret":
				if n, ok := t["secretName"].(string); ok {
					out[n] = true
				}
				if n, ok := t["name"].(string); ok { // projected volume source
					out[n] = true
				}
			case "secretRef", "secretKeyRef":
				if n, ok := t["name"].(string); ok {
					out[n] = true
				}
			}
			for kk, x := range t {
				walk(kk, x)
			}
		case []any:
			for _, x := range t {
				walk(k, x)
			}
		}
	}
	walk("", spec)
	return out
}

func diffSet(before, after map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range after {
		if !before[k] {
			out[k] = true
		}
	}
	return out
}
