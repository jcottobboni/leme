package plugins

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Input is what the UI knows about the object an action or link runs on.
type Input struct {
	Group     string            `json:"group"`
	Version   string            `json:"version"`
	Resource  string            `json:"resource"`
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name"`
	Container string            `json:"container,omitempty"`
	Filter    string            `json:"filter,omitempty"`
	Cols      map[string]string `json:"cols,omitempty"` // column label -> displayed value
}

// Vars builds the k9s-compatible variables of an action. trusted names the variables whose value
// the engine generated itself (KUBECONFIG).
func Vars(in Input, contextName, cluster, user, kubeconfig string) map[string]string {
	v := map[string]string{
		"NAMESPACE":        in.Namespace,
		"NAME":             in.Name,
		"CONTEXT":          contextName,
		"CLUSTER":          cluster,
		"USER":             user,
		"GROUPS":           "",
		"KUBECONFIG":       kubeconfig,
		"FILTER":           in.Filter,
		"RESOURCE_GROUP":   in.Group,
		"RESOURCE_VERSION": in.Version,
		"RESOURCE_NAME":    in.Resource,
		"POD":              "",
		"CONTAINER":        in.Container,
	}
	if in.Resource == "pods" && in.Group == "" {
		v["POD"] = in.Name
	}
	for k, val := range in.Cols {
		v["COL-"+colKey(k)] = val
	}
	return v
}

// colKey turns a column label into the k9s variable suffix: upper case, spaces as underscores.
func colKey(label string) string {
	return strings.ToUpper(strings.Join(strings.Fields(label), "_"))
}

var varRe = regexp.MustCompile(`\$\{(COL-[A-Z0-9_]+|[A-Z][A-Z0-9_]*)\}|\$(COL-[A-Z0-9_]+|[A-Z][A-Z0-9_]*)`)

// safeValue is what may be interpolated into a shell script without quoting.
var safeValue = regexp.MustCompile(`^[A-Za-z0-9._:/@=+,~%-]*$`)

// shells run their arguments as scripts: interpolated values could inject commands.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "ash": true, "busybox": true,
	"cmd": true, "powershell": true, "pwsh": true}

// wrappers hand (part of) their arguments to a shell or run them as another command line.
var wrappers = map[string]bool{"env": true, "watch": true, "xargs": true, "sudo": true, "nohup": true, "script": true, "ssh": true, "su": true, "timeout": true, "nice": true}

func base(command string) string {
	b := strings.ToLower(filepath.Base(filepath.ToSlash(command)))
	return strings.TrimSuffix(b, ".exe")
}

// IsShell reports whether a command interprets its arguments as a script.
func IsShell(command string) bool { return shells[base(command)] }

// Strict reports whether some argument of the command line may be parsed by a shell, locally or
// in a container (kubectl exec … -- sh -c "…"): interpolated values must then be plain.
func Strict(command string, args []string) bool {
	if IsShell(command) || wrappers[base(command)] {
		return true
	}
	for _, a := range args {
		if IsShell(a) {
			return true
		}
	}
	return false
}

// Expand replaces the known variables in s; unknown ones stay for the shell or the program. With
// strict, values that are not plain identifiers are refused (they would be parsed by a shell).
func Expand(s string, vars map[string]string, strict bool, exempt map[string]bool) (string, error) {
	var bad error
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := varRe.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		val, ok := vars[name]
		if !ok {
			// k9s also accepts $COL-<label> spelled with the label's own case; only upper case is known here.
			return m
		}
		if strict && !exempt[name] && !safeValue.MatchString(val) && bad == nil {
			bad = fmt.Errorf("o valor de $%s (%q) tem caracteres que um shell interpretaria: a ação não foi executada", name, val)
		}
		return val
	})
	return out, bad
}

// EnvName maps a variable to an environment variable name ($COL-READY -> COL_READY).
func EnvName(v string) string { return strings.ReplaceAll(v, "-", "_") }
