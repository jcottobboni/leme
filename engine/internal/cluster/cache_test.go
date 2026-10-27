package cluster

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jcottobboni/leme/engine/internal/kube"
)

type testKey struct{}

func TestCachedSharesOneLoad(t *testing.T) {
	s := NewStaticSession(kube.Context{Name: "t"}, nil, nil, nil, true)
	var loads atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := Cached(s, testKey{}, time.Minute, false, func() (int, error) {
				loads.Add(1)
				<-release
				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("got %d %v", v, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Fatalf("%d concurrent loads, want 1", n)
	}
	// Served from the cache; refresh reloads.
	v, _ := Cached(s, testKey{}, time.Minute, false, func() (int, error) { return 0, errors.New("must not load") })
	if v != 42 {
		t.Fatalf("cached value: %d", v)
	}
	if v, _ := Cached(s, testKey{}, time.Minute, true, func() (int, error) { return 7, nil }); v != 7 {
		t.Fatalf("refresh: %d", v)
	}
}

func TestCachedTTLAndErrors(t *testing.T) {
	s := NewStaticSession(kube.Context{Name: "t"}, nil, nil, nil, true)
	if _, err := Cached(s, testKey{}, time.Minute, false, func() (string, error) { return "", errors.New("boom") }); err == nil {
		t.Fatal("error not returned")
	}
	// A failed load is not kept.
	if v, err := Cached(s, testKey{}, time.Minute, false, func() (string, error) { return "ok", nil }); err != nil || v != "ok" {
		t.Fatalf("after failure: %q %v", v, err)
	}
	if v, _ := Cached(s, testKey{}, time.Nanosecond, false, func() (string, error) { return "new", nil }); v != "new" {
		t.Fatalf("expired value reused: %q", v)
	}
	// Keys of different types never collide.
	type otherKey struct{}
	if v, _ := Cached(s, otherKey{}, 0, false, func() (string, error) { return "other", nil }); v != "other" {
		t.Fatal(v)
	}
}

func TestCachedPanicDoesNotBlockWaiters(t *testing.T) {
	s := NewStaticSession(kube.Context{Name: "t"}, nil, nil, nil, true)
	func() {
		defer func() { _ = recover() }()
		_, _ = Cached(s, testKey{}, 0, false, func() (int, error) { panic("x") })
	}()
	if v, err := Cached(s, testKey{}, 0, false, func() (int, error) { return 1, nil }); err != nil || v != 1 {
		t.Fatalf("after panic: %d %v", v, err)
	}
}

func TestOnClose(t *testing.T) {
	s := NewStaticSession(kube.Context{Name: "t"}, nil, nil, nil, true)
	done := make(chan struct{})
	s.OnClose(func() { close(done) })
	s.endLife()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnClose callback not run")
	}
}
