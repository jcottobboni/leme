package kinds

import (
	"reflect"
	"testing"
)

func TestBuiltin(t *testing.T) {
	for g, want := range map[string]bool{
		"": true, "apps": true, "batch": true, "autoscaling": true, "policy": true,
		"networking.k8s.io": true, "gateway.networking.k8s.io": true, "rbac.authorization.k8s.io": true,
		"apps.openshift.io": true, "route.openshift.io": true,
		"cluster.x-k8s.io": false, "longhorn.io": false, "networking.istio.io": false, "serving.knative.dev": false,
		"batch.volcano.sh": false, "argoproj.io": false, "x-k8s.io": false,
	} {
		if got := Builtin(g); got != want {
			t.Errorf("Builtin(%q) = %v, want %v", g, got, want)
		}
	}
}

func TestGroupOf(t *testing.T) {
	for av, want := range map[string]string{"v1": "", "apps/v1": "apps", "longhorn.io/v1beta2": "longhorn.io", "": ""} {
		if got := GroupOf(av); got != want {
			t.Errorf("GroupOf(%q) = %q, want %q", av, got, want)
		}
	}
}

func TestPodTemplatePath(t *testing.T) {
	cases := []struct {
		group, kind string
		want        []string
	}{
		{"", "Pod", []string{"spec"}},
		{"apps", "Deployment", []string{"spec", "template", "spec"}},
		{"batch", "CronJob", []string{"spec", "jobTemplate", "spec", "template", "spec"}},
		{"batch", "Job", []string{"spec", "template", "spec"}},
		{"", "ReplicationController", []string{"spec", "template", "spec"}},
		{"apps.openshift.io", "DeploymentConfig", []string{"spec", "template", "spec"}},
		// CRD homonyms are not workloads.
		{"batch.volcano.sh", "Job", nil},
		{"argoproj.io", "Deployment", nil},
		{"apps", "Pod", nil},
		{"", "Service", nil},
	}
	for _, c := range cases {
		if got := PodTemplatePath(c.group, c.kind); !reflect.DeepEqual(got, c.want) {
			t.Errorf("PodTemplatePath(%q, %q) = %v, want %v", c.group, c.kind, got, c.want)
		}
		if IsWorkload(c.group, c.kind) != (c.want != nil) {
			t.Errorf("IsWorkload(%q, %q) disagrees with PodTemplatePath", c.group, c.kind)
		}
	}
	if got := PodTemplateMetaPath("batch", "CronJob"); !reflect.DeepEqual(got, []string{"spec", "jobTemplate", "spec", "template", "metadata"}) {
		t.Errorf("PodTemplateMetaPath(CronJob) = %v", got)
	}
	if got := PodTemplateMetaPath("", "Pod"); got != nil {
		t.Errorf("PodTemplateMetaPath(Pod) = %v, want nil", got)
	}
}

func TestWorkloadGroup(t *testing.T) {
	for kind := range workloadGroups {
		g, _ := WorkloadGroup(kind)
		if !IsWorkload(g, kind) {
			t.Errorf("WorkloadGroup(%q) = %q is not a workload group", kind, g)
		}
	}
	if _, ok := WorkloadGroup("Service"); ok {
		t.Error("Service is not a workload")
	}
}
