# Flux CD integration

Leme shows the Flux CD objects of a connected cluster in **Integrações → Flux**.

## What it needs
- Any of the Flux toolkit CRDs in the cluster: `kustomize.toolkit.fluxcd.io` (Kustomization),
  `helm.toolkit.fluxcd.io` (HelmRelease) and `source.toolkit.fluxcd.io` (GitRepository,
  OCIRepository, HelmRepository, Bucket). Without them the panel explains what is missing and shows
  how to install Flux; with only some of them it lists what exists and names the missing groups.
- Nothing else: Leme reads and writes the objects through the Kubernetes API with your kubeconfig.
  It never runs the `flux` CLI and never talks to the Flux controllers.

## What it shows
- Three tabs: **Kustomizations**, **HelmReleases** and **Fontes** (sources), with search, a
  namespace filter and a state filter.
- The state comes from the object: **Suspenso** (`spec.suspend`), then the `Ready` condition
  (**Pronto**, **Falhou**, **Reconciliando** while `Reconciling=True`, **Desconhecido**).
- Kustomizations: applied revision (`status.lastAppliedRevision`), last attempted revision when it
  differs, source (`spec.sourceRef`), path and target namespace.
- HelmReleases: chart and version constraint, the revision Helm installed last
  (`status.history[0]`, as `chart@version`), and the source (`spec.chart.spec.sourceRef` or
  `spec.chartRef`).
- Sources: artifact revision (`status.artifact.revision`), URL (Bucket: endpoint and bucket) and
  the Git or OCI reference.
- Detail: the controller's message when `Ready=False`, every condition, the interval, and whether a
  reconcile request is still waiting for the controller (`requestedAt` newer than
  `status.lastHandledReconcileAt`). **Abrir objeto** opens the YAML and events; the source opens
  from the detail too.

## What it can change
Only three requests, on the six kinds above (each in its own group), all built by the engine (the
UI only sends the action name):

| Action | Merge patch | Refused when |
|---|---|---|
| Reconcile | `{"metadata":{"annotations":{"reconcile.fluxcd.io/requestedAt":"<now, RFC 3339>"}}}` | the object is suspended |
| Suspend | `{"spec":{"suspend":true}}` | it is already suspended |
| Resume | `{"spec":{"suspend":null},"metadata":{"annotations":{"reconcile.fluxcd.io/requestedAt":"<now>"}}}` (like `flux resume`) | it is not suspended |

- Each request goes through the change proposal flow: before/after diff, server-side dry-run when
  the session is writable, an RBAC check, a 10-minute single-use token bound to the object's
  resourceVersion and an explicit confirmation. Applying re-runs the dry-run.
- Read-only sessions (and production until unlocked) cannot apply: the buttons are disabled and
  the engine answers 403.
- Proposals and applies are recorded in the audit log with source `flux`.
- The Flux controller does the work; Leme only records the request. What the controller applies
  afterwards comes from Git, the OCI repository or the chart and does not go through Leme's review.
- The AI Copilot cannot propose these changes and MCP agents cannot apply them.

## API
- `GET /api/clusters/{id}/flux?namespace=`: `{installed, groups, kustomizations, helmReleases, sources, hint?, errors?}`;
  each object has `group, kind, namespace, name, ready, reason, message, suspended, reconciling,
  interval, conditions, revision, attempted, source, path, chart, url, ref, targetNamespace,
  requestedAt, handledAt, updatedAt`.
- `POST /api/clusters/{id}/flux/{kind}/{ns}/{name}/propose` with `{action: "reconcile"|"suspend"|"resume"}`
  (`kind` is `Kustomization`, `HelmRelease`, `GitRepository`, `OCIRepository`, `HelmRepository` or `Bucket`).
- `POST /api/clusters/{id}/flux/apply/{token}`

## Tests
`app/e2e/flux.spec.ts` runs against the isolated kind cluster only. `scripts/gitops-crds.sh`
installs the Flux CRDs (source-controller v1.9.6, kustomize-controller v1.9.6, helm-controller
v1.6.5), each manifest checked against a SHA-256 pinned in the script; no controller is installed.
The fixtures (`app/e2e/fixtures/flux`) live in the `m7-flux` namespace; the test writes their
status with `kubectl patch --subresource=status`, recreates them before each test and leaves them
in the fixture state. The CRDs are never deleted.
