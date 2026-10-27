package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAddRecentNewestFirst(t *testing.T) {
	l := Open(t.TempDir())
	if got, err := l.Recent(5); err != nil || len(got) != 0 {
		t.Fatalf("empty log: %v %v", got, err)
	}
	for i := 0; i < 10; i++ {
		if err := l.Add(Entry{Source: SourceUI, Action: "apply", Object: fmt.Sprintf("Deployment/demo/w%d", i), Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.Recent(3)
	if err != nil || len(got) != 3 || got[0].Object != "Deployment/demo/w9" || got[2].Object != "Deployment/demo/w7" || got[0].Time.IsZero() {
		t.Fatalf("recent: %+v %v", got, err)
	}
	if all, _ := l.Recent(100); len(all) != 10 {
		t.Fatalf("all: %d", len(all))
	}
	st, err := os.Stat(l.path)
	// Windows has no POSIX permission bits.
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Fatalf("mode: %v %v", st, err)
	}
}

// Recent reads across chunk boundaries and skips lines that are not entries.
func TestRecentAcrossChunks(t *testing.T) {
	l := Open(t.TempDir())
	pad := strings.Repeat("x", 900)
	for i := 0; i < 400; i++ { // ~370 KB: several 64 KB chunks, lines straddle them
		if err := l.Add(Entry{Source: SourceMCP, Action: "propose", Detail: pad, Object: fmt.Sprintf("o%d", i), Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("not json\n\n")
	f.Close()
	_ = l.Add(Entry{Source: SourceUI, Action: "delete", Object: "last", Result: "ok"})
	got, err := l.Recent(400)
	if err != nil || len(got) != 400 {
		t.Fatalf("got %d entries, %v", len(got), err)
	}
	if got[0].Object != "last" || got[1].Object != "o399" || got[399].Object != "o1" {
		t.Fatalf("order: %s %s %s", got[0].Object, got[1].Object, got[399].Object)
	}
	for i := 1; i < len(got); i++ {
		if want := fmt.Sprintf("o%d", 400-i); got[i].Object != want {
			t.Fatalf("entry %d = %s, want %s", i, got[i].Object, want)
		}
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	l.maxSize = 2000
	for i := 0; i < 60; i++ {
		if err := l.Add(Entry{Source: SourceUI, Action: "scale", Object: fmt.Sprintf("o%d", i), Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	cur, err := os.Stat(filepath.Join(dir, "audit.jsonl"))
	if err != nil || cur.Size() > 2000 {
		t.Fatalf("current file not rotated: %v %v", cur, err)
	}
	old, err := os.Stat(filepath.Join(dir, "audit.jsonl.1"))
	if err != nil || old.Size() > 2000 {
		t.Fatalf("rotated file: %v %v", old, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl.2")); !os.IsNotExist(err) {
		t.Fatal("only one old file is kept")
	}
	// Recent continues into the rotated file.
	inCurrent, _ := tail(filepath.Join(dir, "audit.jsonl"), 1000)
	got, err := l.Recent(1000)
	if err != nil || len(got) <= len(inCurrent) || got[0].Object != "o59" {
		t.Fatalf("recent across rotation: %d (current %d) %v", len(got), len(inCurrent), err)
	}
	for i := 1; i < len(got); i++ {
		if want := fmt.Sprintf("o%d", 59-i); got[i].Object != want {
			t.Fatalf("entry %d = %s, want %s", i, got[i].Object, want)
		}
	}
}

func TestAddFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l := Open(filepath.Join(blocker, "sub")) // parent is a file: cannot create the directory
	if err := l.Add(Entry{Source: SourceUI, Action: "apply", Result: "ok"}); err == nil {
		t.Fatal("expected an error")
	}
	var nilLog *Log
	if err := nilLog.Add(Entry{}); err != nil {
		t.Fatal(err)
	}
}
