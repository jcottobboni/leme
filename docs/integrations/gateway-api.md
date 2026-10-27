# Gateway API

Leme treats the Kubernetes Gateway API (`gateway.networking.k8s.io`) as built-in network kinds:
they appear in the navigator under **Rede**, with their own columns and health, in the Topology and
in the diagnosis.

## What it needs
- The Gateway API CRDs (standard or experimental channel). Kinds that are not installed simply do
  not appear. Nothing else: no controller-specific integration and no credentials.
- Istio's `networking.istio.io/Gateway` is a different kind: it stays under Custom Resources and the
  Istio panel, and none of the rules below apply to it (every rule checks the API group, not only
  the kind name).

## What it shows
| Kind | Columns | Health |
|---|---|---|
| GatewayClass | controller, status | `Accepted` |
| Gateway | class, listeners (`name:port/protocol hostname`), addresses, attached routes, status | `Accepted`, then `Programmed` |
| HTTPRoute, GRPCRoute, TLSRoute | hostnames, parents, backends, status | `Accepted` and `ResolvedRefs` of every parent |
| TCPRoute, UDPRoute | parents, backends, status | same |
| ReferenceGrant | from (namespace/kind), to (kind and name) | — |
| BackendTLSPolicy | targets, hostname, status | `Accepted` of each ancestor |
| ListenerSet | Gateway, listeners, status | `Accepted`, then `Programmed` |

- States: **Programado**, **Não programado**, **Não aceito**, **Pendente** (no controller has
  decided yet, e.g. `Unknown`/`Pending`), **Aceita**, **Não aceita**, **Refs não resolvidas** and
  **Sem status** (no controller wrote a status).
- Parents and backends in the route's own namespace are shown by name; others as `namespace/name`.
  Section names appear as `:section`, ports as `:port`.

## Topology
- `Gateway → GatewayClass` (class), `Gateway → Route` (from the route's `parentRefs`, also when the
  Gateway is in another namespace) and `Route → Service` (from `backendRefs`), so the chain
  continues `Service → Pod ← ReplicaSet ← Deployment`.
- In the namespace of a Gateway, routes of other namespaces attached to it are also drawn (best
  effort: it needs permission to list routes cluster-wide).
- A backend Service of the namespace that does not exist is drawn as **Ausente**. Backends that are
  not Services (e.g. implementation-specific kinds) are not drawn.

## Diagnosis
| Rule | Severity | When |
|---|---|---|
| `gateway-not-accepted` | high | the Gateway has `Accepted=False` |
| `gateway-not-programmed` | high | `Programmed=False` (other than `Pending`) |
| `gateway-pending` | low | no `Programmed=True` yet: usually no controller for the class |
| `gwroute-not-accepted` | high | a parent has `Accepted=False` |
| `gwroute-refs-unresolved` | high | a parent has `ResolvedRefs=False` |
| `gwroute-missing-gateway` | high | a `parentRefs` Gateway does not exist |
| `gwroute-missing-backend` | high | a backend Service does not exist (checked by Leme, even without a controller) |
| `gwroute-missing-port` | high | the backend Service does not expose the port |
| `gwroute-no-referencegrant` | high | a backend in another namespace has no ReferenceGrant there allowing this route kind from this namespace |

The rules read only what the API shows; cross-namespace checks are skipped (not reported) when
Leme cannot read the other namespace.

## Tests
`app/e2e/gateway-api.spec.ts` runs against the isolated kind cluster only. `scripts/gitops-crds.sh`
installs the Gateway API v1.6.3 experimental CRDs, checked against a SHA-256 pinned in the script;
no controller is installed. The fixtures (`app/e2e/fixtures/gateway`) live in the `m7-gateway` and
`m7-gateway-backend` namespaces plus the cluster-scoped `m7-gateway-class`; the test writes their
status with `kubectl patch --subresource=status` and leaves them in place. The CRDs are never
deleted.
