package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// Tool is an external scanner and whether it is installed.
type Tool struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Purpose   string `json:"purpose"`
}

// Tools lists the optional CLIs (looked up in PATH and ~/.local/bin).
func Tools() []Tool {
	out := []Tool{
		{Name: "polaris", Purpose: "boas práticas de configuração (Fairwinds)"},
		{Name: "kubescape", Purpose: "frameworks NSA e MITRE ATT&CK"},
		{Name: "trivy", Purpose: "vulnerabilidades das imagens"},
	}
	for i := range out {
		if p, err := lookPath(out[i].Name); err == nil {
			out[i].Installed, out[i].Path = true, p
		}
	}
	return out
}

func lookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	// Desktop launchers often start without the user's shell PATH.
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{filepath.Join(home, ".local", "bin"), "/usr/local/bin", "/opt/homebrew/bin"} {
			p := filepath.Join(dir, name)
			if runtime.GOOS == "windows" {
				p += ".exe"
			}
			if st, err := os.Stat(p); err == nil && !st.IsDir() && (runtime.GOOS == "windows" || st.Mode()&0o111 != 0) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%s não está instalado", name)
}

// exportKinds are written for file-based scanners. Secrets and ConfigMaps are never exported.
var exportKinds = append(append([]string{}, workloadKeys...), "/services", "networking.k8s.io/ingresses", "networking.k8s.io/networkpolicies",
	"/serviceaccounts", "rbac.authorization.k8s.io/roles", "rbac.authorization.k8s.io/rolebindings")

// Export writes one YAML file per object into a new private temp dir. The caller removes it.
func Export(in Input) (string, error) {
	dir, err := os.MkdirTemp("", "leme-scan-")
	if err != nil {
		return "", err
	}
	n := 0
	for _, key := range exportKinds {
		for _, u := range in.Objects[key] {
			// Owned objects (ReplicaSets, Jobs of CronJobs, pods) duplicate their controller's findings.
			if controlled(u) {
				continue
			}
			c := u.DeepCopy()
			unstructured.RemoveNestedField(c.Object, "status")
			redactEnv(c.Object)
			unstructured.RemoveNestedField(c.Object, "metadata", "managedFields")
			ann := c.GetAnnotations()
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			c.SetAnnotations(ann)
			b, err := yaml.Marshal(c.Object)
			if err != nil {
				continue
			}
			n++
			name := fmt.Sprintf("%04d-%s-%s-%s.yaml", n, strings.ToLower(c.GetKind()), c.GetNamespace(), c.GetName())
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
				os.RemoveAll(dir)
				return "", err
			}
		}
	}
	if n == 0 {
		os.RemoveAll(dir)
		return "", errors.New("nada para analisar neste escopo")
	}
	return dir, nil
}

// baseEnv is the minimal environment every scanner starts with: a fixed system PATH and locale on
// Unix; on Windows, the variables programs need to start at all (system dirs, temp, PATH).
func baseEnv() []string {
	switch runtime.GOOS {
	case "windows":
		var env []string
		for _, k := range []string{"SystemRoot", "SystemDrive", "windir", "TEMP", "TMP", "PATH", "PATHEXT", "ProgramData"} {
			if v, ok := os.LookupEnv(k); ok {
				env = append(env, k+"="+v)
			}
		}
		return env
	case "darwin":
		return []string{"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	default:
		return []string{"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8"}
	}
}

// isolatedEnv keeps scanners from reading the user's kubeconfig or reporting anywhere. Proxies pass
// through: Kubescape downloads its rules on the first run, often behind a corporate proxy.
func isolatedEnv(home string) []string {
	env := append(baseEnv(), "HOME="+home, "KUBECONFIG="+os.DevNull, "KS_SKIP_UPDATE_CHECK=true")
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home) // the home directory Go programs see on Windows
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// redactEnv replaces literal env values in pod templates: they sometimes hold passwords, and the
// exported files are read by external tools. Names stay so credential-in-env checks still work.
func redactEnv(o map[string]any) {
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
									m["value"] = "<redacted>"
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

// CleanStaleExports removes export dirs left behind by a crashed engine.
func CleanStaleExports() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), "leme-scan-*"))
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && time.Since(st.ModTime()) > time.Hour {
			_ = os.RemoveAll(d)
		}
	}
}

// RunPolaris audits exported manifests (no cluster access).
func RunPolaris(ctx context.Context, dir, home string) ([]Finding, error) {
	bin, err := lookPath("polaris")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "audit", "--audit-path", dir, "--format", "json")
	cmd.Env = isolatedEnv(home)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("polaris: %w%s", err, stderrOf(err))
	}
	return ParsePolaris(out)
}

func stderrOf(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		s := strings.TrimSpace(string(ee.Stderr))
		if len(s) > 300 {
			s = s[:300]
		}
		return ": " + s
	}
	return ""
}

type polarisCheck struct {
	ID       string
	Message  string
	Success  bool
	Severity string
	Category string
}

// ParsePolaris converts `polaris audit --format json` output.
func ParsePolaris(b []byte) ([]Finding, error) {
	var doc struct {
		Results []struct {
			Name, Namespace, Kind string
			Results               map[string]polarisCheck
			PodResult             *struct {
				Results          map[string]polarisCheck
				ContainerResults []struct {
					Name    string
					Results map[string]polarisCheck
				}
			}
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("saída inesperada do polaris: %w", err)
	}
	out := []Finding{}
	add := func(kind, ns, name, container string, c polarisCheck) {
		if c.Success || c.Severity == "ignore" {
			return
		}
		sev := Medium
		if c.Severity == "danger" {
			sev = High
		}
		if c.Category == "Efficiency" || c.Category == "Reliability" {
			sev = map[string]string{High: Medium, Medium: Low}[sev]
		}
		out = append(out, Finding{ID: "polaris/" + c.ID, Severity: sev, Category: c.Category, Title: c.Message, Kind: kind, Namespace: ns, Name: name, Container: container, Source: "polaris"})
	}
	for _, r := range doc.Results {
		for _, c := range r.Results {
			add(r.Kind, r.Namespace, r.Name, "", c)
		}
		if r.PodResult != nil {
			for _, c := range r.PodResult.Results {
				add(r.Kind, r.Namespace, r.Name, "", c)
			}
			for _, cr := range r.PodResult.ContainerResults {
				for _, c := range cr.Results {
					add(r.Kind, r.Namespace, r.Name, cr.Name, c)
				}
			}
		}
	}
	return out, nil
}

// Frameworks offered for Kubescape.
var Frameworks = map[string]bool{"nsa": true, "mitre": true}

// RunKubescape scans exported manifests with a framework, keeping results local.
func RunKubescape(ctx context.Context, dir, home, framework string) ([]Finding, error) {
	if !Frameworks[framework] {
		return nil, errors.New("framework deve ser nsa ou mitre")
	}
	bin, err := lookPath("kubescape")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	report := filepath.Join(dir, ".kubescape-report.json")
	cmd := exec.CommandContext(ctx, bin, "scan", "framework", framework, dir, "--keep-local", "--format", "json", "--output", report)
	cmd.Env = isolatedEnv(home)
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(report); statErr != nil {
			msg := strings.TrimSpace(string(out))
			if len(msg) > 400 {
				msg = msg[len(msg)-400:]
			}
			return nil, fmt.Errorf("kubescape: %v: %s (na primeira execução o kubescape precisa de internet para baixar as regras)", err, msg)
		}
	}
	b, err := os.ReadFile(report)
	if err != nil {
		return nil, err
	}
	return ParseKubescape(b, framework)
}

// ParseKubescape converts the JSON report; one finding per failed control and resource.
func ParseKubescape(b []byte, framework string) ([]Finding, error) {
	var doc struct {
		SummaryDetails struct {
			Controls map[string]struct {
				Name     string `json:"name"`
				Severity string `json:"severity"`
				Category struct {
					Name string `json:"name"`
				} `json:"category"`
			} `json:"controls"`
		} `json:"summaryDetails"`
		Results []struct {
			ResourceID string `json:"resourceID"`
			Controls   []struct {
				ControlID string `json:"controlID"`
				Name      string `json:"name"`
				Severity  string `json:"severity"`
				Status    struct {
					Status string `json:"status"`
				} `json:"status"`
				Rules []struct {
					Paths []struct {
						FixPath struct {
							Path  string `json:"path"`
							Value string `json:"value"`
						} `json:"fixPath"`
						FailedPath string `json:"failedPath"`
					} `json:"paths"`
				} `json:"rules"`
			} `json:"controls"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("saída inesperada do kubescape: %w", err)
	}
	out := []Finding{}
	for _, r := range doc.Results {
		group, kind, ns, name := parseResourceID(r.ResourceID)
		for _, c := range r.Controls {
			if c.Status.Status != "failed" {
				continue
			}
			meta := doc.SummaryDetails.Controls[c.ControlID]
			sevName := c.Severity
			if sevName == "" {
				sevName = meta.Severity
			}
			var fixes []string
			for _, rule := range c.Rules {
				for _, p := range rule.Paths {
					if p.FixPath.Path != "" {
						fixes = append(fixes, p.FixPath.Path+" = "+p.FixPath.Value)
					} else if p.FailedPath != "" {
						fixes = append(fixes, "revise "+p.FailedPath)
					}
				}
			}
			out = append(out, Finding{
				ID: "kubescape/" + c.ControlID, Severity: normalizeSeverity(sevName), Category: meta.Category.Name,
				Title: c.Name, Detail: fmt.Sprintf("Controle %s do framework %s (Kubescape).", c.ControlID, strings.ToUpper(framework)),
				Remediation: strings.Join(fixes, "; "), Group: group, Kind: kind, Namespace: ns, Name: name, Source: "kubescape",
			})
		}
	}
	return out, nil
}

// parseResourceID reads "path=…/api=<group>/<version>/<namespace>/<Kind>/<name>" (group absent for core).
func parseResourceID(id string) (group, kind, ns, name string) {
	i := strings.Index(id, "api=")
	if i < 0 {
		return "", "", "", id
	}
	parts := strings.Split(id[i+4:], "/")
	if len(parts) < 4 {
		return "", "", "", id
	}
	n := len(parts)
	name, kind, ns = parts[n-1], parts[n-2], parts[n-3]
	if n-3 >= 2 {
		group = strings.Join(parts[:n-4], "/")
	}
	return group, kind, ns, name
}

func normalizeSeverity(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return Critical
	case "high":
		return High
	case "medium":
		return Medium
	case "low":
		return Low
	}
	return Info
}
