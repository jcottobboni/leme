package project

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func pvc(u *unstructured.Unstructured, r *Row) {
	p := str(u, "status", "phase")
	r.Cells["status"] = p
	r.Cells["volume"] = str(u, "spec", "volumeName")
	r.Cells["capacity"] = str(u, "status", "capacity", "storage")
	r.Cells["storageClass"] = str(u, "spec", "storageClassName")
	r.Health = map[string]string{"Bound": "ok", "Pending": "warn", "Lost": "error"}[p]
	if r.Health == "" {
		r.Health = "neutral"
	}
}

func pv(u *unstructured.Unstructured, r *Row) {
	p := str(u, "status", "phase")
	r.Cells["status"] = p
	r.Cells["capacity"] = str(u, "spec", "capacity", "storage")
	if ns, n := str(u, "spec", "claimRef", "namespace"), str(u, "spec", "claimRef", "name"); n != "" {
		r.Cells["claim"] = ns + "/" + n
	}
	r.Cells["storageClass"] = str(u, "spec", "storageClassName")
	r.Health = map[string]string{"Bound": "ok", "Available": "neutral", "Released": "warn", "Failed": "error"}[p]
	if r.Health == "" {
		r.Health = "neutral"
	}
}

func storageClass(u *unstructured.Unstructured, r *Row) {
	r.Cells["provisioner"] = str(u, "provisioner")
	r.Cells["reclaimPolicy"] = str(u, "reclaimPolicy")
	r.Cells["volumeBindingMode"] = str(u, "volumeBindingMode")
	ann := u.GetAnnotations()
	if ann["storageclass.kubernetes.io/is-default-class"] == "true" || ann["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
		r.Cells["default"] = "Sim"
	}
}

func volumeAttachment(u *unstructured.Unstructured, r *Row) {
	r.Cells["attacher"] = str(u, "spec", "attacher")
	r.Cells["pv"] = str(u, "spec", "source", "persistentVolumeName")
	r.Cells["node"] = str(u, "spec", "nodeName")
	if b, _, _ := unstructured.NestedBool(u.Object, "status", "attached"); b {
		r.Cells["attached"], r.Health = "True", "ok"
	} else {
		r.Cells["attached"], r.Health = "False", "warn"
	}
}
