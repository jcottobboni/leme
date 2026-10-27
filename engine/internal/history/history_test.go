package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const key = "0123456789abcdef01234567"

var (
	deployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	secrets     = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	configmaps  = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	events      = schema.GroupVersionResource{Version: "v1", Resource: "events"}
	pods        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
)

func deploy(rv, image string, env string) *unstructured.Unstructured {
	c := map[string]any{"name": "app", "image": image}
	if env != "" {
		c["env"] = []any{map[string]any{"name": "DB_PASSWORD", "value": env}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "demo", "uid": "u1", "resourceVersion": rv},
		"spec":     map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{c}}}},
		"status":   map[string]any{"observedGeneration": rv},
	}}
}

func open(t *testing.T) (*Store, *Recorder, string) {
	t.Helper()
	dir := t.TempDir()
	s := Open(dir)
	r, err := s.Recorder(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, dir
}

func all(t *testing.T, s *Store, q Query) []Entry {
	t.Helper()
	p, err := s.Query(key, q)
	if err != nil {
		t.Fatal(err)
	}
	return p.Entries
}

func TestRecordsUpdateWithPatchAndPersists(t *testing.T) {
	s, r, dir := open(t)
	r.ObserveAdd(deployments, deploy("1", "busybox:1.36", ""), true) // initial list: not a creation
	r.ObserveUpdate(deployments, deploy("1", "busybox:1.36", ""), deploy("2", "busybox:1.36", ""))
	if got := all(t, s, Query{}); len(got) != 0 {
		t.Fatalf("status-only change recorded: %+v", got)
	}
	r.ObserveUpdate(deployments, deploy("2", "busybox:1.36", ""), deploy("3", "busybox:latest", ""))
	// The same event from a second informer (namespace and cluster-wide) is recorded once.
	r.ObserveUpdate(deployments, deploy("2", "busybox:1.36", ""), deploy("3", "busybox:latest", ""))
	got := all(t, s, Query{Group: "apps", Resource: "deployments", Namespace: "demo", Name: "web"})
	if len(got) != 1 {
		t.Fatalf("entries = %+v", got)
	}
	e := got[0]
	if e.Type != Updated || e.Kind != "Deployment" || len(e.Ops) != 1 || e.Ops[0].Path != "/spec/template/spec/containers/0/image" || e.Ops[0].Value != "busybox:latest" || e.Ops[0].Old != "busybox:1.36" {
		t.Fatalf("entry = %+v", e)
	}
	st, err := os.Stat(filepath.Join(dir, "history", key+".jsonl"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", st, err)
	}
	// A new process (engine restart) reads the same history.
	s2 := Open(dir)
	if got := all(t, s2, Query{Name: "web"}); len(got) != 1 || got[0].ID != e.ID {
		t.Fatalf("after reopen: %+v", got)
	}
}

func TestCreatedDeletedAndIgnored(t *testing.T) {
	s, r, _ := open(t)
	d := deploy("5", "nginx", "")
	r.ObserveAdd(deployments, d, false)
	r.ObserveAdd(deployments, d, false) // second informer
	r.ObserveDelete(deployments, d)
	r.ObserveDelete(deployments, d)
	ev := &unstructured.Unstructured{Object: map[string]any{"kind": "Event", "metadata": map[string]any{"name": "e", "uid": "e1", "resourceVersion": "1"}}}
	r.ObserveAdd(events, ev, false)
	got := all(t, s, Query{})
	if len(got) != 2 || got[0].Type != Deleted || got[1].Type != Created {
		t.Fatalf("entries (newest first) = %+v", got)
	}
	if got := all(t, s, Query{Type: Created}); len(got) != 1 {
		t.Fatalf("type filter: %+v", got)
	}
}

func TestSecretsAndConfigMapsRecordOnlyKeys(t *testing.T) {
	s, r, dir := open(t)
	sec := func(rv, pw string, extra bool) *unstructured.Unstructured {
		data := map[string]any{"password": pw}
		if extra {
			data["token"] = "dG9r"
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "db", "namespace": "demo", "uid": "s1", "resourceVersion": rv, "annotations": map[string]any{"note": "pw=" + pw}},
			"data":     data}}
	}
	r.ObserveUpdate(secrets, sec("1", "czNjcjN0", false), sec("2", "bmV3LXZhbHVl", true))
	cm := func(rv, v string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": "cfg", "namespace": "demo", "uid": "c1", "resourceVersion": rv}, "data": map[string]any{"LOG_LEVEL": v}}}
	}
	r.ObserveUpdate(configmaps, cm("1", "debug"), cm("2", "very-private-value"))
	got := all(t, s, Query{})
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	paths := map[string]bool{}
	for _, e := range got {
		if !e.KeysOnly {
			t.Fatalf("not keys-only: %+v", e)
		}
		for _, op := range e.Ops {
			if op.Value != nil || op.Old != nil {
				t.Fatalf("value recorded: %+v", op)
			}
			paths[op.Path] = true
		}
	}
	for _, p := range []string{"/data/password", "/data/token", "/metadata/annotations/note", "/data/LOG_LEVEL"} {
		if !paths[p] {
			t.Fatalf("missing path %s in %v", p, paths)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history", key+".jsonl"))
	for _, v := range []string{"czNjcjN0", "bmV3LXZhbHVl", "dG9r", "very-private-value", "debug"} {
		if strings.Contains(string(raw), v) {
			t.Fatalf("value %q written to disk: %s", v, raw)
		}
	}
}

func TestEnvValuesAreMasked(t *testing.T) {
	s, r, dir := open(t)
	r.ObserveUpdate(deployments, deploy("1", "app:1", "hunter2"), deploy("2", "app:1", "correct-horse"))
	got := all(t, s, Query{})
	if len(got) != 1 || len(got[0].Ops) != 1 || got[0].Ops[0].Value != "<hidden>" || got[0].Ops[0].Old != "<hidden>" {
		t.Fatalf("entries = %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history", key+".jsonl"))
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "correct-horse") {
		t.Fatalf("env value on disk: %s", raw)
	}
}

func TestPodSchedulingIsNotAChange(t *testing.T) {
	s, r, _ := open(t)
	pod := func(rv, node string) *unstructured.Unstructured {
		spec := map[string]any{"containers": []any{map[string]any{"name": "c", "image": "x"}}}
		if node != "" {
			spec["nodeName"] = node
		}
		return &unstructured.Unstructured{Object: map[string]any{"kind": "Pod", "metadata": map[string]any{"name": "p", "uid": "p1", "resourceVersion": rv}, "spec": spec}}
	}
	r.ObserveUpdate(pods, pod("1", ""), pod("2", "node-1"))
	if got := all(t, s, Query{}); len(got) != 0 {
		t.Fatalf("binding recorded: %+v", got)
	}
}

func TestRetentionAndSizeCompaction(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s := Open(dir)
	s.now = func() time.Time { return now }
	r, err := s.Recorder(key, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two old entries (8 days ago) and one recent one, written directly.
	for i, age := range []time.Duration{8 * 24 * time.Hour, 8*24*time.Hour - time.Minute, time.Hour} {
		e := entryFor(deployments, deploy("1", "x", ""), Updated)
		e.Time = now.Add(-age)
		e.Name = []string{"old1", "old2", "new"}[i]
		if err := r.log.append(e); err != nil {
			t.Fatal(err)
		}
	}
	if got := all(t, s, Query{}); len(got) != 1 || got[0].Name != "new" {
		t.Fatalf("retention on read: %+v", got)
	}
	// Compaction on open drops them from the file.
	s2 := Open(dir)
	s2.now = s.now
	if _, err := s2.Recorder(key, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history", key+".jsonl"))
	if strings.Count(string(raw), "\n") != 1 || !strings.Contains(string(raw), `"new"`) {
		t.Fatalf("after compaction: %s", raw)
	}

	// Size: past the limit the oldest entries go until the file is under 80 % of it.
	s3 := Open(t.TempDir())
	s3.SetLimits(DefaultMaxAge, 4096)
	r3, _ := s3.Recorder(key, nil)
	for i := 0; i < 100; i++ {
		e := entryFor(deployments, deploy("1", "x", ""), Updated)
		e.Name = "n" + strings.Repeat("x", 50) + string(rune('a'+i%26))
		if err := r3.log.append(e); err != nil {
			t.Fatal(err)
		}
	}
	st, _ := os.Stat(r3.log.path)
	if st.Size() > 4096 {
		t.Fatalf("file over the limit: %d", st.Size())
	}
	page, _ := s3.Query(key, Query{Limit: 500})
	if len(page.Entries) == 0 || len(page.Entries) >= 100 {
		t.Fatalf("kept %d entries", len(page.Entries))
	}
}

func TestPaging(t *testing.T) {
	s, r, _ := open(t)
	for i := 0; i < 7; i++ {
		e := entryFor(deployments, deploy("1", "x", ""), Updated)
		if err := r.log.append(e); err != nil {
			t.Fatal(err)
		}
	}
	p1, _ := s.Query(key, Query{Limit: 3})
	if len(p1.Entries) != 3 || p1.Next == 0 || p1.Entries[0].ID <= p1.Entries[2].ID {
		t.Fatalf("page 1: %+v", p1)
	}
	p2, _ := s.Query(key, Query{Limit: 3, Before: p1.Next})
	p3, _ := s.Query(key, Query{Limit: 3, Before: p2.Next})
	if len(p2.Entries) != 3 || len(p3.Entries) != 1 || p3.Next != 0 || p2.Entries[0].ID >= p1.Entries[2].ID {
		t.Fatalf("pages: %+v %+v", p2, p3)
	}
	b, _ := json.Marshal(p3)
	if strings.Contains(string(b), `"next"`) {
		t.Fatalf("last page has a cursor: %s", b)
	}
}

func TestInvalidKeyAndDisabled(t *testing.T) {
	if _, err := Open(t.TempDir()).Recorder("../etc", nil); err == nil {
		t.Fatal("path traversal key accepted")
	}
	if _, err := Open("").Query(key, Query{}); err == nil {
		t.Fatal("disabled store answered")
	}
}

// Kind- and annotation-dependent redaction must apply: an annotation embedding a manifest (kapp,
// last-applied of a Secret) and Template parameters never reach the file.
func TestEmbeddedManifestsAndTemplatesAreNotStored(t *testing.T) {
	s, r, dir := open(t)
	withAnn := func(rv, v string) *unstructured.Unstructured {
		d := deploy(rv, "app:1", "")
		d.SetAnnotations(map[string]string{"kapp.k14s.io/original": `{"apiVersion":"v1","kind":"Secret","data":{"pw":"` + v + `"}}`})
		return d
	}
	r.ObserveUpdate(deployments, withAnn("1", "b2xkU0VDUkVU"), withAnn("2", "bmV3U0VDUkVU"))
	templates := schema.GroupVersionResource{Group: "template.openshift.io", Version: "v1", Resource: "templates"}
	tpl := func(rv, pw string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "template.openshift.io/v1", "kind": "Template",
			"metadata":   map[string]any{"name": "db", "namespace": "demo", "uid": "t1", "resourceVersion": rv},
			"parameters": []any{map[string]any{"name": "DB_PASSWORD", "value": pw}},
		}}
	}
	r.ObserveUpdate(templates, tpl("1", "old-S3CRET"), tpl("2", "new-S3CRET"))
	if got := all(t, s, Query{}); len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "history", key+".jsonl"))
	for _, leak := range []string{"b2xkU0VDUkVU", "bmV3U0VDUkVU", "old-S3CRET", "new-S3CRET"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("%s on disk: %s", leak, raw)
		}
	}
}
