package ops

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

const revisionAnnotation = "deployment.kubernetes.io/revision"

// Revision is one rollout revision of a Deployment (backed by a ReplicaSet).
type Revision struct {
	Revision    int64     `json:"revision"`
	ReplicaSet  string    `json:"replicaSet"`
	Created     time.Time `json:"created"`
	Images      []string  `json:"images"`
	Replicas    int32     `json:"replicas"`
	ChangeCause string    `json:"changeCause,omitempty"`
	Current     bool      `json:"current"`
	Template    string    `json:"template"` // pod template YAML (without pod-template-hash)
}

func templateYAML(t *appsv1.ReplicaSet) string {
	tpl := t.Spec.Template.DeepCopy()
	delete(tpl.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	b, _ := yaml.Marshal(tpl)
	return string(b)
}

func ownedReplicaSets(ctx context.Context, s *cluster.Session, d *appsv1.Deployment) ([]appsv1.ReplicaSet, error) {
	sel, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, err
	}
	l, err := s.Typed.AppsV1().ReplicaSets(d.Namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, err
	}
	var out []appsv1.ReplicaSet
	for _, rs := range l.Items {
		if c := metav1.GetControllerOf(&rs); c != nil && c.UID == d.UID {
			out = append(out, rs)
		}
	}
	return out, nil
}

// RolloutHistory lists the revisions of a Deployment, newest first.
func RolloutHistory(ctx context.Context, s *cluster.Session, namespace, name string) ([]Revision, error) {
	d, err := s.Typed.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	rss, err := ownedReplicaSets(ctx, s, d)
	if err != nil {
		return nil, err
	}
	current := d.Annotations[revisionAnnotation]
	var out []Revision
	for i := range rss {
		rs := &rss[i]
		rev, err := strconv.ParseInt(rs.Annotations[revisionAnnotation], 10, 64)
		if err != nil {
			continue
		}
		r := Revision{Revision: rev, ReplicaSet: rs.Name, Created: rs.CreationTimestamp.Time, Replicas: rs.Status.Replicas,
			ChangeCause: rs.Annotations["kubernetes.io/change-cause"], Current: rs.Annotations[revisionAnnotation] == current, Template: templateYAML(rs)}
		for _, c := range rs.Spec.Template.Spec.Containers {
			r.Images = append(r.Images, c.Image)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out, nil
}

// UndoResult carries the template diff of a rollback.
type UndoResult struct {
	DryRun   bool   `json:"dryRun"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
}

// RolloutUndo restores the pod template of revision, like `kubectl rollout undo --to-revision`.
func RolloutUndo(ctx context.Context, s *cluster.Session, namespace, name string, revision int64, dryRun bool) (*UndoResult, error) {
	if err := s.CheckWritable(); err != nil {
		return nil, err
	}
	var res *UndoResult
	// The deployment controller bumps resourceVersion with status updates. A rollback replaces the
	// whole pod template by definition, so re-reading and retrying on conflict is safe.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		r, err := undoOnce(ctx, s, namespace, name, revision, dryRun)
		res = r
		return err
	})
	return res, err
}

func undoOnce(ctx context.Context, s *cluster.Session, namespace, name string, revision int64, dryRun bool) (*UndoResult, error) {
	d, err := s.Typed.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	rss, err := ownedReplicaSets(ctx, s, d)
	if err != nil {
		return nil, err
	}
	var target *appsv1.ReplicaSet
	for i := range rss {
		if rss[i].Annotations[revisionAnnotation] == strconv.FormatInt(revision, 10) {
			target = &rss[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("revision %d not found", revision)
	}
	curTpl, _ := yaml.Marshal(d.Spec.Template)
	tpl := target.Spec.Template.DeepCopy()
	delete(tpl.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	d.Spec.Template = *tpl
	opts := metav1.UpdateOptions{FieldManager: fieldManager}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	// Update (not patch) keeps optimistic concurrency on the resourceVersion we just read.
	out, err := s.Typed.AppsV1().Deployments(namespace).Update(ctx, d, opts)
	if err != nil {
		return nil, err
	}
	newTpl, _ := yaml.Marshal(out.Spec.Template)
	return &UndoResult{DryRun: dryRun, Current: string(curTpl), Proposed: string(newTpl)}, nil
}
