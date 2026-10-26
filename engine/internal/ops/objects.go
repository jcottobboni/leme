// Package ops implements read and write operations on cluster objects.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

// HiddenValue replaces Secret values until the user explicitly reveals them.
const HiddenValue = "<hidden>"

const fieldManager = "leme"

// Ref identifies one object.
type Ref struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// GVR returns the schema identifier.
func (r Ref) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: r.Group, Version: r.Version, Resource: r.Resource}
}

func client(s *cluster.Session, gvr schema.GroupVersionResource, ns string) dynamic.ResourceInterface {
	if ns != "" {
		return s.Dynamic.Resource(gvr).Namespace(ns)
	}
	return s.Dynamic.Resource(gvr)
}

// ObjectView is an object prepared for display and editing.
type ObjectView struct {
	Object map[string]any `json:"object"`
	YAML   string         `json:"yaml"`
	Masked bool           `json:"masked"`
}

// Get fetches an object, strips noise and masks Secret values unless reveal is set.
func Get(ctx context.Context, s *cluster.Session, ref Ref, reveal bool) (*ObjectView, error) {
	u, err := client(s, ref.GVR(), ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	u.SetManagedFields(nil)
	masked := false
	if u.GetKind() == "Secret" && !reveal {
		masked = MaskSecret(u)
	}
	y, err := yaml.Marshal(u.Object)
	if err != nil {
		return nil, err
	}
	return &ObjectView{Object: u.Object, YAML: string(y), Masked: masked}, nil
}

// MaskSecret replaces Secret values (and the last-applied copy) with HiddenValue; it reports whether anything was hidden.
func MaskSecret(u *unstructured.Unstructured) bool {
	masked := false
	for _, field := range []string{"data", "stringData"} {
		m, found, _ := unstructured.NestedMap(u.Object, field)
		if !found {
			continue
		}
		for k := range m {
			m[k] = HiddenValue
			masked = true
		}
		_ = unstructured.SetNestedMap(u.Object, m, field)
	}
	ann := u.GetAnnotations()
	if _, ok := ann["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		ann["kubectl.kubernetes.io/last-applied-configuration"] = HiddenValue
		u.SetAnnotations(ann)
		masked = true
	}
	return masked
}

// ApplyResult is returned by Apply.
type ApplyResult struct {
	DryRun  bool   `json:"dryRun"`
	Created bool   `json:"created"`
	YAML    string `json:"yaml"`
}

// Apply creates or updates the object described by yamlText. With dryRun the API server validates without persisting.
func Apply(ctx context.Context, s *cluster.Session, yamlText string, dryRun bool) (*ApplyResult, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	if multiDoc.MatchString(strings.TrimSpace(yamlText)) {
		return nil, errors.New("only one YAML document can be applied at a time")
	}
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(yamlText), &m); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	u := &unstructured.Unstructured{Object: m}
	if u.GetName() == "" || u.GetKind() == "" || u.GetAPIVersion() == "" {
		return nil, errors.New("apiVersion, kind and metadata.name are required")
	}
	if u.GetKind() == "Secret" && containsHidden(u) {
		return nil, errors.New("this Secret still has hidden values: reveal it before editing")
	}
	gv, err := schema.ParseGroupVersion(u.GetAPIVersion())
	if err != nil {
		return nil, err
	}
	res, ok := s.LookupKind(gv.Group, u.GetKind())
	if !ok {
		return nil, fmt.Errorf("unknown kind %s in group %q", u.GetKind(), gv.Group)
	}
	gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: res.Resource}
	ns := ""
	if res.Namespaced {
		ns = u.GetNamespace()
		if ns == "" {
			ns = s.Namespace
			if ns == "" {
				ns = "default"
			}
			u.SetNamespace(ns)
		}
	}
	u.SetManagedFields(nil)
	var opts []string
	if dryRun {
		opts = []string{metav1.DryRunAll}
	}
	rc := client(s, gvr, ns)
	_, err = rc.Get(ctx, u.GetName(), metav1.GetOptions{})
	var out *unstructured.Unstructured
	created := false
	switch {
	case apierrors.IsNotFound(err):
		u.SetResourceVersion("")
		out, err = rc.Create(ctx, u, metav1.CreateOptions{DryRun: opts, FieldManager: fieldManager})
		created = true
	case err != nil:
		return nil, err
	default:
		// Optimistic concurrency: never overwrite changes made since the user loaded the object.
		if u.GetResourceVersion() == "" {
			return nil, apierrors.NewConflict(gvr.GroupResource(), u.GetName(), errors.New("metadata.resourceVersion is required to update an existing object; reload and edit again"))
		}
		out, err = rc.Update(ctx, u, metav1.UpdateOptions{DryRun: opts, FieldManager: fieldManager})
	}
	if err != nil {
		return nil, err
	}
	out.SetManagedFields(nil)
	if out.GetKind() == "Secret" {
		MaskSecret(out)
	}
	y, _ := yaml.Marshal(out.Object)
	return &ApplyResult{DryRun: dryRun, Created: created, YAML: string(y)}, nil
}

var multiDoc = regexp.MustCompile(`(?m)^---\s*\n\s*\S`)

func containsHidden(u *unstructured.Unstructured) bool {
	if u.GetAnnotations()["kubectl.kubernetes.io/last-applied-configuration"] == HiddenValue {
		return true
	}
	for _, field := range []string{"data", "stringData"} {
		m, _, _ := unstructured.NestedMap(u.Object, field)
		for _, v := range m {
			if v == HiddenValue {
				return true
			}
		}
	}
	return false
}

// Delete removes an object.
func Delete(ctx context.Context, s *cluster.Session, ref Ref) error {
	if err := s.CheckWritable(); err != nil {
		return err
	}
	policy := metav1.DeletePropagationBackground
	return client(s, ref.GVR(), ref.Namespace).Delete(ctx, ref.Name, metav1.DeleteOptions{PropagationPolicy: &policy})
}

// Scale sets spec.replicas.
func Scale(ctx context.Context, s *cluster.Session, ref Ref, replicas int64) error {
	if err := s.CheckWritable(); err != nil {
		return err
	}
	if replicas < 0 {
		return errors.New("replicas must be >= 0")
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"replicas": replicas}})
	_, err := client(s, ref.GVR(), ref.Namespace).Patch(ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

// Restart triggers a rollout restart like `kubectl rollout restart`.
func Restart(ctx context.Context, s *cluster.Session, ref Ref) error {
	if err := s.CheckWritable(); err != nil {
		return err
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]any{"kubectl.kubernetes.io/restartedAt": time.Now().Format(time.RFC3339)},
	}}}})
	_, err := client(s, ref.GVR(), ref.Namespace).Patch(ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}

// Events lists core events for an object uid.
func Events(ctx context.Context, s *cluster.Session, namespace, uid string) ([]corev1.Event, error) {
	l, err := s.Typed.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: "involvedObject.uid=" + uid})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}
