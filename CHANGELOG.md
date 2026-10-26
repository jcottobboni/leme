# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/);
while in 0.x, minor versions may change behavior.

## [0.3.4] - 2026-10-26

First public release.

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
