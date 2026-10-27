package history

import (
	"log/slog"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/objdiff"
	"github.com/jcottobboni/leme/engine/internal/redact"
)

// maxOps bounds the operations kept for one change.
const maxOps = 200

// Recorder turns informer events of one cluster into history entries. It implements
// cluster.Observer.
type Recorder struct {
	log    *clusterLog
	logger *slog.Logger
}

// Recorder returns the recorder of a cluster (key: the kubeconfig fingerprint of its context).
func (s *Store) Recorder(key string, logger *slog.Logger) (*Recorder, error) {
	l, err := s.log(key)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{log: l, logger: logger}, nil
}

// ignored resources change all the time without a user behind it (events, leader-election leases,
// endpoints derived from pods): recording them would only push real changes out of the file.
func ignored(gvr schema.GroupVersionResource) bool {
	if !kinds.Builtin(gvr.Group) {
		return false
	}
	switch gvr.Group + "/" + gvr.Resource {
	case "/events", "events.k8s.io/events", "coordination.k8s.io/leases", "/endpoints", "discovery.k8s.io/endpointslices",
		"/componentstatuses", "apiregistration.k8s.io/apiservices", "flowcontrol.apiserver.k8s.io/flowschemas",
		"flowcontrol.apiserver.k8s.io/prioritylevelconfigurations":
		return true
	}
	return false
}

// keysOnly reports the built-in kinds whose values are never stored: Secrets, ConfigMaps and
// OpenShift Templates (their parameters hold generated passwords and their objects embed Secrets).
func keysOnly(gvr schema.GroupVersionResource) bool {
	if gvr.Group == "" && (gvr.Resource == "secrets" || gvr.Resource == "configmaps") {
		return true
	}
	return gvr.Group == "template.openshift.io" && (gvr.Resource == "templates" || gvr.Resource == "processedtemplates")
}

// noisyAnnotations change without a user action (controllers bookkeeping).
var noisyAnnotations = append([]string{"control-plane.alpha.kubernetes.io/leader"}, objdiff.ControlAnnotations...)

// tracked is the part of an object whose changes are recorded: every top-level field except
// apiVersion, kind, metadata and status, plus metadata.labels and metadata.annotations.
func tracked(gvr schema.GroupVersionResource, u *unstructured.Unstructured) map[string]any {
	out := map[string]any{}
	for k, v := range u.Object {
		switch k {
		case "apiVersion", "kind", "metadata", "status":
			continue
		}
		out[k] = v
	}
	md := map[string]any{}
	if l := u.GetLabels(); len(l) > 0 {
		md["labels"] = toAny(l)
	}
	if a := u.GetAnnotations(); len(a) > 0 {
		m := toAny(a)
		for _, k := range noisyAnnotations {
			delete(m, k)
		}
		if len(m) > 0 {
			md["annotations"] = m
		}
	}
	if len(md) > 0 {
		out["metadata"] = md
	}
	// Scheduling binds a pod to its node through spec.nodeName: not a change of its desired state.
	if gvr.Group == "" && gvr.Resource == "pods" {
		if spec, ok := out["spec"].(map[string]any); ok {
			if _, ok := spec["nodeName"]; ok {
				c := make(map[string]any, len(spec))
				for k, v := range spec {
					if k != "nodeName" {
						c[k] = v
					}
				}
				out["spec"] = c
			}
		}
	}
	return out
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func entryFor(gvr schema.GroupVersionResource, u *unstructured.Unstructured, typ string) *Entry {
	return &Entry{Type: typ, Group: gvr.Group, Version: gvr.Version, Resource: gvr.Resource, Kind: u.GetKind(),
		Namespace: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID()), ResourceVersion: u.GetResourceVersion()}
}

// mark records that uid reached rv; it reports false when that was already recorded.
func (r *Recorder) mark(uid, rv string, deleted bool) bool {
	l := r.log
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, ok := l.seen[uid]
	if deleted {
		if ok && prev == "" {
			return false
		}
		l.seen[uid] = ""
		return true
	}
	if ok && (prev == rv || prev == "") {
		return false
	}
	if len(l.seen) >= maxSeen {
		l.seen = map[string]string{}
	}
	l.seen[uid] = rv
	return true
}

func (r *Recorder) write(e *Entry) {
	if err := r.log.append(e); err != nil {
		r.logger.Warn("history: cannot record change", "err", err)
	}
}

// ObserveAdd records objects created while an informer runs (not those of its first list).
func (r *Recorder) ObserveAdd(gvr schema.GroupVersionResource, u *unstructured.Unstructured, initial bool) {
	uid := string(u.GetUID())
	if ignored(gvr) {
		return
	}
	if initial {
		// Known from now on: a later update is a change, a second informer's add is not a creation.
		r.seed(uid, u.GetResourceVersion())
		return
	}
	if !r.mark(uid, u.GetResourceVersion(), false) {
		return
	}
	r.write(entryFor(gvr, u, Created))
}

func (r *Recorder) seed(uid, rv string) {
	l := r.log
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[uid]; !ok && len(l.seen) < maxSeen {
		l.seen[uid] = rv
	}
}

// ObserveUpdate records a change of the tracked part of an object, with its JSON Patch.
func (r *Recorder) ObserveUpdate(gvr schema.GroupVersionResource, old, u *unstructured.Unstructured) {
	if ignored(gvr) || old.GetResourceVersion() == u.GetResourceVersion() {
		return
	}
	a, b := tracked(gvr, old), tracked(gvr, u)
	if reflect.DeepEqual(a, b) {
		return
	}
	if !r.mark(string(u.GetUID()), u.GetResourceVersion(), false) {
		return
	}
	e := entryFor(gvr, u, Updated)
	ops := objdiff.Diff(a, b)
	if len(ops) > maxOps {
		ops = ops[:maxOps]
		e.Truncated = true
	}
	if keysOnly(gvr) {
		e.KeysOnly = true
		for _, op := range ops {
			e.Ops = append(e.Ops, Op{Op: op.Op, Path: op.Path})
		}
	} else {
		// Values come from redacted copies of the whole objects (kind and annotations included, so the
		// kind-specific and embedded-manifest rules apply), cut to the same tracked part. A path the
		// redaction dropped is shown as masked.
		ra, rb := tracked(gvr, redact.Object(old)), tracked(gvr, redact.Object(u))
		for _, op := range ops {
			o := Op{Op: op.Op, Path: op.Path}
			if op.Op != "remove" {
				o.Value = valueAt(rb, op.Path)
			}
			if op.Op != "add" {
				o.Old = valueAt(ra, op.Path)
			}
			e.Ops = append(e.Ops, o)
		}
	}
	r.write(e)
}

func valueAt(doc any, path string) any {
	v, ok := objdiff.Lookup(doc, path)
	if !ok {
		return redact.Mask
	}
	if s, ok := v.(string); ok && strings.Contains(s, redact.Mask) {
		return redact.Mask
	}
	return v
}

// ObserveDelete records a deletion.
func (r *Recorder) ObserveDelete(gvr schema.GroupVersionResource, u *unstructured.Unstructured) {
	if ignored(gvr) {
		return
	}
	if !r.mark(string(u.GetUID()), "", true) {
		return
	}
	r.write(entryFor(gvr, u, Deleted))
}
