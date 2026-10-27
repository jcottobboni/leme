package ops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// Aggregated logs: every pod matched by a label selector (a workload's spec.selector or one typed
// by the user), followed through the pods informer so pods of a rollout join and leave on their own.

// DefaultMaxPods is how many pods are streamed at once.
const DefaultMaxPods = 20

// maxLogLine bounds one line; longer lines are split.
const maxLogLine = 16 << 10

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// MultiLogOptions select the pods and the streams.
type MultiLogOptions struct {
	Namespace string
	Selector  string
	Container string // only containers with this name ("" = all)
	TailLines int64  // lines of history per container of the pods present at start
	MaxPods   int
}

// LogLine is one line of one container.
type LogLine struct {
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Time      string `json:"ts,omitempty"`
	Text      string `json:"text"`
}

// PodInfo describes a matched pod.
type PodInfo struct {
	Name       string   `json:"name"`
	Containers []string `json:"containers"`
	Phase      string   `json:"phase,omitempty"`
	Streaming  bool     `json:"streaming"`
}

// MultiLogMsg is what the stream emits. Type: lines | pods | join | leave | error (and, from the
// API, selector | dropped).
type MultiLogMsg struct {
	Type    string    `json:"type"`
	Lines   []LogLine `json:"lines,omitempty"`
	Pods    []PodInfo `json:"pods,omitempty"`
	Matched int       `json:"matched,omitempty"` // pods matching the selector
	Max     int       `json:"max,omitempty"`     // cap of streamed pods
	Pod     string    `json:"pod,omitempty"`
	Time    string    `json:"time,omitempty"`
	Error   string    `json:"error,omitempty"`
	// Selector and Namespace (type "selector"): what is followed. Dropped (type "dropped"): lines
	// discarded because the client did not keep up.
	Selector  string `json:"selector,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Dropped   int    `json:"dropped,omitempty"`
}

// WorkloadSelector returns the pod selector of an object: spec.selector as a LabelSelector
// (matchLabels/matchExpressions: Deployment, StatefulSet, DaemonSet, ReplicaSet, Job and CRDs that
// follow the convention) or as a plain map (Service, ReplicationController, DeploymentConfig).
func WorkloadSelector(u *unstructured.Unstructured) (string, error) {
	raw, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "selector")
	m, ok := raw.(map[string]any)
	if !found || !ok || len(m) == 0 {
		return "", fmt.Errorf("%s %s has no pod selector", u.GetKind(), u.GetName())
	}
	_, hasML := m["matchLabels"]
	_, hasME := m["matchExpressions"]
	var sel labels.Selector
	if hasML || hasME {
		var ls metav1.LabelSelector
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, &ls); err != nil {
			return "", err
		}
		s, err := metav1.LabelSelectorAsSelector(&ls)
		if err != nil {
			return "", err
		}
		sel = s
	} else {
		set := labels.Set{}
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("%s %s: unsupported selector", u.GetKind(), u.GetName())
			}
			set[k] = s
		}
		sel = labels.SelectorFromSet(set)
	}
	if sel.Empty() {
		return "", fmt.Errorf("%s %s selects every pod", u.GetKind(), u.GetName())
	}
	return sel.String(), nil
}

// container stream state of one pod.
type ctrStream struct {
	id     string // containerID being streamed ("" = none yet)
	cancel context.CancelFunc
	active bool      // a stream is open
	ended  time.Time // when the last stream ended or failed (zero: never)
	last   time.Time // timestamp of the last line delivered (resume point)
}

type podState struct {
	pod       *corev1.Pod
	streaming bool
	known     bool // already reported in a "pods" message
	initial   bool // present at start: history limited to TailLines
	ctrs      map[string]*ctrStream
}

type multiLog struct {
	sess *cluster.Session
	o    MultiLogOptions
	sel  labels.Selector
	emit func(MultiLogMsg)

	mu   sync.Mutex
	pods map[string]*podState
	wake chan struct{}
	wg   sync.WaitGroup
}

// subscriber side of the informer: keep the newest matching pods, wake the loop.
func (m *multiLog) OnUpsert(u *unstructured.Unstructured) {
	var p corev1.Pod
	if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &p) != nil {
		return
	}
	m.mu.Lock()
	st, ok := m.pods[p.Name]
	switch {
	case !m.sel.Matches(labels.Set(p.Labels)):
		if ok {
			st.pod = nil // left the selection: treated like a deletion
		}
	case ok:
		st.pod = &p
	default:
		m.pods[p.Name] = &podState{pod: &p, ctrs: map[string]*ctrStream{}}
	}
	m.mu.Unlock()
	m.poke()
}

func (m *multiLog) OnDelete(u *unstructured.Unstructured) {
	m.mu.Lock()
	if st, ok := m.pods[u.GetName()]; ok {
		st.pod = nil
	}
	m.mu.Unlock()
	m.poke()
}

func (m *multiLog) OnError(err error) { m.emit(MultiLogMsg{Type: "error", Error: err.Error()}) }

func (m *multiLog) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// StreamMultiLogs follows the logs of every pod matching o.Selector in o.Namespace until ctx ends.
// It is a read: allowed on read-only clusters. emit is called from several goroutines and must be
// safe for concurrent use.
func StreamMultiLogs(ctx context.Context, s *cluster.Session, o MultiLogOptions, emit func(MultiLogMsg)) error {
	sel, err := labels.Parse(o.Selector)
	if err != nil {
		return fmt.Errorf("invalid label selector: %w", err)
	}
	if sel.Empty() {
		return errors.New("a label selector is required (it would select every pod in the namespace)")
	}
	if o.Namespace == "" {
		return errors.New("namespace is required")
	}
	if o.MaxPods <= 0 {
		o.MaxPods = DefaultMaxPods
	}
	if s.Informers == nil {
		return cluster.ErrClosed
	}
	m := &multiLog{sess: s, o: o, sel: sel, emit: emit, pods: map[string]*podState{}, wake: make(chan struct{}, 1)}
	key := cluster.Key{GVR: podsGVR, Namespace: o.Namespace}
	snapCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	objs, err := s.Informers.Subscribe(snapCtx, key, m)
	cancel()
	if err != nil {
		return err
	}
	defer s.Informers.Unsubscribe(key, m)
	m.mu.Lock()
	for _, u := range objs {
		var p corev1.Pod
		if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &p) != nil || !sel.Matches(labels.Set(p.Labels)) {
			continue
		}
		m.pods[p.Name] = &podState{pod: &p, initial: true, ctrs: map[string]*ctrStream{}}
	}
	m.mu.Unlock()

	defer m.wg.Wait()
	defer m.stopAll()
	retry := time.NewTicker(2 * time.Second)
	defer retry.Stop()
	m.reconcile(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.wake:
		case <-retry.C:
		}
		m.reconcile(ctx, false)
	}
}

func (m *multiLog) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.pods {
		for _, c := range st.ctrs {
			if c.cancel != nil {
				c.cancel()
			}
		}
	}
}

// reconcile applies the pod set: leaves, joins (within the cap, oldest pods first) and streams of
// containers that started or restarted.
func (m *multiLog) reconcile(ctx context.Context, first bool) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var msgs []MultiLogMsg
	m.mu.Lock()
	changed := first
	for name, st := range m.pods {
		if st.pod != nil {
			continue
		}
		for _, c := range st.ctrs {
			if c.cancel != nil {
				c.cancel()
			}
		}
		if st.streaming {
			msgs = append(msgs, MultiLogMsg{Type: "leave", Pod: name, Time: now})
		}
		delete(m.pods, name)
		changed = true
	}
	names := make([]string, 0, len(m.pods))
	streaming := 0
	for name, st := range m.pods {
		names = append(names, name)
		if st.streaming {
			streaming++
		}
		if !st.known {
			st.known, changed = true, true
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := m.pods[names[i]].pod, m.pods[names[j]].pod
		if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
			return a.CreationTimestamp.Before(&b.CreationTimestamp)
		}
		return a.Name < b.Name
	})
	for _, name := range names {
		st := m.pods[name]
		if !st.streaming && streaming < m.o.MaxPods {
			st.streaming = true
			streaming++
			changed = true
			if !first {
				msgs = append(msgs, MultiLogMsg{Type: "join", Pod: name, Time: now})
			}
		}
		if st.streaming {
			m.startStreams(ctx, st)
		}
	}
	var state MultiLogMsg
	if changed {
		state = MultiLogMsg{Type: "pods", Matched: len(m.pods), Max: m.o.MaxPods, Pods: make([]PodInfo, 0, len(names))}
		for _, name := range names {
			st := m.pods[name]
			state.Pods = append(state.Pods, PodInfo{Name: name, Containers: m.containers(st.pod), Phase: string(st.pod.Status.Phase), Streaming: st.streaming})
		}
	}
	m.mu.Unlock()
	for _, msg := range msgs {
		m.emit(msg)
	}
	if changed {
		m.emit(state)
	}
}

func (m *multiLog) containers(p *corev1.Pod) []string {
	var out []string
	for _, c := range p.Spec.Containers {
		if m.o.Container == "" || c.Name == m.o.Container {
			out = append(out, c.Name)
		}
	}
	return out
}

// startStreams starts a stream for every container instance with logs not streamed yet. Called with m.mu held.
func (m *multiLog) startStreams(ctx context.Context, st *podState) {
	p := st.pod
	status := map[string]corev1.ContainerStatus{}
	for _, cs := range p.Status.ContainerStatuses {
		status[cs.Name] = cs
	}
	for _, name := range m.containers(p) {
		cs, ok := status[name]
		// Logs exist once the container ran: running, or terminated (a finished Job pod).
		if !ok || cs.ContainerID == "" || (cs.State.Running == nil && cs.State.Terminated == nil) {
			continue
		}
		c := st.ctrs[name]
		if c == nil {
			c = &ctrStream{}
			st.ctrs[name] = c
		}
		var since time.Time
		tail := int64(-1) // a new instance (pod joined, container restarted): its whole log, bounded
		switch {
		case c.id == "":
			if st.initial {
				tail = m.o.TailLines
			}
		case c.id == cs.ContainerID:
			// Same instance: reopen only a stream that dropped while the container still runs.
			if c.active || cs.State.Running == nil || time.Since(c.ended) < 3*time.Second {
				continue
			}
			since = c.last
			if since.IsZero() && st.initial {
				tail = m.o.TailLines
			}
		}
		if c.cancel != nil {
			c.cancel()
		}
		c.id, c.active = cs.ContainerID, true
		sctx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		m.wg.Add(1)
		go m.stream(sctx, p.Name, name, tail, since, c)
	}
}

func (m *multiLog) stream(ctx context.Context, pod, container string, tail int64, since time.Time, c *ctrStream) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		c.active, c.ended = false, time.Now()
		m.mu.Unlock()
		m.poke()
	}()
	opts := &corev1.PodLogOptions{Container: container, Follow: true, Timestamps: true}
	if !since.IsZero() {
		st := metav1.NewTime(since)
		opts.SinceTime = &st
	} else {
		if tail < 0 {
			tail = 5000
		}
		opts.TailLines = &tail
	}
	rc, err := m.sess.Typed.CoreV1().Pods(m.o.Namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return
	}
	defer rc.Close()
	readLines(ctx, rc, func(ts, text string) {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			// A resumed stream starts at the second of the last line: skip what was already sent.
			if !since.IsZero() && !t.After(since) {
				return
			}
			m.mu.Lock()
			c.last = t
			m.mu.Unlock()
		}
		m.emit(MultiLogMsg{Type: "lines", Lines: []LogLine{{Pod: pod, Container: container, Time: ts, Text: text}}})
	})
}

// readLines splits a log stream (with --timestamps) into lines, cutting very long ones.
func readLines(ctx context.Context, r io.Reader, fn func(ts, text string)) {
	br := bufio.NewReaderSize(r, maxLogLine)
	for ctx.Err() == nil {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			s := strings.TrimRight(string(line), "\r\n")
			ts := ""
			if i := strings.IndexByte(s, ' '); i > 0 {
				if _, perr := time.Parse(time.RFC3339Nano, s[:i]); perr == nil {
					ts, s = s[:i], s[i+1:]
				}
			}
			fn(ts, s)
		}
		if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}
