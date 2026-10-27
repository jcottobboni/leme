# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/);
while in 0.x, minor versions may change behavior.

## [Unreleased]

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
