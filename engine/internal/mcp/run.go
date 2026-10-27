package mcp

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jcottobboni/leme/engine/internal/settings"
)

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// Main runs `leme mcp [--kubeconfig FILE]...` on stdin/stdout and returns the exit code.
func Main(args []string, version string) int {
	fl := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fl.SetOutput(os.Stderr)
	var kubeconfigs listFlag
	fl.Var(&kubeconfigs, "kubeconfig", "extra kubeconfig file (repeatable)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	dir, err := settings.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "leme mcp: config dir:", err)
		return 1
	}
	env, err := NewEnv(kubeconfigs, dir, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "leme mcp: settings:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := NewServer(env).Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "leme mcp:", err)
		return 1
	}
	return 0
}
