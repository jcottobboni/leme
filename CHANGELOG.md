# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/);
while in 0.x, minor versions may change behavior.

## [Unreleased]

### Security
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
