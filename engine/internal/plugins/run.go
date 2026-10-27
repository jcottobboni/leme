package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/rest"
)

// Limits of the output and background modes.
const (
	MaxOutput  = 1 << 20
	RunTimeout = 10 * time.Minute
)

// Target is the cluster side of a run.
type Target struct {
	Config    *rest.Config
	Context   string
	Cluster   string
	User      string
	Namespace string // default namespace of the plugin's kubeconfig
	Policy    Policy
}

// Prepared is a command ready to start, with the proxy and the temporary kubeconfig it uses.
type Prepared struct {
	Cmd     *exec.Cmd
	proxy   *Proxy
	dir     string
	cleaned sync.Once
}

// Cleanup stops the proxy and removes the temporary kubeconfig.
func (p *Prepared) Cleanup() {
	p.cleaned.Do(func() {
		if p.proxy != nil {
			p.proxy.Close()
		}
		if p.dir != "" {
			_ = os.RemoveAll(p.dir)
		}
	})
}

// Prepare builds the command of an action for one object. Nothing runs yet.
func Prepare(ctx context.Context, a Action, in Input, t Target) (*Prepared, error) {
	path, err := exec.LookPath(a.Command)
	if err != nil {
		return nil, fmt.Errorf("comando %q não encontrado no PATH", a.Command)
	}
	dir, err := os.MkdirTemp("", "leme-plugin-")
	if err != nil {
		return nil, err
	}
	p := &Prepared{dir: dir}
	ok := false
	defer func() {
		if !ok {
			p.Cleanup()
		}
	}()
	if p.proxy, err = StartProxy(t.Config, t.Policy); err != nil {
		return nil, fmt.Errorf("proxy do plugin: %w", err)
	}
	ns := in.Namespace
	if ns == "" {
		ns = t.Namespace
	}
	kc, err := WriteKubeconfig(dir, p.proxy, t.Context, t.Cluster, t.User, ns)
	if err != nil {
		return nil, err
	}
	vars := Vars(in, t.Context, t.Cluster, t.User, kc)
	strict := Strict(a.Command, a.Args)
	exempt := map[string]bool{"KUBECONFIG": true}
	args := make([]string, 0, len(a.Args))
	for _, raw := range a.Args {
		v, err := Expand(raw, vars, strict, exempt)
		if err != nil {
			return nil, err
		}
		// A value must not turn into an option of the command (e.g. a column holding "--server=…").
		if !strings.HasPrefix(raw, "-") && strings.HasPrefix(v, "-") {
			return nil, fmt.Errorf("o argumento %q virou uma opção (%q) depois de preencher as variáveis: a ação não foi executada", raw, v)
		}
		args = append(args, v)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = childEnv(vars)
	if h, err := os.UserHomeDir(); err == nil {
		cmd.Dir = h
	}
	p.Cmd = cmd
	ok = true
	return p, nil
}

// childEnv is the user's environment without kubeconfig overrides, plus the plugin variables.
func childEnv(vars map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(vars))
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, mine := vars[k]; mine || strings.EqualFold(k, "KUBECONFIG") || strings.HasPrefix(k, "COL_") {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range vars {
		out = append(out, EnvName(k)+"="+v)
	}
	return out
}

// Result of a run in the output or background mode.
type Result struct {
	ExitCode  int    `json:"exitCode"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
	Duration  int64  `json:"durationMs"`
}

// capped keeps the first MaxOutput bytes.
type capped struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := MaxOutput - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(b), nil
	}
	if len(b) > room {
		c.buf.Write(b[:room])
		c.truncated = true
		return len(b), nil
	}
	return c.buf.Write(b)
}

// Run runs a prepared command to completion (output and background modes).
func Run(p *Prepared) Result {
	defer p.Cleanup()
	out := &capped{}
	cmd := p.Cmd
	cmd.Stdout, cmd.Stderr = out, out
	killGroup(cmd)
	t0 := time.Now()
	err := cmd.Run()
	res := Result{Output: out.buf.String(), Truncated: out.truncated, Duration: time.Since(t0).Milliseconds()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode < 0 {
			res.Error = "processo encerrado: " + ee.String()
		}
	default:
		res.ExitCode = -1
		res.Error = err.Error()
	}
	return res
}
