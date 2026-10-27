#!/usr/bin/env bash
# Installs the third-party CRDs used by the M7 e2e tests in the isolated kind cluster ONLY:
# the Gateway API (experimental channel: Gateway, GatewayClass, HTTP/GRPC/TCP/TLS/UDPRoute,
# ReferenceGrant, BackendTLSPolicy) and the Flux CD toolkit CRDs (source, kustomize, helm).
# Only the CRDs: no controller is installed, so the tests write the status themselves
# (kubectl patch --subresource=status). Every manifest is checked against the SHA-256 pinned here.
# Idempotent: CRDs already present are left as they are (the cluster is shared by the suites).
# Usage: scripts/gitops-crds.sh
set -euo pipefail
cd "$(dirname "$0")/.."
kk() { kubectl --kubeconfig "${DEV_KUBECONFIG:-$HOME/.kube/leme-dev/kubeconfig}" --context "kind-${KIND_NAME:-leme-dev}" "$@"; }

# Applies a release manifest only if it matches the SHA-256 pinned here (server-side: the CRDs are
# larger than the last-applied annotation allows).
apply_pinned() {
  local f; f="$(mktemp)"
  curl -fsSL "$1" -o "$f"
  echo "$2  $f" | sha256sum --check --strict --quiet - || { echo "checksum mismatch: $1" >&2; rm -f "$f"; exit 1; }
  kk apply --server-side --force-conflicts -f "$f" >/dev/null; rm -f "$f"
}

GW=https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.3
if ! kk get crd httproutes.gateway.networking.k8s.io tcproutes.gateway.networking.k8s.io >/dev/null 2>&1; then
  echo "gateway api v1.6.3 (experimental)"
  apply_pinned $GW/experimental-install.yaml 72f549ff57416e2e1085c5be1718f5f86108aa75ac39b4bc27c1c7aef1497127
fi

FLUX=https://github.com/fluxcd
if ! kk get crd gitrepositories.source.toolkit.fluxcd.io >/dev/null 2>&1; then
  echo "flux source-controller v1.9.6 CRDs"
  apply_pinned $FLUX/source-controller/releases/download/v1.9.6/source-controller.crds.yaml 5acf3b01c24f15647a162f2d43b1287f0d0156fab0026f4afb9865874cdbe16d
fi
if ! kk get crd kustomizations.kustomize.toolkit.fluxcd.io >/dev/null 2>&1; then
  echo "flux kustomize-controller v1.9.6 CRDs"
  apply_pinned $FLUX/kustomize-controller/releases/download/v1.9.6/kustomize-controller.crds.yaml b9e74622f9f3d87950f59641651fe0ea0220367c1d6f34a72c9591552afda292
fi
if ! kk get crd helmreleases.helm.toolkit.fluxcd.io >/dev/null 2>&1; then
  echo "flux helm-controller v1.6.5 CRDs"
  apply_pinned $FLUX/helm-controller/releases/download/v1.6.5/helm-controller.crds.yaml 8af19966e63cccde7d2c62e24c7bd3466010dd53e9d063e2ce22d1391e11f272
fi

kk wait --for=condition=Established --timeout=60s \
  crd/gateways.gateway.networking.k8s.io crd/gatewayclasses.gateway.networking.k8s.io crd/httproutes.gateway.networking.k8s.io \
  crd/grpcroutes.gateway.networking.k8s.io crd/tcproutes.gateway.networking.k8s.io crd/tlsroutes.gateway.networking.k8s.io \
  crd/udproutes.gateway.networking.k8s.io crd/referencegrants.gateway.networking.k8s.io \
  crd/gitrepositories.source.toolkit.fluxcd.io crd/ocirepositories.source.toolkit.fluxcd.io crd/helmrepositories.source.toolkit.fluxcd.io \
  crd/buckets.source.toolkit.fluxcd.io crd/kustomizations.kustomize.toolkit.fluxcd.io crd/helmreleases.helm.toolkit.fluxcd.io >/dev/null
echo "gitops CRDs ready"
