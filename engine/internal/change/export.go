package change

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// exportable is the final object as a manifest to commit (M7 "Exportar proposta"): the same object
// as After without what the API server owns (uid, resourceVersion, generation, creationTimestamp,
// revision annotations), so it applies cleanly through GitOps.
func exportable(u *unstructured.Unstructured) *unstructured.Unstructured {
	c := u.DeepCopy()
	for _, f := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "selfLink", "managedFields"} {
		unstructured.RemoveNestedField(c.Object, "metadata", f)
	}
	unstructured.RemoveNestedField(c.Object, "status")
	if ann := c.GetAnnotations(); ann != nil {
		for _, k := range []string{"deployment.kubernetes.io/revision", "kubectl.kubernetes.io/last-applied-configuration"} {
			delete(ann, k)
		}
		if len(ann) == 0 {
			ann = nil
		}
		c.SetAnnotations(ann)
	}
	return c
}
