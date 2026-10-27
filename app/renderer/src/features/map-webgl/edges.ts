// Traffic edges placed on the map layout (shared by the SVG and WebGL renderers).
import { call, qs } from '../../api'
import type { Layout } from '../map/layout'
import { TILE_R } from '../map/layout'
import { endpointKey, setTrafficProvider, type TrafficEdge, type TrafficEndpoint, type TrafficSnapshot } from './trafficEdges'

/** One drawable edge, in world coordinates. `w` is the stroke width in screen pixels. */
export interface EdgeL {
  x0: number; y0: number; x1: number; y1: number
  /** Gap to leave at the target end (tile radius, or 0 for an island centre). */
  gap: number
  w: number
  e: TrafficEdge
  label: string
}

export interface PlacedEdges { edges: EdgeL[]; dropped: number }

const kindKey = (kind: string, ns: string, name: string) => `${kind}/${ns}/${name}`

/** Width in pixels: 1.5 for the smallest rate up to 7 for the largest (log scale, so one big flow does not hide the rest). */
export function edgeWidth(rate: number, max: number) {
  if (!(max > 0) || !(rate > 0)) return 1.5
  return 1.5 + 5.5 * (Math.log1p(rate) / Math.log1p(max))
}

export function formatRate(rate: number, unit: TrafficEdge['unit']) {
  if (unit === 'flows/s') return `${rate < 10 ? rate.toFixed(1) : Math.round(rate)} fluxos/s`
  const u = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s']
  let v = rate, i = 0
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++ }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${u[i]}`
}

/**
 * Places snapshot edges on the layout: an endpoint resolves to its workload tile (by endpointKey, or kind/ns/name
 * when the group is absent), else to its namespace island; endpoints outside the map (Node, External, filtered
 * namespaces) drop the edge and are counted in `dropped`.
 */
export function placeEdges(layout: Layout | null, snap: TrafficSnapshot): PlacedEdges {
  if (!layout || !snap.available || !snap.edges.length) return { edges: [], dropped: 0 }
  const byKey = new Map<string, { x: number; y: number }>()
  const byKind = new Map<string, { x: number; y: number }>()
  for (const t of layout.tiles) {
    byKey.set(endpointKey({ group: t.w.group, kind: t.w.kind, namespace: t.ns, name: t.w.name }), t)
    byKind.set(kindKey(t.w.kind, t.ns, t.w.name), t)
  }
  const isl = new Map(layout.islands.map((i) => [i.ns.name, i]))
  const at = (e: TrafficEndpoint): { x: number; y: number; gap: number } | null => {
    const t = byKey.get(endpointKey({ ...e, group: e.group ?? '' })) ?? byKind.get(kindKey(e.kind, e.namespace ?? '', e.name))
    if (t) return { x: t.x, y: t.y, gap: TILE_R + 4 }
    const i = e.namespace ? isl.get(e.namespace) : undefined
    return i ? { x: i.x, y: i.y, gap: 0 } : null
  }
  const max = snap.edges.reduce((m, e) => Math.max(m, e.rate), 0)
  const edges: EdgeL[] = []
  let dropped = 0
  for (const e of snap.edges) {
    const a = at(e.from), b = at(e.to)
    if (!a || !b || (a.x === b.x && a.y === b.y)) {
      dropped++
      continue
    }
    const port = e.port ? `:${e.port}` : ''
    edges.push({ x0: a.x, y0: a.y, x1: b.x, y1: b.y, gap: b.gap, w: edgeWidth(e.rate, max), e, label: `${e.from.name} → ${e.to.name}${port} · ${formatRate(e.rate, e.unit)}` })
  }
  // Thin first, so the heavy edges end up on top.
  edges.sort((p, q) => p.w - q.w)
  return { edges, dropped }
}

/**
 * Arrow head for an edge: the tip stops `gap` world units before the target centre; sizes are in screen pixels
 * converted with the zoom `k`. Returns the shortened line end and the three triangle points.
 */
export function arrowOf(e: EdgeL, k: number) {
  const dx = e.x1 - e.x0, dy = e.y1 - e.y0
  const len = Math.hypot(dx, dy) || 1
  const ux = dx / len, uy = dy / len
  const tipD = Math.min(e.gap, len * 0.45)
  const tx = e.x1 - ux * tipD, ty = e.y1 - uy * tipD
  const hl = (7 + e.w * 1.6) / k, hw = (4 + e.w) / k
  const bx = tx - ux * hl, by = ty - uy * hl
  return { ex: bx, ey: by, tip: [tx, ty, bx - uy * hw, by + ux * hw, bx + uy * hw, by - ux * hw] as const }
}

/**
 * Test-only provider: with localStorage `leme.e2e.trafficProvider` = "1" the map reads GET /api/clusters/{id}/traffic
 * directly, so the e2e can serve a fake snapshot with page.route before the traffic feature ships its provider.
 * Off by default; never set by the app.
 */
export function installTestTrafficProvider() {
  try {
    if (localStorage.getItem('leme.e2e.trafficProvider') !== '1') return
  } catch {
    return
  }
  setTrafficProvider((id, namespace) => call<TrafficSnapshot>('GET', `/api/clusters/${encodeURIComponent(id)}/traffic?` + qs({ namespace })))
}
