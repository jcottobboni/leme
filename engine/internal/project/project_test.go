package project

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func obj(t *testing.T, y string) *unstructured.Unstructured {
	t.Helper()
	m := map[string]any{}
	if err := yaml.Unmarshal([]byte(y), &m); err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: m}
}

func TestPodImagePullBackOff(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: broken, namespace: demo, uid: u1}
spec: {containers: [{name: c}]}
status:
  phase: Pending
  containerStatuses:
  - name: c
    ready: false
    restartCount: 0
    state: {waiting: {reason: ImagePullBackOff}}
`))
	if r.Cells["status"] != "ImagePullBackOff" || r.Health != "error" || r.Cells["ready"] != "0/1" {
		t.Fatalf("got %+v", r)
	}
	if c := r.Cells["containers"].([]ContainerState); len(c) != 1 || c[0].State != "error" {
		t.Fatalf("containers %+v", c)
	}
}

func TestPodContainerStates(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: p, namespace: demo, uid: u3}
spec:
  initContainers: [{name: setup}]
  containers: [{name: app}, {name: side}]
status:
  phase: Pending
  initContainerStatuses:
  - {name: setup, ready: false, restartCount: 0, state: {running: {}}}
  containerStatuses:
  - {name: app, ready: false, restartCount: 0, state: {waiting: {reason: PodInitializing}}}
`))
	c := r.Cells["containers"].([]ContainerState)
	want := []ContainerState{{"setup", "init"}, {"app", "waiting"}, {"side", "waiting"}}
	if len(c) != len(want) {
		t.Fatalf("got %+v", c)
	}
	for i := range want {
		if c[i] != want[i] {
			t.Fatalf("got %+v, want %+v", c, want)
		}
	}
}

func TestPodRunning(t *testing.T) {
	r := Project(obj(t, `
kind: Pod
metadata: {name: web, namespace: demo, uid: u2}
spec: {containers: [{name: a}, {name: b}], nodeName: n1}
status:
  phase: Running
  containerStatuses:
  - {name: a, ready: true, restartCount: 2, state: {running: {}}}
  - {name: b, ready: true, restartCount: 1, state: {running: {}}}
`))
	if r.Cells["status"] != "Running" || r.Health != "ok" || r.Cells["restarts"] != int64(3) || r.Cells["ready"] != "2/2" {
		t.Fatalf("got %+v", r)
	}
}

func TestWorkloadStates(t *testing.T) {
	cases := []struct{ y, state, health string }{
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: a, uid: a, generation: 2}, spec: {replicas: 3}, status: {observedGeneration: 2, readyReplicas: 3, updatedReplicas: 3, availableReplicas: 3,
			conditions: [{type: Progressing, status: "True", reason: NewReplicaSetAvailable}]}}`, StateAvailable, "ok"},
		// Just created: the controller has not observed it yet.
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: b, uid: b, generation: 1}, spec: {replicas: 1}, status: {}}`, StateProgressing, "warn"},
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: c, uid: c, generation: 3}, spec: {replicas: 3}, status: {observedGeneration: 3, readyReplicas: 1, updatedReplicas: 3, availableReplicas: 1,
			conditions: [{type: Progressing, status: "True", reason: ReplicaSetUpdated}]}}`, StateProgressing, "warn"},
		// Rollout finished, then pods stopped being ready.
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: c2, uid: c2, generation: 3}, spec: {replicas: 3}, status: {observedGeneration: 3, replicas: 3, readyReplicas: 1, updatedReplicas: 3, availableReplicas: 1,
			conditions: [{type: Progressing, status: "True", reason: NewReplicaSetAvailable}]}}`, StateDegraded, "warn"},
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: c3, uid: c3, generation: 3}, spec: {replicas: 2}, status: {observedGeneration: 3, replicas: 2, updatedReplicas: 2,
			conditions: [{type: Progressing, status: "True", reason: NewReplicaSetAvailable}]}}`, StateDegraded, "error"},
		// demo/broken while its rollout runs: the only ready pod is the old one, the new one fails.
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: d0, uid: d0, generation: 23}, spec: {replicas: 1}, status: {observedGeneration: 23, replicas: 2, updatedReplicas: 1, readyReplicas: 1, availableReplicas: 1, unavailableReplicas: 1,
			conditions: [{type: Available, status: "True", reason: MinimumReplicasAvailable}, {type: Progressing, status: "True", reason: ReplicaSetUpdated}]}}`, StateProgressing, "warn"},
		// The demo/broken fixture (nginx:does-not-exist) after progressDeadlineSeconds.
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: d, uid: d, generation: 1}, spec: {replicas: 1}, status: {observedGeneration: 1, replicas: 1, updatedReplicas: 1, unavailableReplicas: 1,
			conditions: [{type: Available, status: "False", reason: MinimumReplicasUnavailable}, {type: Progressing, status: "False", reason: ProgressDeadlineExceeded, message: "ReplicaSet \"broken-x\" has timed out progressing."}]}}`, StateStuck, "error"},
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: e, uid: e}, spec: {replicas: 0}, status: {}}`, StateNoReplicas, "neutral"},
		{`{apiVersion: apps/v1, kind: Deployment, metadata: {name: f, uid: f, generation: 4}, spec: {replicas: 2, paused: true}, status: {observedGeneration: 4, readyReplicas: 2, updatedReplicas: 1, availableReplicas: 2}}`, StatePaused, "warn"},
		{`{apiVersion: apps/v1, kind: ReplicaSet, metadata: {name: g, uid: g, generation: 1}, spec: {replicas: 2}, status: {observedGeneration: 1, replicas: 2, readyReplicas: 2, availableReplicas: 2}}`, StateAvailable, "ok"},
		{`{apiVersion: apps/v1, kind: ReplicaSet, metadata: {name: h, uid: h, generation: 1}, spec: {replicas: 2}, status: {observedGeneration: 1, replicas: 2,
			conditions: [{type: ReplicaFailure, status: "True", reason: FailedCreate, message: "exceeded quota"}]}}`, StateDegraded, "error"},
		{`{apiVersion: apps/v1, kind: StatefulSet, metadata: {name: i, uid: i, generation: 1}, spec: {replicas: 3}, status: {observedGeneration: 1, replicas: 2, readyReplicas: 2, availableReplicas: 2, updatedReplicas: 2}}`, StateProgressing, "warn"},
		{`{apiVersion: apps/v1, kind: DaemonSet, metadata: {name: j, uid: j, generation: 1}, status: {observedGeneration: 1, desiredNumberScheduled: 3, numberReady: 3, numberAvailable: 3, updatedNumberScheduled: 3}}`, StateAvailable, "ok"},
		{`{apiVersion: apps/v1, kind: DaemonSet, metadata: {name: k, uid: k, generation: 2}, status: {observedGeneration: 2, desiredNumberScheduled: 3, numberReady: 3, numberAvailable: 3, updatedNumberScheduled: 1}}`, StateProgressing, "warn"},
		{`{apiVersion: apps.openshift.io/v1, kind: DeploymentConfig, metadata: {name: l, uid: l, generation: 1}, spec: {replicas: 1}, status: {observedGeneration: 1, latestVersion: 3,
			conditions: [{type: Progressing, status: "False", reason: RolloutCancelled}]}}`, StateStuck, "error"},
	}
	for _, c := range cases {
		r := Project(obj(t, c.y))
		if r.Cells["status"] != c.state || r.Health != c.health {
			t.Errorf("%s: got %v/%s, want %s/%s", r.Name, r.Cells["status"], r.Health, c.state, c.health)
		}
	}
	// A CRD named like a workload gets no workload state.
	if _, ok := StateOf(obj(t, `{apiVersion: argoproj.io/v1alpha1, kind: Deployment, metadata: {name: x}}`)); ok {
		t.Error("StateOf accepted a CRD homonym")
	}
}

func TestRouteURL(t *testing.T) {
	mk := func(api, host, path string, tls bool) *unstructured.Unstructured {
		spec := map[string]any{"host": host}
		if path != "" {
			spec["path"] = path
		}
		if tls {
			spec["tls"] = map[string]any{"termination": "edge"}
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": api, "kind": "Route", "spec": spec}}
	}
	cases := []struct {
		u    *unstructured.Unstructured
		want string
	}{
		{mk("route.openshift.io/v1", "web.apps.example.com", "/app", true), "https://web.apps.example.com/app"},
		{mk("route.openshift.io/v1", "web.apps.example.com", "", false), "http://web.apps.example.com"},
		{mk("route.openshift.io/v1", "bank.com@evil.com", "", true), ""},
		{mk("route.openshift.io/v1", "evil.com/x y", "", true), ""},
		{mk("route.openshift.io/v1", "ok.example.com", " javascript:alert(1)", true), "https://ok.example.com"},
		{mk("serving.knative.dev/v1", "web.example.com", "", true), ""},
	}
	for _, c := range cases {
		if got := RouteURL(c.u); got != c.want {
			t.Errorf("%v: got %q want %q", c.u.Object["spec"], got, c.want)
		}
	}
}

func TestAppOf(t *testing.T) {
	cases := []struct {
		labels, ann map[string]string
		want        string
	}{
		{map[string]string{"app.kubernetes.io/part-of": "shop", "app": "web"}, nil, "shop"},
		{map[string]string{"app": "web"}, map[string]string{"meta.helm.sh/release-name": "rel"}, "rel"},
		{map[string]string{"app.kubernetes.io/instance": "i", "app": "web"}, nil, "i"},
		{map[string]string{"k8s-app": "kube-dns"}, nil, "kube-dns"},
		{nil, nil, ""},
	}
	for _, c := range cases {
		if got := AppOf(c.labels, c.ann); got != c.want {
			t.Errorf("AppOf(%v, %v) = %q, want %q", c.labels, c.ann, got, c.want)
		}
	}
}

func TestCronJobState(t *testing.T) {
	r := Project(obj(t, `{kind: CronJob, metadata: {name: c, namespace: demo, uid: c}, spec: {schedule: "*/5 * * * *", suspend: true}}`))
	if r.Cells["state"] != "Suspended" || r.Health != "warn" {
		t.Fatalf("got %+v", r)
	}
	if r := Project(obj(t, `{kind: CronJob, metadata: {name: d, uid: d}, spec: {schedule: "@daily"}}`)); r.Cells["state"] != "Active" || r.Health != "ok" {
		t.Fatalf("got %+v", r)
	}
}

func TestMoreProjectors(t *testing.T) {
	cases := []struct {
		y      string
		key    string
		want   any
		health string
	}{
		{`{kind: PodDisruptionBudget, metadata: {name: p, uid: p}, spec: {minAvailable: 1}, status: {disruptionsAllowed: 0}}`, "minAvailable", "1", "warn"},
		{`{kind: ResourceQuota, metadata: {name: q, uid: q}, status: {hard: {pods: "10", cpu: "4"}, used: {pods: "3"}}}`, "usage", "cpu: 0/4, pods: 3/10", "neutral"},
		{`{kind: CustomResourceDefinition, metadata: {name: x, uid: x}, spec: {group: a.io, scope: Namespaced, versions: [{name: v1, served: true}, {name: v0, served: false}]}}`, "versions", "v1", "neutral"},
		{`{kind: ClusterRoleBinding, metadata: {name: b, uid: b}, roleRef: {kind: ClusterRole, name: admin}, subjects: [{kind: User, name: ana}, {kind: Group, name: ops}]}`, "subjects", "User/ana +1", "neutral"},
		{`{kind: StorageClass, metadata: {name: s, uid: s, annotations: {storageclass.kubernetes.io/is-default-class: "true"}}, provisioner: rancher.io/local-path}`, "default", "Sim", "neutral"},
		{`{kind: PriorityClass, metadata: {name: pc, uid: pc}, value: 1000}`, "value", int64(1000), "neutral"},
		{`{kind: HorizontalPodAutoscaler, metadata: {name: h, uid: h}, spec: {maxReplicas: 5, metrics: [{type: Resource, resource: {name: cpu, target: {type: Utilization, averageUtilization: 80}}}]}, status: {currentReplicas: 2, currentMetrics: [{type: Resource, resource: {name: cpu, current: {averageUtilization: 35}}}]}}`, "metrics", "cpu: 35%/80%", "ok"},
		{`{kind: Endpoints, metadata: {name: e, uid: e}, subsets: [{addresses: [{ip: 10.0.0.1}], ports: [{port: 80}]}]}`, "addresses", "10.0.0.1:80", "neutral"},
		{`{kind: VolumeAttachment, metadata: {name: va, uid: va}, spec: {attacher: csi, nodeName: n1, source: {persistentVolumeName: pv1}}, status: {attached: true}}`, "attached", "True", "ok"},
	}
	for _, c := range cases {
		r := Project(obj(t, c.y))
		if r.Cells[c.key] != c.want || r.Health != c.health {
			t.Errorf("%s: %s=%#v health=%s, want %#v %s", c.y, c.key, r.Cells[c.key], r.Health, c.want, c.health)
		}
	}
	if cs := Columns("rbac.authorization.k8s.io", "ClusterRoleBinding", false); cs[1].Key == "namespace" {
		t.Errorf("cluster-scoped kind got a namespace column: %+v", cs)
	}
}

func TestCRDWithBuiltinKindNameIsNotProjectedAsBuiltin(t *testing.T) {
	crd := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "longhorn.io/v1beta2", "kind": "Node",
		"metadata": map[string]any{"name": "con4", "namespace": "longhorn-system"},
		"status":   map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
	}}
	r := Project(crd)
	if _, ok := r.Cells["roles"]; ok {
		t.Fatalf("CRD Node got the built-in Node projection: %+v", r.Cells)
	}
	if cs := Columns("longhorn.io", "Node", true); len(cs) != 3 || cs[1].Key != "namespace" {
		t.Fatalf("CRD Node must get the generic columns: %+v", cs)
	}
	if cs := Columns("", "Node", false); len(cs) <= 2 {
		t.Fatalf("built-in Node keeps its columns: %+v", cs)
	}
}
