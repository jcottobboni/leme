package api

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/jcottobboni/leme/engine/internal/cluster"
	"github.com/jcottobboni/leme/engine/internal/kinds"
	"github.com/jcottobboni/leme/engine/internal/project"
)

var crdGVR = k8sschema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

type printerKey struct{ group, version, resource string }

// printerTTL bounds how long a CRD's printer columns are reused before the CRD is read again.
const printerTTL = 5 * time.Minute

// printerFor returns the printer columns of a custom resource (nil for built-in kinds, CRDs without
// printer columns, aggregated APIs, or when the CRD cannot be read). The CRD comes from a running
// CRD informer when there is one, otherwise from one GET kept in the session cache.
func printerFor(ctx context.Context, sess *cluster.Session, res cluster.Resource) *project.Printer {
	if kinds.Builtin(res.Group) {
		return nil
	}
	p, _ := cluster.Cached(sess, printerKey{res.Group, res.Version, res.Resource}, printerTTL, false, func() (*project.Printer, error) {
		crd, err := getCRD(ctx, sess, res.Resource+"."+res.Group)
		if err != nil {
			return nil, nil // no permission or not a CRD: keep the generic columns, do not retry for the TTL
		}
		return project.NewPrinter(project.PrinterColumns(crd, res.Version), res.Namespaced), nil
	})
	return p
}

func getCRD(ctx context.Context, sess *cluster.Session, name string) (*unstructured.Unstructured, error) {
	if sess.Informers != nil {
		if u, ok := sess.Informers.Get(cluster.Key{GVR: crdGVR}, "", name); ok {
			return u, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return sess.Dynamic.Resource(crdGVR).Get(ctx, name, metav1.GetOptions{})
}
