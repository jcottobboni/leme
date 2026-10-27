#!/usr/bin/env bash
# Simulates load in the kind test cluster (and only there): 5 kwok fake nodes plus a Deployment
# with N fake pods (default 5000). Usage: scripts/kwok-load.sh [N]
set -euo pipefail
export KUBECONFIG="${DEV_KUBECONFIG:-$HOME/.kube/leme-dev/kubeconfig}"
kk() { kubectl --context "kind-${KIND_NAME:-leme-dev}" "$@"; }
# Applies a release manifest only if it matches the SHA-256 pinned here.
apply_pinned() {
  local f; f="$(mktemp)"
  curl -fsSL "$1" -o "$f"
  echo "$2  $f" | sha256sum --check --strict --quiet - || { echo "checksum mismatch: $1" >&2; rm -f "$f"; exit 1; }
  kk apply -f "$f" >/dev/null; rm -f "$f"
}
apply_pinned https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/kwok.yaml a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405
apply_pinned https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0/stage-fast.yaml 2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24
kk -n kube-system rollout status deploy/kwok-controller --timeout=120s
for i in 0 1 2 3 4; do
cat <<Y | kk apply -f - >/dev/null
apiVersion: v1
kind: Node
metadata:
  name: kwok-node-$i
  annotations: {node.alpha.kubernetes.io/ttl: "0", kwok.x-k8s.io/node: fake}
  labels: {type: kwok, kubernetes.io/role: agent, kubernetes.io/hostname: kwok-node-$i}
spec:
  taints: [{key: kwok.x-k8s.io/node, value: fake, effect: NoSchedule}]
status:
  allocatable: {cpu: "64", memory: 256Gi, pods: "1100"}
  capacity: {cpu: "64", memory: 256Gi, pods: "1100"}
Y
done
kk create ns scale >/dev/null 2>&1 || true
cat <<Y | kk apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata: {name: fake-load, namespace: scale}
spec:
  replicas: ${1:-5000}
  selector: {matchLabels: {app: fake-load}}
  template:
    metadata: {labels: {app: fake-load}}
    spec:
      nodeSelector: {type: kwok}
      tolerations: [{key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}]
      containers: [{name: app, image: fake-image}]
Y
[ "${1:-5000}" = 0 ] && exit 0
sleep 90
kk get pods -n scale --no-headers | awk '{print $3}' | sort | uniq -c
