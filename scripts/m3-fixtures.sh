#!/usr/bin/env bash
# Prepares the M3 fixtures in the isolated kind cluster ONLY: OpenShift look-alike CRDs and objects,
# an insecure workload, an RBAC risk and a DeploymentConfig-owned ReplicationController.
# Usage: scripts/m3-fixtures.sh [apply|delete]
set -euo pipefail
cd "$(dirname "$0")/.."
kk() { kubectl --kubeconfig "${DEV_KUBECONFIG:-$HOME/.kube/leme-dev/kubeconfig}" --context kind-leme-dev "$@"; }
F=app/e2e/fixtures

if [ "${1:-apply}" = "delete" ]; then
  kk -n demo delete rc frontend-1 --ignore-not-found
  kk delete -f $F/m3-objects.yaml --ignore-not-found
  kk -n demo delete networkpolicy e2e-deny-all e2e-allow-client-web --ignore-not-found
  exit 0
fi

kk apply -f $F/openshift-crds.yaml >/dev/null
kk wait --for=condition=Established crd/routes.route.openshift.io crd/deploymentconfigs.apps.openshift.io \
  crd/buildconfigs.build.openshift.io crd/builds.build.openshift.io crd/imagestreams.image.openshift.io crd/projects.project.openshift.io --timeout=60s >/dev/null
kk apply -f $F/m3-objects.yaml >/dev/null
kk apply -f - >/dev/null <<EOF
apiVersion: project.openshift.io/v1
kind: Project
metadata: {name: demo, annotations: {openshift.io/display-name: "Leme demo"}}
EOF
dc=$(kk -n demo get deploymentconfig frontend -o jsonpath='{.metadata.uid}')
bc=$(kk -n demo get buildconfig frontend -o jsonpath='{.metadata.uid}')
kk apply -f - >/dev/null <<EOF
apiVersion: v1
kind: ReplicationController
metadata:
  name: frontend-1
  namespace: demo
  ownerReferences: [{apiVersion: apps.openshift.io/v1, kind: DeploymentConfig, name: frontend, uid: "$dc", controller: true}]
spec:
  replicas: 1
  selector: {deploymentconfig: frontend, deployment: frontend-1}
  template:
    metadata: {labels: {deploymentconfig: frontend, deployment: frontend-1}}
    spec:
      containers: [{name: nginx, image: "nginx:1.27-alpine"}]
---
apiVersion: build.openshift.io/v1
kind: Build
metadata:
  name: frontend-1
  namespace: demo
  labels: {openshift.io/build-config.name: frontend}
  ownerReferences: [{apiVersion: build.openshift.io/v1, kind: BuildConfig, name: frontend, uid: "$bc", controller: true}]
spec: {strategy: {type: Docker}}
EOF
kk -n demo patch build frontend-1 --subresource=status --type=merge -p '{"status":{"phase":"Complete","startTimestamp":"2026-10-25T10:00:00Z","completionTimestamp":"2026-10-25T10:02:30Z"}}' >/dev/null
kk -n demo patch deploymentconfig frontend --subresource=status --type=merge -p '{"status":{"latestVersion":1,"replicas":1,"readyReplicas":1,"availableReplicas":1,"updatedReplicas":1}}' >/dev/null
kk -n demo patch route web --subresource=status --type=merge -p '{"status":{"ingress":[{"host":"web-demo.apps.kind.local","conditions":[{"type":"Admitted","status":"True"}]}]}}' >/dev/null
kk -n demo patch buildconfig frontend --subresource=status --type=merge -p '{"status":{"lastVersion":1}}' >/dev/null
kk -n demo patch imagestream frontend --subresource=status --type=merge -p '{"status":{"dockerImageRepository":"image-registry.openshift-image-registry.svc:5000/demo/frontend","tags":[{"tag":"latest","items":[]}]}}' >/dev/null
kk -n demo wait --for=condition=Ready pod -l deploymentconfig=frontend --timeout=120s >/dev/null
kk -n demo wait --for=condition=Ready pod/client --timeout=120s >/dev/null
echo "m3 fixtures ready"
