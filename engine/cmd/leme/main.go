// Command leme runs the local engine that backs the desktop UI.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jcottobboni/leme/engine/internal/api"
	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
	"github.com/jcottobboni/leme/engine/internal/security"
	"github.com/jcottobboni/leme/engine/internal/settings"
)

// version is set at build time with -X main.version from app/package.json (Makefile, release
// workflow); a plain `go build` reports "dev".
var version = "dev"

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Leme %s\n\nusage: leme serve [--addr 127.0.0.1:0] [--static DIR] [--kubeconfig FILE]...\n", version)
		os.Exit(2)
	}
	fsFlags := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fsFlags.String("addr", "127.0.0.1:0", "loopback listen address")
	static := fsFlags.String("static", "", "directory with the built UI")
	var kubeconfigs, devOrigins listFlag
	fsFlags.Var(&kubeconfigs, "kubeconfig", "extra kubeconfig file (repeatable)")
	fsFlags.Var(&devOrigins, "dev-origin", "extra allowed Origin for UI dev server (repeatable)")
	_ = fsFlags.Parse(os.Args[2:])

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		logger.Error("refusing to listen on a non-loopback address", "addr", *addr)
		os.Exit(2)
	}

	token := os.Getenv("LEME_TOKEN")
	printToken := token == ""
	if token == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			logger.Error("cannot generate session token", "err", err)
			os.Exit(1)
		}
		token = hex.EncodeToString(b)
	}
	os.Unsetenv("LEME_TOKEN")

	dir, err := settings.Dir()
	if err != nil {
		logger.Error("config dir", "err", err)
		os.Exit(1)
	}
	store, err := settings.Open(dir)
	if err != nil {
		logger.Error("settings", "err", err)
		os.Exit(1)
	}
	security.CleanStaleExports()
	mgr := cluster.NewManager(kube.NewLoader(kubeconfigs))
	srv := api.New(mgr, api.Options{Token: token, StaticDir: *static, DevOrigins: devOrigins, Logger: logger, Settings: store, ConfigDir: dir})
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("listen", "err", err)
		os.Exit(1)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	srv.Allow(port)

	ready := map[string]any{"event": "ready", "port": port, "version": version}
	b, _ := json.Marshal(ready)
	fmt.Println(string(b))
	if printToken {
		fmt.Fprintf(os.Stderr, "open: http://127.0.0.1:%s/#token=%s\n", port, token)
	}

	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut)
	}()
	// Exit when the parent (Electron) closes our stdin.
	if os.Getenv("LEME_EXIT_ON_STDIN_EOF") == "1" {
		go func() {
			buf := make([]byte, 64)
			for {
				if _, err := os.Stdin.Read(buf); err != nil {
					stop()
					return
				}
			}
		}()
	}
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "err", err)
		os.Exit(1)
	}
}
