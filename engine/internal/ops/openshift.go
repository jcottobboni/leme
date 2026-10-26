package ops

import (
	"context"
	"errors"
	"io"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
)

var (
	dcGVR = schema.GroupVersionResource{Group: "apps.openshift.io", Version: "v1", Resource: "deploymentconfigs"}
	bcGVR = schema.GroupVersionResource{Group: "build.openshift.io", Version: "v1", Resource: "buildconfigs"}
)

var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

func validNames(ns, name string) error {
	if !dns1123.MatchString(ns) || !dns1123.MatchString(name) {
		return errors.New("namespace e nome devem ser nomes Kubernetes válidos")
	}
	return nil
}

// RolloutLatest starts a new DeploymentConfig rollout, like `oc rollout latest`.
func RolloutLatest(ctx context.Context, s *cluster.Session, ns, name string) (int64, error) {
	if err := s.CheckWritable(); err != nil {
		return 0, err
	}
	if err := validNames(ns, name); err != nil {
		return 0, err
	}
	req := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps.openshift.io/v1", "kind": "DeploymentRequest", "metadata": map[string]any{"name": name}, "name": name, "latest": true, "force": true,
	}}
	out, err := s.Dynamic.Resource(dcGVR).Namespace(ns).Create(ctx, req, metav1.CreateOptions{FieldManager: fieldManager}, "instantiate")
	if err != nil {
		return 0, err
	}
	v, _, _ := unstructured.NestedInt64(out.Object, "status", "latestVersion")
	return v, nil
}

// StartBuild instantiates a BuildConfig, like `oc start-build`. It returns the new build name.
func StartBuild(ctx context.Context, s *cluster.Session, ns, name string) (string, error) {
	if err := s.CheckWritable(); err != nil {
		return "", err
	}
	if err := validNames(ns, name); err != nil {
		return "", err
	}
	req := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "build.openshift.io/v1", "kind": "BuildRequest", "metadata": map[string]any{"name": name},
	}}
	out, err := s.Dynamic.Resource(bcGVR).Namespace(ns).Create(ctx, req, metav1.CreateOptions{FieldManager: fieldManager}, "instantiate")
	if err != nil {
		return "", err
	}
	return out.GetName(), nil
}

// StreamBuildLogs copies a build log (follow) to w.
func StreamBuildLogs(ctx context.Context, s *cluster.Session, ns, name string, follow bool, w io.Writer) error {
	if err := validNames(ns, name); err != nil {
		return err
	}
	r := s.Typed.CoreV1().RESTClient().Get().AbsPath("/apis/build.openshift.io/v1/namespaces", ns, "builds", name, "log")
	if follow {
		r = r.Param("follow", "true")
	}
	stream, err := r.Stream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = io.Copy(w, stream)
	return err
}
