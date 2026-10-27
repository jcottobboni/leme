# Architecture

## Principles

1. **The cluster is the source of truth; the engine is the cache.** One cache per cluster, shared by
   every window. The UI never talks to an API server directly.
2. **Lightweight comes from the architecture, not the framework.** Informers on demand, row
   projections for grids, batched deltas, virtualized lists and layout off the main thread.
3. **No agent and no CLI dependency.** exec, port-forward, logs, Helm and auth run natively in the
   engine; `kubectl` and `helm` are optional.
4. **No mutation without a preview.** Diff, server-side dry-run and confirmation.
5. **Nothing leaves the machine.** No telemetry; works air-gapped.

## Overview

```
┌──────────────────────── Electron (thin shell) ─────────────────────────┐
│ main: windows, menus, engine supervision, pop-out windows              │
│ renderer (React, sandboxed): dockview workspace · virtualized grids ·  │
│   Monaco (YAML) · xterm.js · React Flow + elkjs (topology) · SVG map, │
│   PixiJS/WebGL map above 1,500 workloads (lazy) ·                      │
│   uPlot (metrics) · canvas timeline · Web Workers for layout           │
└──────────────▲──────────────────────────────────────▲──────────────────┘
               │ HTTP/JSON (commands, queries)         │ WebSocket (grid deltas,
               │ 127.0.0.1:<random port> + token       │ logs, exec, timeline)
┌──────────────┴──────────────────────────────────────┴──────────────────┐
│ engine (Go, single binary, started by the shell, dies with it)         │
│ kube       kubeconfig contexts, local/production detection, OAuth      │
│ cluster    sessions, informer manager (per cluster × resource, lazy)   │
│ project    row projections and columns per kind (Table-like rows)      │
│ ops        objects, logs, exec, port-forward, rollout, overview,       │
│            search, metrics-server, OpenShift actions                   │
│ helm       Helm v4 SDK: repos, charts, releases, dry-run               │
│ rbac       SelfSubjectRulesReview/AccessReview, who-can, bindings      │
│ prom       Prometheus via API server service proxy or direct URL       │
│ graph      object relation graph (owners, selectors, volumes, env,     │
│            ingress/routes, service accounts, nodes)                    │
│ clustermap namespaces → apps → workloads with health                   │
│ timeline   events.k8s.io watch, grouping, retention while open         │
│ security   own rules, RBAC risks, Polaris/Kubescape/Trivy runners      │
│ netpol     NetworkPolicy evaluation, flows and simulator               │
│ analyze    deterministic diagnosis rules (M4)                          │
│ ai, anonymize, redact, change, audit, mcp   Copilot and agents (M4)    │
│ traffic    observed edges from Caretta/Hubble metrics in Prometheus    │
│ argocd     Applications/AppProjects; sync and refresh as proposals     │
│ grafana    Grafana/Prometheus detection and local port-forwards        │
│ istio      mTLS resolution, routes, offline istioctl analyze           │
│ settings   JSON settings (0600)                                        │
└────────────────────────────────────────────────────────────────────────┘
```

The same engine runs without Electron: `leme serve` serves the UI to a browser.

## Why Electron + Go

- **Predictable rendering on Linux.** A bundled Chromium behaves the same everywhere; system
  webviews (WebKitGTK) are unreliable with WebGL, Wayland and some GPU drivers.
- **Mature multi-window, Monaco and xterm.js support.**
- **The heavy work happens in Go.** client-go is the reference Kubernetes client (informers,
  exec/port-forward, auth plugins) and Helm v4 is a Go SDK, so the extra memory of the shell buys
  reliability instead of carrying the data load.

## Data flow and scale

| Problem | Approach |
|---|---|
| 10k pods flooding the UI | The engine keeps full objects; the UI receives row projections (a few hundred bytes) in deltas batched every ~200 ms |
| Kinds without a projector (CRDs) | Generic rows (name, namespace, age); CRD printer columns are on the roadmap |
| Too many watches | Informers start when a screen needs them and stop when idle; namespace-scoped lists reuse an existing cluster-wide cache |
| Cache memory | `managedFields` and last-applied annotations stripped; metadata-only informers where enough |
| Object details | Full object fetched only when opened |
| Large graphs | Layout with elkjs in a Web Worker; pods of one controller aggregated; culling and level of detail on the map |
| Metrics | One query per (cluster, query, window), deduplicated and cached for 10 s |

Measured on kind + kwok with 5,000 pods: grid snapshot in about 1 s and scrolling at ~58 fps;
namespace topology in 0.9 s cold / 0.1 s warm; namespace security report in 0.75 s cold.

## Workspace (UI)

- **Shell** (`features/shell`): top bar, hotbar of pinned clusters with per-cluster identity
  (alias, short tag, color, icon, environment), navigator per cluster, status bar.
- **Workspace** (`features/workspace`): dockview tabs with preview (temporary) tabs, split views,
  pop-out windows and a bottom dock for terminals and logs. The layout is restored on start, but
  no cluster reconnects on its own.
- **Grids** (`components/ResourceGrid.tsx`): live rows over WebSocket, virtualized, proportional
  columns, details drawer, row and bulk actions.
- **State**: zustand stores; UI preferences and cluster profiles in `localStorage`.
- **Theme**: CSS tokens for dark and light, followed by dockview, Monaco and the charts.

## Security

The security model (read-only by default, production unlock, local API protection, credential
handling, scanner isolation) is described in [SECURITY.md](../SECURITY.md).

## Packaging

electron-builder produces a `.deb` and an AppImage; the engine binary and the built UI ship as
extra resources. The `.deb` configures the Chromium sandbox; on systems that restrict user
namespaces the AppImage needs `--no-sandbox`.
