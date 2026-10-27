package ops

import (
	"context"
	"errors"
	"testing"
	"time"
)

func closedWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func waitEmpty(t *testing.T, f *Forwards) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(f.List()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("forward still listed: %+v", f.List())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A forwarder that dies after it was ready (pod deleted, connection lost) leaves the list.
func TestForwardDiesIsRemoved(t *testing.T) {
	f := NewForwards()
	done := make(chan error, 1)
	v := &Forward{ID: "a", ClusterID: "c", stop: make(chan struct{})}
	f.track(v, done, context.Background())
	if len(f.List()) != 1 {
		t.Fatal("not listed")
	}
	done <- errors.New("lost connection to pod")
	waitEmpty(t, f)
	if !closedWithin(v.stop, time.Second) {
		t.Fatal("stop channel not closed")
	}
}

func TestForwardEndsWithScopeAndStop(t *testing.T) {
	f := NewForwards()
	scope, cancel := context.WithCancel(context.Background())
	a := &Forward{ID: "a", ClusterID: "c", stop: make(chan struct{})}
	b := &Forward{ID: "b", ClusterID: "c", stop: make(chan struct{})}
	f.track(a, make(chan error, 1), scope)
	f.track(b, make(chan error, 1), context.Background())
	cancel() // session locked or disconnected
	if !closedWithin(a.stop, time.Second) {
		t.Fatal("locking the session must stop the forward")
	}
	if !f.Stop("b") || f.Stop("b") {
		t.Fatal("Stop must succeed once")
	}
	waitEmpty(t, f)
}
