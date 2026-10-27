# Cluster map: WebGL renderer

The cluster map ("Mapa do cluster") has two stage renderers that draw the same layout:

| Workloads in the map | Renderer | Layout |
|---|---|---|
| below the threshold (default 1,500) | SVG (DOM, `map-tile-*` test ids) | main thread |
| at or above the threshold | WebGL (PixiJS v8, loaded on demand in its own chunk) | Web Worker |

The toolbar, search, filters, tooltip, footer and zoom controls are shared. The threshold is in the
map settings (gear button, `map-webgl-threshold`) and is stored with the UI preferences
(`useUi().mapWebglThreshold`).

## Behaviour

- **Level of detail** follows the SVG rules: namespaces only, then one dot per workload, then a hexagon
  with the kind icon, then the name and pod bar, then pod squares. Labels appear only at close zoom.
- **Culling and LOD** are recomputed when the view settles (about every 80 ms); pan and zoom only move the
  world transform on the GPU.
- **Hit testing** uses a spatial grid (tile, then app region, then island), so click (open the object),
  double click (topology of the namespace), hover tooltip and zoom-to-island work as in the SVG.
- **Search and "Só com problemas"** highlight and filter in both renderers.
- **No WebGL** (no context, driver failure): the map stays in SVG and shows a notice; nothing else changes.
- **Traffic edges** (toggle `map-traffic`): read through `useTrafficEdges`
  (`features/map-webgl/trafficEdges.ts`, the contract with the traffic feature). Width grows with the rate
  (log scale), the arrow shows the direction. Endpoints resolve to the workload tile (group/kind/namespace/name,
  or kind/namespace/name when the group is absent), else to the namespace island; Node and External endpoints
  are counted as "fora do mapa". Without a source the note explains what is missing.

## Engine

`GET /api/clusters/{id}/map` is unchanged. `clustermap.MaxWorkloads` is 25,000. Above
`clustermap.CompactAbove` (1,500) the response omits the tooltip-only fields (workload `status` text and
service names) and sets `compact: true`.

## Tests and probes

- `data-testid="map-renderer"` carries `data-kind` (`svg`/`webgl`), `data-ready`, `data-workloads`,
  `data-edges` and `data-fps` (frames per second over the last second, sampled with requestAnimationFrame).
- `app/e2e/map-webgl.spec.ts` (always runs): synthetic 20k-workload map through `page.route`; WebGL, search,
  click, double click, filters, threshold, SVG fallback without WebGL, traffic edges in both renderers.
  A test-only provider (localStorage `leme.e2e.trafficProvider = "1"`) reads `GET /api/clusters/{id}/traffic`
  so the e2e can serve a fake snapshot; the app never sets it.
- `app/e2e/perf/map-webgl.spec.ts` (`PERF=1`): `app/e2e/fixtures/map-webgl/kwok-20k.sh up` creates 20 kwok
  nodes `m5-mapwebgl-kwok-N` and 20,000 bare pods in `m5-mapwebgl-scale` in the kind test cluster
  (kwok only from `scripts/kwok-load.sh`, v0.8.0 with a pinned SHA-256); the spec measures the open time and
  pan/zoom frames per second over 3 s, then `kwok-20k.sh down` deletes only that namespace and those nodes.
  `KEEP_FIXTURE=1` keeps the fixture; `PERF_GL=swiftshader` uses the software GPU.
