#!/usr/bin/env bash
# M5 map-webgl scale fixture, for the kind test cluster ONLY (kind-leme-dev, ~/.kube/leme-dev/kubeconfig).
#   up    20 kwok fake nodes m5-mapwebgl-kwok-N + 20,000 bare pods (no controller = 20,000 map workloads)
#         in namespace m5-mapwebgl-scale. Idempotent.
#   down  deletes that namespace and those nodes, nothing else.
# The kwok controller comes only from scripts/kwok-load.sh (v0.8.0, pinned SHA-256); it is installed by it
# (with N=0) only when absent. Usage: kwok-20k.sh up|down   (PODS=<n> to change the count)
set -euo pipefail
KCFG="$HOME/.kube/leme-dev/kubeconfig"
CTX=kind-leme-dev
NS=m5-mapwebgl-scale
NODES=20
PODS="${PODS:-20000}"
CHUNK=500
LABEL=leme.dev/m5-fixture=mapwebgl
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
kk() { kubectl --kubeconfig "$KCFG" --context "$CTX" "$@"; }

nodes() {
  local i
  for ((i = 0; i < NODES; i++)); do
    cat <<Y
---
apiVersion: v1
kind: Node
metadata:
  name: m5-mapwebgl-kwok-$i
  annotations: {node.alpha.kubernetes.io/ttl: "0", kwok.x-k8s.io/node: fake}
  labels: {type: m5-mapwebgl-kwok, leme.dev/m5-fixture: mapwebgl, kubernetes.io/hostname: m5-mapwebgl-kwok-$i}
spec:
  taints: [{key: kwok.x-k8s.io/node, value: fake, effect: NoSchedule}]
status:
  allocatable: {cpu: "64", memory: 256Gi, pods: "1100"}
  capacity: {cpu: "64", memory: 256Gi, pods: "1100"}
Y
  done
}

# Pods bound straight to the fixture nodes (no scheduler); 400 apps of 50 (label app). kwok marks them Running.
pods() {
  local from=$1 to=$2 i
  for ((i = from; i < to; i++)); do
    cat <<Y
---
apiVersion: v1
kind: Pod
metadata:
  name: m5-mapwebgl-p-$i
  namespace: $NS
  labels: {app: m5-mapwebgl-app-$((i / 50)), leme.dev/m5-fixture: mapwebgl}
spec:
  nodeName: m5-mapwebgl-kwok-$((i % NODES))
  tolerations: [{key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule}]
  containers: [{name: app, image: fake-image}]
Y
  done
}

running() { kk -n "$NS" get pods --no-headers 2>/dev/null | wc -l; }

up() {
  if ! kk -n kube-system get deploy kwok-controller >/dev/null 2>&1; then
    DEV_KUBECONFIG="$KCFG" KIND_NAME=leme-dev "$ROOT/scripts/kwok-load.sh" 0
  fi
  nodes | kk apply -f - >/dev/null
  kk get ns "$NS" >/dev/null 2>&1 || kk create ns "$NS" >/dev/null
  kk label ns "$NS" "$LABEL" --overwrite >/dev/null
  local have
  have=$(running)
  if [ "$have" -lt "$PODS" ]; then
    echo "creating pods ($have/$PODS present)…"
    local from
    for ((from = 0; from < PODS; from += CHUNK)); do
      echo "$from $((from + CHUNK > PODS ? PODS : from + CHUNK))"
    done | xargs -P 6 -n 2 bash -c 'source /dev/stdin <<<"$FN"; pods "$0" "$1" | kk create -f - 2>&1 | grep -v -e "created" -e "AlreadyExists" || true' \
      || true
  fi
  local t=0
  while :; do
    local n
    n=$(kk -n "$NS" get pods --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l)
    echo "running $n/$PODS"
    [ "$n" -ge $((PODS * 98 / 100)) ] && break
    t=$((t + 10))
    [ $t -gt 900 ] && { echo "timeout waiting for kwok pods" >&2; exit 1; }
    sleep 10
  done
}

down() {
  kk delete ns "$NS" --ignore-not-found --wait=true --timeout=15m
  kk delete node -l "$LABEL" --ignore-not-found
}

export FN
FN="$(declare -f kk pods); KCFG='$KCFG'; CTX='$CTX'; NS='$NS'; NODES=$NODES"
case "${1:-}" in
  up) up ;;
  down) down ;;
  *) echo "usage: $0 up|down" >&2; exit 2 ;;
esac
