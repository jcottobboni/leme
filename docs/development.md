# Development

## Requirements

- Go 1.27+
- Node.js 24 and npm
- For tests: Docker, [kind](https://kind.sigs.k8s.io/) and `kubectl`
- Optional: `polaris`, `kubescape`, `trivy` (security scanner integrations)

## Layout

```
engine/            Go engine: kubeconfig, sessions, informer caches, row projections, HTTP/WS API,
                   exec, logs, port-forward, Helm (SDK v4), RBAC, Prometheus, topology graph,
                   cluster map, timeline, security rules and scanners, NetworkPolicy, OpenShift OAuth
app/main/          Electron main process and preload
app/renderer/      React UI (features/<area>, components/, store.ts)
app/e2e/           Playwright tests (browser and Electron) and fixtures
app/scripts/       UI sweep
scripts/           test cluster setup and load generation
```

## Build and run

```bash
make deps        # npm ci in app/
make engine      # engine/bin/leme
make ui          # app/renderer-dist
make run         # desktop app with your kubeconfig (remote clusters open read-only)
make run-dev     # desktop app with the kind test cluster only
make serve-dev   # engine + UI in the browser (kind only); prints the URL with the session token
make dist        # .deb and AppImage in app/release/
```

`leme serve --addr 127.0.0.1:<port> --static app/renderer-dist` runs the engine without
Electron; the UI opens in any browser through the printed URL.

## Test cluster

```bash
make dev-cluster
```

`scripts/dev-cluster.sh` is idempotent. It creates the kind cluster `leme-dev` with its own
kubeconfig at `~/.kube/leme-dev/kubeconfig` and installs:

- metrics-server and a small Prometheus (`monitoring` namespace);
- the `demo` namespace: a healthy `web` Deployment and Service, a `broken` Deployment, a `logger`
  Pod, a `tick` CronJob, a ConfigMap and a Secret;
- kwok fake nodes.

`DEV_KUBECONFIG` overrides the kubeconfig path. The M3 tests apply and remove their own fixtures
(`scripts/m3-fixtures.sh`: OpenShift look-alike CRDs, an insecure workload, an RBAC risk).

## Tests

```bash
make test-go     # go vet + Go unit tests
make test-e2e    # typecheck + Playwright e2e (browser and Electron), kind only
make test        # both
```

The e2e tests start their own engine on a random port, use the kind kubeconfig and the fake
contexts in `app/e2e/*.kubeconfig`, and expect `make engine ui` to have been run.
Playwright uses the system Chrome (`channel: 'chrome'`).

Scale testing with kwok:

```bash
make scale-lab N=5000   # N fake pods in the "scale" namespace
make perf               # grid render and scroll with every pod
```

## UI sweep

```bash
make sweep OUT=/tmp/leme-sweep
```

`app/scripts/ui-sweep.mjs` visits every screen (cluster catalog, tools, every kind in the navigator,
object details and tabs, menus and dialogs) at 1500 px in the dark theme and 1280 px in the light
theme. It uses the kind cluster plus the fake contexts in `app/e2e/fixtures/sweep-fakes.kubeconfig`,
saves a screenshot per screen and writes `report.txt` with page errors, elements sticking out of
their container or the viewport, and text clipped without an ellipsis.

### README demo

```bash
make engine ui && cd app && node scripts/demo-gif.mjs
```

It records a scripted tour against the kind cluster (read-only, only the kind context visible,
example plugins in a temporary config dir) and writes `docs/images/demo.gif` with ffmpeg.

## Releases

Bump `app/package.json`, update `CHANGELOG.md`, then tag `vX.Y.Z` on `main`. The release workflow
builds the engine and the Linux packages and publishes a GitHub release that is also a flat, signed
APT repository: `Packages`, `Release`, `InRelease` and `Release.gpg` sit next to the `.deb`, and
`releases/latest/download/` always resolves to the newest release, so APT users get it with
`apt upgrade`.

The index is signed with the key in `packaging/leme-archive-keyring.asc`; its private part is the
`APT_SIGNING_KEY` repository secret. Rotating it means publishing the new public key and asking
users to replace `/usr/share/keyrings/leme-archive-keyring.gpg`.

## Troubleshooting

- **kind pods crash with `too many open files`** (kube-proxy, kwok-controller): the host's inotify
  limits are too low, especially with more than one kind cluster. Raise them:
  `sudo sysctl fs.inotify.max_user_instances=512 fs.inotify.max_user_watches=524288`.
- **The AppImage does not start on Ubuntu 24.04+:** run it with `--no-sandbox`, or install the `.deb`.
