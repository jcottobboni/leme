# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/);
while in 0.x, minor versions may change behavior.

## [0.7.0] - 2026-10-27

M7 "evolução": the most requested features of other Kubernetes desktop tools, without paywall.

### Added
- **Aggregated logs:** "Logs (todos os pods)" on Deployments, StatefulSets, DaemonSets, ReplicaSets
  and Jobs, or by label selector: one stream with a color and prefix per pod, follow, text and regex
  filter, pause, timestamps and download. Pods that join (a rollout) or leave are followed and
  marked. Up to 20 pods; works in read-only mode.
- **Compare ("Comparar com…"):** the same object in another connected cluster and/or namespace,
  side by side, after normalizing away managed fields, status and other noise. Secret values stay
  masked; only the keys that differ are named.
- **Change history:** creations, spec/label/annotation changes and deletions observed by the open
  views are kept locally for 7 days ("Histórico" tab of an object, "Mudanças" in the timeline), past
  the TTL of Kubernetes events. Secrets and ConfigMaps record only which keys changed.
- **Gateway API:** Gateway, GatewayClass, HTTPRoute, GRPCRoute, TCPRoute, TLSRoute and UDPRoute with
  useful columns and health from their conditions; Gateway → Route → Service in the topology;
  diagnosis of routes not accepted, missing backends and cross-namespace references without a
  ReferenceGrant. Istio's Gateway is not affected. See docs/integrations/gateway-api.md.
- **Flux CD:** Kustomizations, HelmReleases and sources with readiness, revision and suspension;
  reconcile, suspend and resume as reviewed change proposals audited with source `flux`.
  See docs/integrations/flux.md.
- **k9s-style filters** in the palette and grid search: `:alias [ns]`, `-l app=web` (server-side
  label selector), `!text`, `/regex/`, `ns:name`, shown as removable chips.
- **JSONPath columns** per resource in the grid columns menu, evaluated by the engine (Secrets on a
  masked copy, with limits).
- **Local AI with Ollama:** preset provider on loopback, treated as local (no cloud consent), with
  the installed models listed; "Exportar proposta" downloads the final YAML and the patch.

## [0.6.0] - 2026-10-27

M6 "pente fino": correctness, robustness, performance and a layout pass, from a review of the
whole code base, 77 screenshots and a comparison with other Kubernetes desktop tools.

### Fixed
- Kind-specific rules apply only to Kubernetes and OpenShift API groups everywhere (navigator,
  columns, actions, tabs, diagnosis, security, topology, map, timeline, MCP): a CRD named like a
  built-in kind no longer gets false findings or built-in actions. The MCP refuses an ambiguous kind
  without a group and lists the candidates.
- Workload status comes from the conditions, in Portuguese: Disponível, Progredindo, **Travado**
  (progress deadline exceeded), Degradado, Pausado, Sem réplicas. A stuck rollout no longer shows as
  healthy, and Problems reports "Rollout travado".
- The timeline groups events by their real owners (DaemonSet pods were shown as Jobs or
  Deployments) and cluster-scoped objects under "(cluster)"; a race when a collector expired.
- Namespace diagnosis reports the kinds it could not list instead of "no problems".
- A port-forward that dies leaves the list; network errors answer 502/504 instead of 400.
- The details drawer no longer re-fetches the object on every grid update; logs in follow mode keep
  the last 5 MB; closing the main window quits the app even with Grafana or pop-out windows open.

### Changed
- One header for every panel, whose actions move into a "⋯" menu when they do not fit.
- Grids keep the checkbox and Name columns on the left and the actions on the right while scrolling;
  double click a column edge to fit it.
- Topology, NetworkPolicy and RBAC follow the global namespace (or open on the busiest namespace).
- Empty states that teach (traffic sources with copyable commands, Istio summary, cluster-admin
  banner in RBAC), clearer object details (relative dates, status pills, event filter), metric axes
  with units, Portuguese wording throughout, keyboard navigation in the navigator.
- Initial download of the UI went from 4.3 MB to under 0.8 MB: panels, the YAML editor, terminals,
  graphs and charts load on demand; hidden tabs stop polling.
- Every write made from the UI (apply, delete, scale, restart, rollbacks, Helm, Argo CD) is audited
  with source `ui`; the audit file rotates at 10 MB.

### Added
- CRD printer columns (`additionalPrinterColumns`) in the grids, and favorite CRDs pinned to the top
  of Custom Resources.

## [0.5.3] - 2026-10-27

### Added
- Kubeconfig files and folders can be added in the app ("Arquivos kubeconfig"), with bounded folder
  scans; only the paths are stored (#4, contributed).
- "Topologia do projeto": an OpenShift-console-style topology of one namespace, with status rings,
  grouping by application or Helm release, decorators and draggable layout (#4, contributed).
- Pod shells can be allowed without unlocking writes ("Liberar shell"); production asks for the
  context name (#3, contributed).

### Changed
- A context on the same API server as a production context counts as production (#3, contributed).

### Fixed
- "Configurar Prometheus" on a cluster without any Prometheus blanked the window (the engine sent
  `candidates: null`). Each panel now renders inside an error boundary, so a failure stays in that
  tab instead of blanking the whole app.
- The overview blanked the window on namespaces without warnings or without access to nodes (#2,
  contributed).
- Links from cluster data in the project topology (Route host, `vcs-uri`) open only when http(s).

## [0.5.2] - 2026-10-27

### Fixed
- A CRD reusing a built-in kind name (for example Longhorn's `nodes.longhorn.io`, kind `Node`, or
  Istio's `Gateway`) showed up as a second built-in entry in the navigator, got the built-in columns
  and could offer that kind's actions. Kind-specific categories, columns, health, actions and tabs now
  apply only to Kubernetes and OpenShift API groups; such CRDs are listed under Custom Resources.

## [0.5.1] - 2026-10-27

### Changed
- **Cluster map, visual refresh (SVG):** text and bars keep a readable screen size at every zoom (no
  more giant or overlapping labels); pods are a honeycomb of small hexagons inside each workload,
  problems at the center; workloads get a kind tag (DEPLOY, STS, CRON…), a pulsing alert badge when
  they have problems and, zoomed in, chips for Services, Ingress hosts, PVCs and restarts. Namespaces
  get a header pill with workload and pod counts and a soft glow; namespaces without workloads are
  small dashed islands (they can be hidden in the map settings). Richer tooltip with a pod bar.

### Added
- Cluster map minimap (click or drag to move), legend, and a selection ring on the last workload
  opened (Escape clears it). The dotted backdrop pans with the map.
- The map response carries each workload's container restarts and the Ingress hosts routing to its
  Services (omitted in the compact response of big maps).

## [0.5.0] - 2026-10-27

### Added
- **Cluster map at scale:** above a threshold (default 1,500 workloads, in the map settings) the map
  draws with WebGL (PixiJS, loaded on demand) with level of detail, culling and a layout in a Web
  Worker; below it stays in SVG. Search, filters, tooltip, click and double click behave the same in
  both. Without WebGL the map stays in SVG and says so. The engine now sends up to 25,000 workloads
  and a compact response above 1,500 (status text and service names move to the object panel).
- **Observed traffic:** connections between workloads from Caretta (bytes/s) or Hubble (flows/s)
  metrics in the configured Prometheus. "Tráfego observado" panel with source → destination, port
  and volume; dashed, animated edges in the Topology and in the cluster map. Nothing is installed
  in the cluster. See docs/integrations/traffic.md.
- **Argo CD:** Applications with sync and health, sources, destination, managed resources (linked to
  the objects), conditions and deploy history. Sync and Refresh go through the reviewed change flow
  (diff, dry-run, RBAC check, confirmation, audit with source `argocd`) on the Application only;
  no Argo CD credentials are used. See docs/integrations/argocd.md.
- **Grafana and Prometheus:** detects Grafana, Prometheus and Thanos Services and opens their web UI
  through a local port-forward, in a sandboxed window on the desktop (new tab in `leme serve`). The
  admin credential is only referenced, never read. See docs/integrations/grafana.md.
- **Istio:** effective mTLS per namespace (with workload exceptions), VirtualService routes with
  weights, DestinationRules, Gateways and an optional offline `istioctl analyze` over an exported,
  masked copy of the configuration. Needs only the CRDs. See docs/integrations/istio.md.

### Changed
- Double click on a map tile opens the Topology of its namespace (it used to work only on islands and
  apps).
- `pixi.js` 8.22.0 (MIT) is bundled; THIRD_PARTY_NOTICES.md lists it. The license generator accepts a
  vendored license text for packages published without one (`@pixi/colord`).

### Fixed
- Applying a change proposal kept the object's status only on kinds with a status subresource; on
  others (the Argo CD Application CRD) the status, including the deploy history, was wiped. The live
  status is now sent back.
- Applies through the generic change route are audited under the proposal's source instead of always
  `copilot`.

## [0.4.0] - 2026-10-27

### Added
- **Diagnosis without AI:** an analyzer that explains common problems with evidence and a
  suggestion (image pull, crash loops and OOM, scheduling, probes, stuck rollouts, Services
  without endpoints, broken Ingress/Route backends, pending PVCs, node pressure, HPA, Jobs and
  CronJobs). "Diagnóstico" tab on every object and a "Problemas" panel per cluster. Offline.
- **Copilot (opt-in):** chat about the selected object or the cluster with Claude (Anthropic API),
  any OpenAI-compatible API, or a local Ollama model. It reads through read-only tools and can
  propose a fix as a reviewed diff; applying always needs your approval, server dry-run and the
  cluster's write unlock. It cannot delete or touch Secrets, RBAC, Namespaces, CRDs or ConfigMap
  contents.
- **Privacy:** off by default; consent per cloud provider; Secret and env values never leave the
  machine; names, hosts, IPs and e-mails are anonymized (including derived names such as
  ReplicaSets and Pods) and restored in the answer; "see what will be sent" preview; local audit
  log. API keys stay in the OS keychain and are bound to the endpoint they were entered for.
- **MCP server (`leme mcp`):** lets agents such as Claude Code read clusters through Leme. Writes
  are off by default, enabled per context in the app (production needs the typed context name),
  always propose → apply with a short-lived token, and audited. See docs/mcp.md.
- New icon: a ship's helm wheel.

### Changed
- UI review of every screen in both themes: visible selected segments in dark mode, grid headers
  that no longer collide and columns that keep ages and IPs readable, Events sorted by last seen,
  responsive Overview, chart axes not clipped, HPA metrics as "cpu: 35%/80%", Portuguese labels
  for OpenShift columns and actions (Escalar, Reiniciar), icons for every resource kind, clearer
  empty states, and twins in the cluster list told apart by host.

### Security (internal review before release)
- Redaction goes deeper: credential-named fields at any depth, private keys (Route TLS),
  password arguments, OpenShift Template parameters and Secret annotations.
- Proposals that resend masked values are refused; privilege or exposure escalations are flagged,
  need an explicit ack in the app and can never be applied by MCP agents.
- Anonymization aliases persist per conversation; referenced names and the kubeconfig user are
  anonymized too.
- Agent write permission is bound to the context's server, cluster and user.
- CI downloads are pinned by SHA-256, release builds do not restore caches, and the README checks
  the APT key fingerprint before trusting it.


### Security (contributed in #1)
- A loopback API server no longer makes a cluster local on its own: SSH tunnels and
  `kubectl proxy` to remote clusters now open read-only. Local detection also requires a context
  name from a local tool and only accepts 172.16.0.0/12 and 192.168.0.0/16 as container networks.
- Production markers glued to a prefix (`ocpprd`, `k8sprod01`) and `produccion` are detected.
- DOMPurify, bundled by Monaco, is pinned to 3.4.17.

### Fixed
- The engine reports the app version (it said 0.3.0 in 0.3.5).

## [0.3.5] - 2026-10-26

### Added
- Windows installer (x64, per-user, NSIS). Unsigned for now: SmartScreen asks once.
- CI runs the engine tests and a desktop smoke test (engine start, contexts, UI state across
  restart) on Linux, Windows and macOS; the release installs and starts the Windows package.

### Changed
- External scanners get a portable minimal environment (Windows system variables, `.exe` lookup).

## [0.3.4] - 2026-10-26

First public release.

### Distribution
- Signed APT repository published with every release: `apt install leme`, updates through
  `apt upgrade`. `.deb` and AppImage with `SHA256SUMS`; third-party license texts inside the packages.

### Fixed before release
- Desktop app: hotbar pins, cluster profiles, theme, column widths and the tab layout now survive
  restarts. The page origin changed with the engine's random port on every launch, so the browser
  storage started empty each time; the shell now keeps this state in `~/.config/leme/ui-state.json`.
- Empty resource lists no longer stay on the loading skeleton.
- Feature CSS no longer leaks into other screens; double-click on a row opens the tab.

### Clusters and safety
- Every kubeconfig context; nothing connects on its own.
- Remote clusters open read-only, enforced in the engine; production contexts unlock by typing the
  context name.
- Hotbar with per-cluster alias, short tag, color, icon and environment; hover card; drag to reorder.

### Resources and workloads
- Live grids for every kind, details drawer, bulk actions.
- YAML editing with server-side dry-run and diff.
- Logs, exec shell, port-forward, scale, restart, Deployment rollback.

### Observability
- Cluster overview with capacity and namespaces.
- Object topology grouped by app.
- Cluster map.
- Event timeline.
- Prometheus metrics with reference lines.

### Helm, RBAC and security
- Helm releases and charts (install, upgrade, rollback, uninstall) with dry-run and diff.
- RBAC: my permissions, can-I, who-can, subjects.
- Security score and findings, RBAC risks, NetworkPolicy flows and simulator, optional
  Polaris/Kubescape/Trivy.

### OpenShift
- Routes, DeploymentConfigs, BuildConfigs, Builds, ImageStreams, Projects.
- In-memory OAuth login.

### UI
- Dockview workspace with preview tabs, split views and pop-out windows.
- Dark and light themes.
- Ctrl+K global search.
