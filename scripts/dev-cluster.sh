#!/usr/bin/env bash
# Creates the isolated kind cluster used by the e2e tests and installs the fixtures they expect:
# metrics-server, a small Prometheus, the "demo" namespace (healthy and broken workloads, a CronJob,
# a log generator, a ConfigMap and a Secret) and kwok fake nodes (no fake pods by default).
#
# It only ever talks to the kind cluster below, through its own kubeconfig file.
# Usage: scripts/dev-cluster.sh            (idempotent; re-run to restore the fixtures)
set -euo pipefail
cd "$(dirname "$0")/.."

NAME="${KIND_NAME:-leme-dev}"
export KUBECONFIG="${DEV_KUBECONFIG:-$HOME/.kube/leme-dev/kubeconfig}"
kk() { kubectl --context "kind-$NAME" "$@"; }

for bin in kind kubectl docker; do
  command -v "$bin" >/dev/null || { echo "missing: $bin" >&2; exit 1; }
done

if ! kind get clusters 2>/dev/null | grep -qx "$NAME"; then
  mkdir -p "$(dirname "$KUBECONFIG")"
  kind create cluster --name "$NAME" --kubeconfig "$KUBECONFIG"
fi

# Applies a release manifest only if it matches the SHA-256 pinned here.
apply_pinned() {
  local f; f="$(mktemp)"
  curl -fsSL "$1" -o "$f"
  echo "$2  $f" | sha256sum --check --strict --quiet - || { echo "checksum mismatch: $1" >&2; rm -f "$f"; exit 1; }
  kk apply -f "$f" >/dev/null; rm -f "$f"
}

echo "metrics-server"
if ! kk -n kube-system get deploy metrics-server >/dev/null 2>&1; then
  apply_pinned https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml 1cec29a5267809306a2c6ec74a3e449abbb705b4a8beed0c8a1963910f72c79b
  # kind kubelets use self-signed certificates.
  kk -n kube-system patch deploy metrics-server --type=json \
    -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' >/dev/null
fi

echo "prometheus"
kk apply -f app/e2e/fixtures/prometheus.yaml >/dev/null

echo "demo namespace"
kk create namespace demo >/dev/null 2>&1 || true
# Pods are rejected until the namespace's default ServiceAccount exists.
until kk -n demo get serviceaccount default >/dev/null 2>&1; do sleep 1; done
kk apply -f - >/dev/null <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata: {name: web, namespace: demo, labels: {app: web}}
spec:
  replicas: 3
  selector: {matchLabels: {app: web}}
  template:
    metadata: {labels: {app: web}}
    spec:
      containers: [{name: nginx, image: "nginx:1.27-alpine", ports: [{containerPort: 80}]}]
---
apiVersion: v1
kind: Service
metadata: {name: web, namespace: demo, labels: {app: web}}
spec:
  selector: {app: web}
  ports: [{port: 80, targetPort: 80}]
---
# Never becomes ready: exercises error states, warnings and the timeline.
apiVersion: apps/v1
kind: Deployment
metadata: {name: broken, namespace: demo, labels: {app: broken}}
spec:
  replicas: 1
  selector: {matchLabels: {app: broken}}
  template:
    metadata: {labels: {app: broken}}
    spec:
      containers: [{name: nginx, image: "nginx:does-not-exist"}]
---
apiVersion: v1
kind: Pod
metadata: {name: logger, namespace: demo}
spec:
  containers:
  - name: logger
    image: busybox:1.36
    args: [sh, -c, 'i=0; while true; do echo "line $i $(date)"; i=$((i+1)); sleep 1; done']
---
apiVersion: batch/v1
kind: CronJob
metadata: {name: tick, namespace: demo}
spec:
  schedule: "*/1 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers: [{name: tick, image: busybox:1.36, command: [sh, -c, "date; echo tick"]}]
---
apiVersion: v1
kind: ConfigMap
metadata: {name: app-config, namespace: demo}
data: {LOG_LEVEL: debug}
---
apiVersion: v1
kind: Secret
metadata: {name: app-secret, namespace: demo}
stringData: {password: s3cr3t}
EOF

echo "kwok fake nodes"
bash scripts/kwok-load.sh 0 >/dev/null

kk -n demo rollout status deploy/web --timeout=180s >/dev/null
kk -n monitoring rollout status deploy/prometheus-server --timeout=180s >/dev/null
echo "ready: KUBECONFIG=$KUBECONFIG (context kind-$NAME)"
