package ops

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kube"
)

func fakePod(name string, lbl map[string]any, running bool) *unstructured.Unstructured {
	status := map[string]any{"phase": "Pending"}
	if running {
		status = map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{
			"name": "app", "containerID": "containerd://" + name, "ready": true, "restartCount": int64(0), "image": "x", "imageID": "x",
			"state": map[string]any{"running": map[string]any{"startedAt": "2026-10-27T10:00:00Z"}},
		}}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": "demo", "uid": "uid-" + name, "labels": lbl, "creationTimestamp": "2026-10-27T10:00:00Z"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "x"}}},
		"status":   status,
	}}
}

type recorder struct {
	mu   sync.Mutex
	msgs []MultiLogMsg
}

func (r *recorder) emit(m MultiLogMsg) {
	r.mu.Lock()
	r.msgs = append(r.msgs, m)
	r.mu.Unlock()
}

func (r *recorder) wait(t *testing.T, what string, fn func(MultiLogMsg) bool) MultiLogMsg {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.msgs {
			if fn(m) {
				r.mu.Unlock()
				return m
			}
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("timeout waiting for %s; got %+v", what, r.msgs)
	return MultiLogMsg{}
}

func TestMultiLogsFollowPodsJoiningAndLeaving(t *testing.T) {
	web := map[string]any{"app": "web"}
	podsGVR := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{podsGVR: "PodList"},
		fakePod("web-1", web, true), fakePod("web-2", web, true), fakePod("other", map[string]any{"app": "db"}, true))
	s := cluster.NewStaticSession(kube.Context{Name: "test"}, kubefake.NewClientset(), dyn, nil, true)
	s.Informers = cluster.NewInformerManager(dyn, time.Minute)
	defer s.Informers.StopAll()

	ctx, cancel := context.WithCancel(context.Background())
	r := &recorder{}
	done := make(chan error, 1)
	// Read-only session: logs are a read and must work.
	go func() {
		done <- StreamMultiLogs(ctx, s, MultiLogOptions{Namespace: "demo", Selector: "app=web", TailLines: 10, MaxPods: 2}, r.emit)
	}()

	first := r.wait(t, "initial pods", func(m MultiLogMsg) bool { return m.Type == "pods" })
	if first.Matched != 2 || len(first.Pods) != 2 || first.Pods[0].Name != "web-1" || !first.Pods[0].Streaming || first.Max != 2 {
		t.Fatalf("initial pods = %+v", first)
	}
	// The fake API answers every log request with "fake logs".
	line := r.wait(t, "a line", func(m MultiLogMsg) bool { return m.Type == "lines" })
	if l := line.Lines[0]; (l.Pod != "web-1" && l.Pod != "web-2") || l.Container != "app" || l.Text != "fake logs" {
		t.Fatalf("line = %+v", line)
	}

	// A third pod is over the cap of 2: matched but not streamed.
	if _, err := dyn.Resource(podsGVR).Namespace("demo").Create(ctx, fakePod("web-3", web, true), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	r.wait(t, "capped pod", func(m MultiLogMsg) bool {
		return m.Type == "pods" && m.Matched == 3 && len(m.Pods) == 3 && !m.Pods[2].Streaming
	})
	// One leaves: the waiting pod takes its slot.
	if err := dyn.Resource(podsGVR).Namespace("demo").Delete(ctx, "web-1", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	r.wait(t, "leave", func(m MultiLogMsg) bool { return m.Type == "leave" && m.Pod == "web-1" })
	r.wait(t, "join", func(m MultiLogMsg) bool { return m.Type == "join" && m.Pod == "web-3" })
	r.wait(t, "web-3 lines", func(m MultiLogMsg) bool { return m.Type == "lines" && m.Lines[0].Pod == "web-3" })

	// Pods of other workloads never appear.
	r.mu.Lock()
	for _, m := range r.msgs {
		if m.Pod == "other" || (len(m.Lines) > 0 && m.Lines[0].Pod == "other") {
			t.Fatalf("pod outside the selector: %+v", m)
		}
	}
	r.mu.Unlock()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop")
	}
}

func TestMultiLogsRejectsEmptySelector(t *testing.T) {
	s := cluster.NewStaticSession(kube.Context{Name: "test"}, kubefake.NewClientset(), nil, nil, true)
	for _, sel := range []string{"", "app in ("} {
		if err := StreamMultiLogs(context.Background(), s, MultiLogOptions{Namespace: "demo", Selector: sel}, func(MultiLogMsg) {}); err == nil {
			t.Fatalf("selector %q accepted", sel)
		}
	}
}

func TestWorkloadSelector(t *testing.T) {
	mk := func(sel map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"kind": "X", "metadata": map[string]any{"name": "w"}, "spec": map[string]any{"selector": sel}}}
	}
	cases := []struct {
		sel  map[string]any
		want string
	}{
		{map[string]any{"matchLabels": map[string]any{"app": "web"}}, "app=web"},
		{map[string]any{"matchExpressions": []any{map[string]any{"key": "tier", "operator": "In", "values": []any{"a", "b"}}}}, "tier in (a,b)"},
		{map[string]any{"app": "web"}, "app=web"}, // Service, ReplicationController
	}
	for _, c := range cases {
		got, err := WorkloadSelector(mk(c.sel))
		if err != nil || got != c.want {
			t.Fatalf("%v: %q %v", c.sel, got, err)
		}
	}
	for _, bad := range []map[string]any{nil, {"matchLabels": map[string]any{}}} {
		if _, err := WorkloadSelector(mk(bad)); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestReadLinesSplitsTimestamps(t *testing.T) {
	in := "2026-10-27T10:00:00.123456789Z hello world\nno-timestamp line\n" + strings.Repeat("x", maxLogLine+10) + "\n"
	var got [][2]string
	readLines(context.Background(), strings.NewReader(in), func(ts, text string) { got = append(got, [2]string{ts, text}) })
	if len(got) < 4 || got[0] != [2]string{"2026-10-27T10:00:00.123456789Z", "hello world"} || got[1] != [2]string{"", "no-timestamp line"} {
		t.Fatalf("lines = %v", got[:2])
	}
}
