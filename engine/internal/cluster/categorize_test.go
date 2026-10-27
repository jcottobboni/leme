package cluster

import "testing"

func TestCategorizeOnlyBuiltinGroupsByKind(t *testing.T) {
	for _, c := range []struct{ group, kind, want string }{
		{"", "Node", "Cluster"},
		{"longhorn.io", "Node", "Custom Resources"}, // a CRD reusing a built-in kind name
		{"networking.istio.io", "Gateway", "Custom Resources"},
		{"gateway.networking.k8s.io", "Gateway", "Network"},
		{"apps", "Deployment", "Workloads"},
		{"route.openshift.io", "Route", "Network"},
		{"snapshot.storage.k8s.io", "VolumeSnapshot", "Storage"},
		{"cluster.x-k8s.io", "Cluster", "Custom Resources"},
		{"apiregistration.k8s.io", "APIService", "Cluster"},
		{"events.k8s.io", "Event", "Cluster"},
		{"apps", "ControllerRevision", "Cluster"},
		{"batch.volcano.sh", "Job", "Custom Resources"},
		{"serving.knative.dev", "Route", "Custom Resources"},
		{"config.openshift.io", "ClusterVersion", "Custom Resources"},
	} {
		if got := categorize(c.group, c.kind); got != c.want {
			t.Errorf("categorize(%q, %q) = %q, want %q", c.group, c.kind, got, c.want)
		}
	}
}
