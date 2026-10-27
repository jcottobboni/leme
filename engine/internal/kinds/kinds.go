// Package kinds is the single source of the rules that depend on an object's kind: which API groups
// are built in, where a workload keeps its pod template and which kinds are workloads.
//
// A CRD may reuse a built-in kind name (Longhorn's nodes.longhorn.io is kind Node, Knative has a
// Route, Volcano a Job, Istio a Gateway), so every per-kind rule must also check the group.
package kinds

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Builtin reports whether an API group is one of the Kubernetes or OpenShift built-in groups:
// the core group, apps, batch, autoscaling, policy, any *.k8s.io (except the *.x-k8s.io groups of
// SIG projects shipped as CRDs) and any *.openshift.io.
func Builtin(group string) bool {
	switch group {
	case "", "apps", "batch", "autoscaling", "policy":
		return true
	}
	return (strings.HasSuffix(group, ".k8s.io") && !strings.HasSuffix(group, ".x-k8s.io")) || strings.HasSuffix(group, ".openshift.io")
}

// GroupOf returns the group of an apiVersion ("apps/v1" -> "apps", "v1" -> "").
func GroupOf(apiVersion string) string {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
			return apiVersion[:i]
		}
		return ""
	}
	return gv.Group
}

// PodTemplatePath returns where the pod spec lives for a workload kind (or a Pod), nil for anything else.
func PodTemplatePath(group, kind string) []string {
	if !Builtin(group) {
		return nil
	}
	switch kind {
	case "Pod":
		if group == "" {
			return []string{"spec"}
		}
	case "CronJob":
		if group == "batch" {
			return []string{"spec", "jobTemplate", "spec", "template", "spec"}
		}
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet":
		if group == "apps" {
			return []string{"spec", "template", "spec"}
		}
	case "Job":
		if group == "batch" {
			return []string{"spec", "template", "spec"}
		}
	case "ReplicationController":
		if group == "" {
			return []string{"spec", "template", "spec"}
		}
	case "DeploymentConfig":
		if group == "apps.openshift.io" {
			return []string{"spec", "template", "spec"}
		}
	}
	return nil
}

// PodTemplateMetaPath is PodTemplatePath for the template metadata (labels, annotations) instead of
// the spec; nil for a Pod (its own metadata) and for anything that is not a workload.
func PodTemplateMetaPath(group, kind string) []string {
	p := PodTemplatePath(group, kind)
	if len(p) < 2 {
		return nil
	}
	out := append([]string(nil), p[:len(p)-1]...)
	return append(out, "metadata")
}

var workloadGroups = map[string]string{
	"Pod": "", "ReplicationController": "", "Deployment": "apps", "StatefulSet": "apps", "DaemonSet": "apps",
	"ReplicaSet": "apps", "Job": "batch", "CronJob": "batch", "DeploymentConfig": "apps.openshift.io",
}

// WorkloadGroup returns the built-in group of a workload kind ("Deployment" -> "apps"). APIs that
// historically took only a kind use it when the caller sends no group.
func WorkloadGroup(kind string) (string, bool) {
	g, ok := workloadGroups[kind]
	return g, ok
}

// IsWorkload reports kinds that run pods from a template (and Pod itself).
func IsWorkload(group, kind string) bool { return PodTemplatePath(group, kind) != nil }
