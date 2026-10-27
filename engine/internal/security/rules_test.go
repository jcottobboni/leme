package security

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func obj(t *testing.T, doc string) *unstructured.Unstructured {
	t.Helper()
	j, err := yaml.YAMLToJSON([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(j); err != nil {
		t.Fatal(err)
	}
	return u
}

const insecure = `apiVersion: apps/v1
kind: Deployment
metadata: {name: insecure, namespace: demo}
spec:
  template:
    spec:
      hostNetwork: true
      volumes:
        - {name: sock, hostPath: {path: /var/run/docker.sock}}
      containers:
        - name: app
          image: busybox
          securityContext:
            privileged: true
            runAsUser: 0
            capabilities: {add: [SYS_ADMIN]}
          ports: [{containerPort: 80, hostPort: 8080}]
`

const hardened = `apiVersion: apps/v1
kind: Deployment
metadata: {name: good, namespace: demo}
spec:
  template:
    spec:
      automountServiceAccountToken: false
      securityContext: {runAsNonRoot: true, runAsUser: 1000, seccompProfile: {type: RuntimeDefault}}
      containers:
        - name: app
          image: registry.example.com/app:1.2.3
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: {drop: [ALL]}
          resources: {requests: {cpu: 10m, memory: 32Mi}, limits: {memory: 64Mi}}
          readinessProbe: {httpGet: {path: /, port: 80}}
          livenessProbe: {httpGet: {path: /, port: 80}}
`

func ids(fs []Finding) map[string]string {
	m := map[string]string{}
	for _, f := range fs {
		m[f.ID] = f.Severity
	}
	return m
}

func TestInsecureWorkload(t *testing.T) {
	u := obj(t, insecure)
	spec, ok := PodSpec(u)
	if !ok {
		t.Fatal("pod spec not found")
	}
	got := ids(CheckPodSpec(RefOf(u), spec, false))
	want := map[string]string{"privileged": Critical, "host-path": Critical, "host-network": High, "run-as-root": High, "dangerous-capability": High, "host-port": Medium, "image-tag": Medium}
	for id, sev := range want {
		if got[id] != sev {
			t.Errorf("%s: got %q want %q", id, got[id], sev)
		}
	}
	if _, ok := got["privilege-escalation"]; ok {
		t.Error("privileged already implies escalation; do not double count")
	}
}

func TestHardenedWorkload(t *testing.T) {
	u := obj(t, hardened)
	spec, _ := PodSpec(u)
	if fs := CheckPodSpec(RefOf(u), spec, false); len(fs) != 0 {
		t.Fatalf("hardened workload must be clean, got %+v", fs)
	}
}

func TestCRDHomonymHasNoPodSpec(t *testing.T) {
	u := obj(t, strings.Replace(insecure, "apiVersion: apps/v1", "apiVersion: example.io/v1", 1))
	if _, ok := PodSpec(u); ok {
		t.Fatal("a CRD named Deployment must not be read as a workload")
	}
}

func TestImageUnpinned(t *testing.T) {
	for img, want := range map[string]bool{"nginx": true, "nginx:latest": true, "localhost:5000/app": true, "localhost:5000/app:1.0": false, "nginx:1.27-alpine": false, "nginx@sha256:abc": false} {
		if got := imageUnpinned(img); got != want {
			t.Errorf("%s: got %v", img, got)
		}
	}
}

func TestScanUsesRunningPodAndNamespaceRules(t *testing.T) {
	dep := obj(t, `apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: demo}
spec:
  template:
    spec:
      containers: [{name: app, image: nginx:1.27}]
`)
	rs := obj(t, `apiVersion: apps/v1
kind: ReplicaSet
metadata: {name: web-abc, namespace: demo, ownerReferences: [{apiVersion: apps/v1, kind: Deployment, name: web, uid: d, controller: true}]}
spec: {template: {spec: {containers: [{name: app, image: nginx:1.27}]}}}
`)
	// The running pod got runAsUser from admission (e.g. OpenShift SCC): effective spec wins.
	pod := obj(t, `apiVersion: v1
kind: Pod
metadata: {name: web-abc-x1, namespace: demo, ownerReferences: [{apiVersion: apps/v1, kind: ReplicaSet, name: web-abc, uid: r, controller: true}]}
spec:
  containers: [{name: app, image: nginx:1.27, securityContext: {runAsUser: 1000700000}}]
status: {phase: Running}
`)
	r := Scan(Input{Objects: map[string][]*unstructured.Unstructured{"apps/deployments": {dep}, "apps/replicasets": {rs}, "/pods": {pod}}}, "demo")
	if len(r.Workloads) != 1 || r.Workloads[0].Kind != "Deployment" {
		t.Fatalf("only the Deployment is a top-level workload: %+v", r.Workloads)
	}
	f := ids(r.Findings)
	if _, ok := f["run-as-non-root"]; ok {
		t.Error("effective pod spec sets a non-zero UID; run-as-non-root must not fire")
	}
	if f["no-network-policy"] != Medium || len(r.NamespacesWithoutNetpol) != 1 {
		t.Errorf("namespace without policy not reported: %v %v", f, r.NamespacesWithoutNetpol)
	}
	for _, x := range r.Findings {
		if x.Kind == "Deployment" && !x.Effective {
			t.Errorf("finding %s should be marked effective", x.ID)
		}
	}
}
