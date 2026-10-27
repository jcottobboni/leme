# Third-party notices

Leme depends on the open source components below, used unmodified.

The packaged app ships the full license texts in `resources/licenses/` (Leme's LICENSE and NOTICE,
this file, and `THIRD_PARTY_LICENSES-engine.txt` for every Go module linked into the engine) and in
`resources/renderer-dist/THIRD_PARTY_LICENSES.txt` (every npm package in the UI bundle, fonts
included). Both lists are generated at build time from what is actually linked or bundled
(`scripts/go-licenses.mjs` and `app/scripts/third-party-licenses.mts`). Electron adds
`LICENSE.electron.txt` and `LICENSES.chromium.html`.

## Engine (Go)

| Module | Version | License |
|---|---|---|
| k8s.io/client-go | v0.37.1 | Apache-2.0 |
| k8s.io/api | v0.37.1 | Apache-2.0 |
| k8s.io/apimachinery | v0.37.1 | Apache-2.0 |
| k8s.io/metrics | v0.37.1 | Apache-2.0 |
| sigs.k8s.io/yaml | v1.6.0 | MIT / BSD-3-Clause |
| github.com/coder/websocket | v1.8.15 | ISC |
| helm.sh/helm/v4 (SDK) | v4.3.1 | Apache-2.0 |

The Helm SDK brings transitive dependencies. As of 2026-10-25, the 123 modules linked into the binary were 59 Apache-2.0, 36 MIT, 25 BSD, 2 ISC and 1 MPL-2.0 (`github.com/cyphar/filepath-securejoin`, unmodified; source: https://github.com/cyphar/filepath-securejoin). No GPL/AGPL. To list them: `go version -m engine/bin/leme`.

## Desktop / UI

| Package | Version | License |
|---|---|---|
| electron | 45.0.0 | MIT |
| react, react-dom, scheduler | 19.3.1 / 0.28.0 | MIT |
| dockview-react, dockview, dockview-core | 8.6.2 | MIT (the proprietary `dockview-enterprise` package is NOT used) |
| @tanstack/react-virtual, @tanstack/virtual-core | 3.14.22 / 3.19.7 | MIT |
| lucide-react (icons) | 1.54.0 | ISC |
| @fontsource-variable/inter (Inter font, bundled) | 5.3.0 | OFL-1.1 |
| @fontsource-variable/jetbrains-mono (JetBrains Mono font, bundled) | 5.3.0 | OFL-1.1 |
| monaco-editor | 0.57.0 | MIT (bundles code from other projects; their notices are in its `ThirdPartyNotices.txt`, included in the generated file) |
| @xterm/xterm, @xterm/addon-fit, @xterm/addon-search | 6.0.0 / 0.11.0 / 0.16.0 | MIT |
| zustand, use-sync-external-store | 5.0.16 (and 4.5.7 via React Flow) / 1.7.0 | MIT |
| @xyflow/react, @xyflow/system (React Flow) | 12.12.0 / 0.0.83 | MIT (d3-zoom, d3-selection, d3-drag, d3-interpolate, d3-color, d3-dispatch, d3-timer, d3-transition: ISC; d3-ease: BSD-3-Clause; classcat: MIT) |
| elkjs | 0.12.0 | EPL-2.0 OR GPL-3.0-or-later. Used under **EPL-2.0**, unmodified; the `elk-worker.min.js` worker is distributed as is. Source: https://github.com/kieler/elkjs |
| uplot | 1.6.32 | MIT |

Build and test tools (not distributed): vite (MIT), typescript (Apache-2.0), electron-builder (MIT), @playwright/test (Apache-2.0).

## Optional external tools

Leme does **not** bundle or link these tools. If they are installed, the app runs them as separate processes, only over local files (Polaris, Kubescape) or images (Trivy):

| Tool | License | Use |
|---|---|---|
| Polaris (FairwindsOps) | Apache-2.0 | `polaris audit --audit-path <temp dir>` over the exported YAML |
| Kubescape | Apache-2.0 | `kubescape scan framework nsa\|mitre <dir> --keep-local`, with an isolated HOME and update checks off |
| Trivy (Aqua Security) | Apache-2.0 | `trivy image --scanners vuln` on demand; trivy-operator reports are read through the API |

The OpenShift test CRDs (`app/e2e/fixtures/openshift-crds.yaml`) were written for this project, without copying openshift/api; they only mimic the type names.

The app icon and logo (`app/build/icon.svg`) are original artwork: an isometric cube with a status dot.
