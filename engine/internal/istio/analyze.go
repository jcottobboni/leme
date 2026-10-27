package istio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/security"
)

// InstallHint is shown when istioctl is missing.
const InstallHint = "Instale o istioctl a partir das releases oficiais do Istio (https://istio.io/latest/docs/setup/getting-started/#download) e deixe-o no PATH ou em ~/.local/bin. O Leme só o usa offline: nada é aplicado no cluster."

// AnalyzeTimeout bounds one istioctl run.
var AnalyzeTimeout = 2 * time.Minute

// Message is one finding of `istioctl analyze`.
type Message struct {
	Code     string `json:"code"`
	Level    string `json:"level"`
	Message  string `json:"message"`
	Resource string `json:"resource"`
	DocURL   string `json:"documentationUrl,omitempty"`
}

// exportCore are the non-Istio objects the analyzers need. Secrets and ConfigMaps are never
// exported (see forbiddenKinds).
var exportCore = []string{"/namespaces", "/services", "/pods", "apps/deployments", "apps/statefulsets", "apps/daemonsets"}

// forbiddenKinds are never written to disk, whatever the caller passes.
var forbiddenKinds = map[string]bool{"Secret": true, "ConfigMap": true}

// ExportInput lists what `istioctl analyze` reads: every resource of the *.istio.io groups plus
// Namespaces, Services and workloads. ns "" exports the whole cluster.
func ExportInput(ctx context.Context, sess interface {
	Lookup
	Resources() []cluster.Resource
}, list Lister, ns string) ([]*unstructured.Unstructured, map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var res []cluster.Resource
	for _, r := range sess.Resources() {
		if strings.HasSuffix(r.Group, ".istio.io") {
			res = append(res, r)
		}
	}
	for _, key := range exportCore {
		g, rs, _ := strings.Cut(key, "/")
		if r, ok := sess.Lookup(g, rs); ok {
			res = append(res, r)
		}
	}
	errs := map[string]string{}
	var out []*unstructured.Unstructured
	for _, r := range res {
		if forbiddenKinds[r.Kind] {
			continue
		}
		n := ns
		if !r.Namespaced {
			n = ""
		}
		objs, err := list(ctx, r, n)
		if err != nil {
			errs[r.Group+"/"+r.Resource] = err.Error()
			continue
		}
		for _, o := range objs {
			if r.Kind == "Namespace" && ns != "" && o.GetName() != ns && o.GetName() != DefaultRootNamespace {
				continue
			}
			out = append(out, o)
		}
	}
	return out, errs
}

// Export writes one sanitized YAML file per object into dir: no Secrets or ConfigMaps, no status,
// managed fields or last-applied annotation, and literal env values masked.
func Export(objs []*unstructured.Unstructured, dir string) (int, error) {
	n := 0
	for _, u := range objs {
		if forbiddenKinds[u.GetKind()] {
			continue
		}
		c := u.DeepCopy()
		unstructured.RemoveNestedField(c.Object, "status")
		unstructured.RemoveNestedField(c.Object, "metadata", "managedFields")
		if ann := c.GetAnnotations(); ann != nil {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			c.SetAnnotations(ann)
		}
		maskEnv(c.Object)
		b, err := yaml.Marshal(c.Object)
		if err != nil {
			continue
		}
		n++
		name := fmt.Sprintf("%04d-%s-%s-%s.yaml", n, safe(c.GetKind()), safe(c.GetNamespace()), safe(c.GetName()))
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return n, err
		}
	}
	return n, nil
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, strings.ToLower(s))
}

// maskEnv replaces literal env values (they sometimes hold passwords); names stay.
func maskEnv(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if k == "env" {
				if list, ok := x.([]any); ok {
					for _, e := range list {
						if m, ok := e.(map[string]any); ok {
							if _, has := m["value"]; has {
								m["value"] = "<redacted>"
							}
						}
					}
				}
				continue
			}
			maskEnv(x)
		}
	case []any:
		for _, x := range t {
			maskEnv(x)
		}
	}
}

// Analyze exports objs to a private temp dir, runs `istioctl analyze --use-kube=false -o json` on it
// with an isolated environment (no kubeconfig, private HOME) and a timeout, then removes the dir.
func Analyze(ctx context.Context, bin string, objs []*unstructured.Unstructured, ns string) ([]Message, error) {
	// The leme-scan- prefix lets security.CleanStaleExports remove it after a crash.
	root, err := os.MkdirTemp("", "leme-scan-istio-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	in, home := filepath.Join(root, "in"), filepath.Join(root, "home")
	for _, d := range []string{in, home} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return nil, err
		}
	}
	n, err := Export(objs, in)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errors.New("nada para analisar neste escopo")
	}
	ctx, cancel := context.WithTimeout(ctx, AnalyzeTimeout)
	defer cancel()
	args := []string{"analyze", "--use-kube=false", "-o", "json"}
	if ns != "" {
		args = append(args, "--namespace", ns)
	} else {
		args = append(args, "--all-namespaces")
	}
	args = append(args, in)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = security.IsolatedEnv(home)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("istioctl analyze excedeu %s", AnalyzeTimeout)
	}
	msgs, perr := ParseAnalyze(stdout.Bytes())
	if perr == nil {
		// istioctl exits non-zero when it finds errors; the JSON is still the answer.
		return msgs, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("istioctl analyze: %v: %s", runErr, tail(stderr.String(), 400))
	}
	return nil, perr
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// ParseAnalyze reads `istioctl analyze -o json`: an array of {code, level, message, origin,
// reference, documentationUrl}. Empty output means no findings.
func ParseAnalyze(b []byte) ([]Message, error) {
	b = bytes.TrimSpace(b)
	out := []Message{}
	if len(b) == 0 {
		return out, nil
	}
	// Some versions print a line of text before the JSON.
	if i := bytes.IndexAny(b, "[{"); i > 0 {
		b = b[i:]
	}
	type raw struct {
		Code      string `json:"code"`
		Level     string `json:"level"`
		Message   string `json:"message"`
		Origin    string `json:"origin"`
		Reference string `json:"reference"`
		DocURL    string `json:"documentationUrl"`
	}
	var list []raw
	if err := json.Unmarshal(b, &list); err != nil {
		var wrapped struct {
			Messages []raw `json:"messages"`
		}
		if err2 := json.Unmarshal(b, &wrapped); err2 != nil {
			return nil, fmt.Errorf("saída inesperada do istioctl: %w", err)
		}
		list = wrapped.Messages
	}
	for _, m := range list {
		res := m.Origin
		if res == "" {
			res = m.Reference
		}
		out = append(out, Message{Code: m.Code, Level: m.Level, Message: m.Message, Resource: res, DocURL: m.DocURL})
	}
	return out, nil
}
