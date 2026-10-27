//go:build !windows

package plugins

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestParseFileRefusesSpecialFiles(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "x.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() { _, err := parseFile(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reading a FIFO blocked the registry")
	}
}
