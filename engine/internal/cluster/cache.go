package cluster

import (
	"context"
	"sync"
	"time"
)

// sessionCache holds values that live as long as one session (discovery for Helm, detected
// Prometheus, the search index…). Features used to keep their own maps keyed by *Session and
// clean them up in different ways; the cache on the session goes away with it.
type sessionCache struct {
	mu sync.Mutex
	m  map[any]*cacheEntry
}

type cacheEntry struct {
	done chan struct{} // closed when the load finished
	val  any
	err  error
	at   time.Time
}

// OnClose runs fn once, in its own goroutine, when the session is disconnected.
func (s *Session) OnClose(fn func()) { context.AfterFunc(s.life, fn) }

func (s *Session) dropCache() {
	s.cache.mu.Lock()
	s.cache.m = nil
	s.cache.mu.Unlock()
}

// Cached returns the session's value for key, calling load when there is none, when it is older
// than ttl (ttl <= 0: never expires) or when refresh is set. Concurrent callers of the same key
// share one load. A failed load is returned to the callers waiting on it but not kept. Use an
// unexported key type per feature so keys of different packages never collide.
func Cached[T any](s *Session, key any, ttl time.Duration, refresh bool, load func() (T, error)) (T, error) {
	c := &s.cache
	c.mu.Lock()
	if c.m == nil {
		c.m = map[any]*cacheEntry{}
	}
	if e, ok := c.m[key]; ok {
		select {
		case <-e.done:
			if !refresh && e.err == nil && (ttl <= 0 || time.Since(e.at) < ttl) {
				c.mu.Unlock()
				v, _ := e.val.(T)
				return v, nil
			}
		default:
			// Someone is loading it right now: wait for that result instead of loading twice.
			c.mu.Unlock()
			<-e.done
			v, _ := e.val.(T)
			return v, e.err
		}
	}
	e := &cacheEntry{done: make(chan struct{})}
	c.m[key] = e
	c.mu.Unlock()

	var v T
	var err error
	finished := false
	defer func() {
		// Also runs while a panicking load unwinds: waiters get an error instead of blocking forever.
		e.val, e.err, e.at = v, err, time.Now()
		if !finished {
			e.err = errLoadPanicked
		}
		close(e.done)
		if e.err != nil {
			c.mu.Lock()
			if c.m[key] == e {
				delete(c.m, key)
			}
			c.mu.Unlock()
		}
	}()
	v, err = load()
	finished = true
	return v, err
}

type cacheError string

func (e cacheError) Error() string { return string(e) }

const errLoadPanicked = cacheError("session cache: load panicked")
