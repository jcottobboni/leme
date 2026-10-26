package ops

import (
	"context"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// LogOptions selects a log stream.
type LogOptions struct {
	Namespace  string
	Pod        string
	Container  string
	Follow     bool
	Previous   bool
	Timestamps bool
	TailLines  int64
}

// StreamLogs copies container logs into w until ctx ends or the stream closes.
func StreamLogs(ctx context.Context, s *cluster.Session, o LogOptions, w io.Writer) error {
	opts := &corev1.PodLogOptions{Container: o.Container, Follow: o.Follow, Previous: o.Previous, Timestamps: o.Timestamps}
	if o.TailLines > 0 {
		opts.TailLines = &o.TailLines
	}
	rc, err := s.Typed.CoreV1().Pods(o.Namespace).GetLogs(o.Pod, opts).Stream(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// ExecOptions describes an interactive exec session.
type ExecOptions struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
	Stdin     io.Reader
	Stdout    io.Writer
	Sizes     remotecommand.TerminalSizeQueue
}

// DefaultShell prefers bash, then sh.
var DefaultShell = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; elif command -v ash >/dev/null 2>&1; then exec ash; else exec sh; fi"}

// Exec runs an interactive TTY command in a container. Blocked on read-only clusters.
func Exec(ctx context.Context, s *cluster.Session, o ExecOptions) error {
	if err := s.CheckWritable(); err != nil {
		return err
	}
	cmd := o.Command
	if len(cmd) == 0 {
		cmd = DefaultShell
	}
	req := s.Typed.CoreV1().RESTClient().Post().Resource("pods").Namespace(o.Namespace).Name(o.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: o.Container, Command: cmd, Stdin: true, Stdout: true, Stderr: false, TTY: true}, scheme.ParameterCodec)
	ws, err := remotecommand.NewWebSocketExecutor(s.Config, http.MethodGet, req.URL().String())
	if err != nil {
		return err
	}
	spdy, err := remotecommand.NewSPDYExecutor(s.Config, http.MethodPost, req.URL())
	if err != nil {
		return err
	}
	exec, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return err
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: o.Stdin, Stdout: o.Stdout, Tty: true, TerminalSizeQueue: o.Sizes})
}
