# Istio

The Istio panel (**Ferramentas → Istio**) reads the Istio configuration straight from the API server.
It needs no Istio controller, sidecar or `istioctl`; it only needs the Istio CRDs.

## What it shows

| Tab | Source | Notes |
|---|---|---|
| mTLS | `PeerAuthentication` (`security.istio.io`) and namespace labels | Effective mode per namespace: the namespace-wide policy (no selector) wins over the mesh-wide policy in the root namespace (`istio-system`); `UNSET` inherits; nothing set is shown as "não definido" (Istio then accepts plaintext and mTLS). Policies with a selector are listed as workload exceptions, with their port-level overrides. Injection comes from `istio-injection`, `istio.io/rev` or `istio.io/dataplane-mode=ambient`. |
| Rotas | `VirtualService` (`networking.istio.io`) | Hosts, gateways and every HTTP/TCP/TLS rule with its match and weighted destinations (`host`, `subset`, `port`, `weight`), drawn as a weight bar. Hosts that resolve to a Service link to it. |
| DestinationRules | `DestinationRule` | Host and the Service it resolves to (`found`), subsets and client TLS mode. |
| Gateways | `Gateway` (`networking.istio.io`, not the Gateway API) | Workload selector and servers (port, protocol, hosts, TLS mode). |
| Analyze | `istioctl analyze` (optional) | See below. |

The API version of each kind is the one the cluster's discovery prefers (`security.istio.io` v1/v1beta1,
`networking.istio.io` v1/v1beta1/v1alpha3). Without the CRDs the panel says so instead of showing empty tabs.
After installing Istio, reconnect the cluster so the new APIs are discovered.

## Analyze

When `istioctl` is in `PATH` or `~/.local/bin`, the Analyze tab runs it **offline**:

1. The engine exports the Istio objects, Namespaces, Services and workloads of the scope to a private
   temporary directory. Secrets and ConfigMaps are never exported; status, managed fields and the
   last-applied annotation are dropped and literal env values are masked.
2. It runs `istioctl analyze --use-kube=false -o json <dir>` with no kubeconfig (`KUBECONFIG=/dev/null`),
   a private `HOME`, only proxy variables passed through and a 2-minute timeout.
3. It removes the directory and shows the messages (code, level, resource, message).

`istioctl` never talks to the cluster. Get it from the official Istio releases
(<https://github.com/istio/istio/releases>) and check the published SHA-256.

## API

- `GET /api/clusters/{id}/istio?namespace=` → `{installed, kinds, rootNamespace, namespaces, virtualServices, destinationRules, gateways, errors?}`
- `GET /api/istio/tool` → `{installed, path}` (or `{installed: false, hint}`)
- `POST /api/clusters/{id}/istio/analyze` `{namespace}` → `{installed, messages[{code, level, message, resource}]}` or `{installed: false, hint}`

## Tests

`app/e2e/istio.spec.ts` runs against the isolated kind cluster with minimal CRDs (created only when
absent, never deleted) and the `m5-istio-*` fixtures in `app/e2e/fixtures/istio/`. The analyze test uses
`istioctl` from `PATH` or downloads a pinned release, verified by SHA-256, into a temporary directory
(`LEME_E2E_NO_DOWNLOAD=1` skips it). `LEME_E2E_KEEP=1` keeps the fixture namespaces after the run.
