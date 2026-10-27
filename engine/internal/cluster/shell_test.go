package cluster

import (
	"errors"
	"testing"

	"github.com/jcottobboni/leme/engine/internal/kube"
)

func TestShellPermission(t *testing.T) {
	s := NewStaticSession(kube.Context{Name: "c"}, nil, nil, nil, true)
	if err := s.CheckShell(); !errors.Is(err, ErrNoShell) {
		t.Fatalf("locked session without permission: %v", err)
	}
	s.SetShell(true)
	if s.CheckShell() != nil || s.CheckWritable() == nil {
		t.Fatal("shell allowed, writes still locked")
	}
	scope := s.ShellScope()
	s.SetShell(false)
	if scope.Err() == nil {
		t.Fatal("revoking must end open shells")
	}
	if err := s.CheckShell(); !errors.Is(err, ErrNoShell) {
		t.Fatalf("revoked: %v", err)
	}
	s.SetReadOnly(false)
	if s.CheckShell() != nil {
		t.Fatal("an unlocked session allows shells")
	}
	w := s.ShellScope()
	s.SetReadOnly(true)
	if w.Err() == nil {
		t.Fatal("locking must end shells opened through the write unlock")
	}
}
