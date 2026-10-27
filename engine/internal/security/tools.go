package security

// LookTool finds an optional CLI in PATH or the usual install dirs (also used by the M5 istio
// feature for istioctl).
func LookTool(name string) (string, error) { return lookPath(name) }

// IsolatedEnv is the environment external scanners run with: no kubeconfig, a private HOME and
// only proxies passed through. Callers create and remove home.
func IsolatedEnv(home string) []string { return isolatedEnv(home) }
