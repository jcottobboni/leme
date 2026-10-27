import type { ClusterMap, MapNamespace, MapWorkload } from '../../api'

// World units. A workload tile is a pointy-top hexagon of radius TILE_R; hex cells leave room for the label below.
export const TILE_R = 30
export const DX = 78
export const DY = 84
const TILE_REACH = 44
export const BLOB_AMP = 0.1

export interface TileL { id: number; x: number; y: number; w: MapWorkload; ns: string; app: string; key: string }
export interface AppL { id: number; x: number; y: number; r: number; name: string; ns: string; tiles: TileL[]; boxed: boolean; path: string }
export interface IslandL {
  id: number; x: number; y: number; r: number; ns: MapNamespace; hue: number; path: string
  font: number; labelY: number; apps: AppL[]; workloads: number; empty: boolean
}
export interface Bounds { x0: number; y0: number; x1: number; y1: number }
export interface Totals { namespaces: number; workloads: number; pods: number; apps: number; services: number }
export interface Layout { islands: IslandL[]; tiles: TileL[]; appsFlat: AppL[]; bounds: Bounds; totals: Totals }
export interface LayoutOpts { selected: Set<string>; hideSystem: boolean; onlyProblems: boolean; hideEmpty?: boolean }

export const isSystemNs = (n: string) => n.startsWith('kube-') || n.startsWith('openshift-') || n === 'openshift'
export const hasProblem = (w: MapWorkload) => w.health === 'warn' || w.health === 'error' || w.pods.warn + w.pods.error > 0

function hash(s: string) {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return h >>> 0
}
export const hueOf = (name: string) => (hash(name) * 137) % 360

let hexCache: [number, number][] = []
function hexCells(k: number): [number, number][] {
  if (hexCache.length < k) {
    const m = Math.ceil(0.75 * Math.sqrt(k)) + 3
    const cells: [number, number, number, number][] = []
    for (let r = -m; r <= m; r++) {
      for (let q = Math.max(-m, -r - m); q <= Math.min(m, -r + m); q++) {
        const x = DX * (q + r / 2), y = DY * r
        cells.push([x, y, Math.round((x * x + y * y) / 10), Math.atan2(y, x)])
      }
    }
    cells.sort((a, b) => a[2] - b[2] || a[3] - b[3])
    hexCache = cells.map(([x, y]) => [x, y])
  }
  return hexCache.slice(0, k)
}

/** Greedy spiral circle packing with a spatial hash; deterministic, biggest circles end up in the middle. */
export function pack(rs: number[], pad: number): [number, number][] {
  const out: [number, number][] = rs.map(() => [0, 0])
  if (rs.length < 2) return out
  const order = rs.map((_, i) => i).sort((a, b) => rs[b] - rs[a] || a - b)
  const cell = 2 * rs[order[0]] + pad + 1
  const grid = new Map<string, number[]>()
  const gk = (cx: number, cy: number) => `${cx},${cy}`
  const fits = (x: number, y: number, r: number) => {
    const cx = Math.floor(x / cell), cy = Math.floor(y / cell)
    for (let i = cx - 1; i <= cx + 1; i++) {
      for (let j = cy - 1; j <= cy + 1; j++) {
        const l = grid.get(gk(i, j))
        if (!l) continue
        for (const o of l) {
          const d = r + rs[o] + pad
          const dx = x - out[o][0], dy = y - out[o][1]
          if (dx * dx + dy * dy < d * d) return false
        }
      }
    }
    return true
  }
  const put = (i: number) => {
    const k = gk(Math.floor(out[i][0] / cell), Math.floor(out[i][1] / cell))
    const l = grid.get(k)
    if (l) l.push(i)
    else grid.set(k, [i])
  }
  put(order[0])
  for (let n = 1; n < order.length; n++) {
    const i = order[n], r = rs[i]
    const ds = Math.max(4, r * 0.3), b = ds * 1.2 / (2 * Math.PI) * 2
    let th = 0
    for (let guard = 0; guard < 4_000_000; guard++) {
      const rho = b * th
      const x = rho * Math.cos(th), y = rho * Math.sin(th)
      if (fits(x, y, r)) {
        out[i] = [x, y]
        break
      }
      th += ds / Math.max(rho, ds)
    }
    put(i)
  }
  return out
}

function enclose(cs: { x: number; y: number; r: number }[]) {
  if (!cs.length) return { cx: 0, cy: 0, r: 0 }
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
  for (const c of cs) {
    x0 = Math.min(x0, c.x - c.r); x1 = Math.max(x1, c.x + c.r)
    y0 = Math.min(y0, c.y - c.r); y1 = Math.max(y1, c.y + c.r)
  }
  const cx = (x0 + x1) / 2, cy = (y0 + y1) / 2
  let r = 0
  for (const c of cs) r = Math.max(r, Math.hypot(c.x - cx, c.y - cy) + c.r)
  return { cx, cy, r }
}

function blobPts(cx: number, cy: number, r: number, seed: number, amp: number, n: number): [number, number][] {
  const p1 = (seed % 628) / 100, p2 = ((seed >> 5) % 628) / 100, p3 = ((seed >> 11) % 628) / 100
  const pts: [number, number][] = []
  for (let i = 0; i < n; i++) {
    const a = (i / n) * Math.PI * 2
    const u = (Math.sin(2 * a + p1) * 0.5 + Math.sin(3 * a + p2) * 0.3 + Math.sin(5 * a + p3) * 0.2 + 1) / 2
    const rr = r * (1 - amp * Math.min(1, Math.max(0, u)))
    pts.push([cx + rr * Math.cos(a), cy + rr * Math.sin(a)])
  }
  return pts
}

/** Smooth closed organic outline: a circle whose radius wobbles inward (never past r*(1-amp)..r). */
export function blobPath(cx: number, cy: number, r: number, seed: number, amp = BLOB_AMP, n = 26): string {
  const pts = blobPts(cx, cy, r, seed, amp, n)
  const f = (v: number) => v.toFixed(1)
  let d = `M${f(pts[0][0])} ${f(pts[0][1])}`
  for (let i = 0; i < n; i++) {
    const p0 = pts[(i - 1 + n) % n], p1_ = pts[i], p2_ = pts[(i + 1) % n], p3_ = pts[(i + 2) % n]
    d += `C${f(p1_[0] + (p2_[0] - p0[0]) / 6)} ${f(p1_[1] + (p2_[1] - p0[1]) / 6)} ${f(p2_[0] - (p3_[0] - p1_[0]) / 6)} ${f(p2_[1] - (p3_[1] - p1_[1]) / 6)} ${f(p2_[0])} ${f(p2_[1])}`
  }
  return d + 'Z'
}

/** The same outline as blobPath, flattened to a polygon (x0, y0, x1, y1, …) for canvas/WebGL renderers. */
export function blobPolygon(cx: number, cy: number, r: number, seed: number, amp = BLOB_AMP, n = 26, sub = 3): number[] {
  const pts = blobPts(cx, cy, r, seed, amp, n)
  const out: number[] = []
  for (let i = 0; i < n; i++) {
    const p0 = pts[(i - 1 + n) % n], a = pts[i], b = pts[(i + 1) % n], p3 = pts[(i + 2) % n]
    const c1x = a[0] + (b[0] - p0[0]) / 6, c1y = a[1] + (b[1] - p0[1]) / 6
    const c2x = b[0] - (p3[0] - a[0]) / 6, c2y = b[1] - (p3[1] - a[1]) / 6
    for (let j = 0; j < sub; j++) {
      const t = j / sub, u = 1 - t
      out.push(u * u * u * a[0] + 3 * u * u * t * c1x + 3 * u * t * t * c2x + t * t * t * b[0], u * u * u * a[1] + 3 * u * u * t * c1y + 3 * u * t * t * c2y + t * t * t * b[1])
    }
  }
  return out
}

/** Island blob seed (shared by the SVG and WebGL renderers). */
export const islandSeed = (ns: string) => hash(ns)
/** Boxed app region seed. */
export const appSeed = (ns: string, app: string) => hash(ns + app)

/** Workloads in the map (before filters): the WebGL threshold compares against this. */
export const countWorkloads = (m: ClusterMap) => m.namespaces.reduce((a, n) => a + n.apps.reduce((b, ap) => b + ap.workloads.length, 0), 0)

/** Search result: matching tiles and islands, and where to zoom first. */
export interface Match { tiles: Set<number>; islands: Set<number>; islandsWithTiles: Set<number>; first?: { x: number; y: number; r: number; k?: number } }

export function computeMatch(l: Layout, q: string): Match | null {
  const s = q.trim().toLowerCase()
  if (!s) return null
  const m: Match = { tiles: new Set(), islands: new Set(), islandsWithTiles: new Set() }
  const byIsl = new Map(l.islands.map((i) => [i.ns.name, i]))
  for (const t of l.tiles) {
    if (t.w.name.toLowerCase().includes(s) || t.app.toLowerCase().includes(s) || t.w.kind.toLowerCase() === s) {
      m.tiles.add(t.id)
      const isl = byIsl.get(t.ns)
      if (isl) m.islandsWithTiles.add(isl.id)
      if (!m.first) m.first = { x: t.x, y: t.y, r: TILE_R, k: 1.5 }
    }
  }
  for (const i of l.islands) {
    if (i.ns.name.toLowerCase().includes(s)) {
      m.islands.add(i.id)
      if (!m.first) m.first = { x: i.x, y: i.y, r: i.r }
    }
  }
  for (const id of m.islandsWithTiles) m.islands.add(id)
  return m
}

/** Level of detail from the zoom: 0 nothing, 1 dot, 2 hexagon + icon, 3 + name and pod bar, 4 pod squares. */
export const lodOf = (k: number) => { const r = TILE_R * k; return r < 1 ? 0 : r < 11 ? 1 : r < 22 ? 2 : r < 48 ? 3 : 4 }
/** Dot radius (world units) at LOD 1, so a dot stays a few pixels wide. */
export const dotRadius = (k: number) => Math.min(30, Math.max(17, Math.ceil(1.6 / k / 4) * 4))

export function layoutMap(map: ClusterMap, o: LayoutOpts): Layout {
  const hasUser = map.namespaces.some((n) => !isSystemNs(n.name))
  const tiles: TileL[] = []
  const appsFlat: AppL[] = []
  const islands: IslandL[] = []
  const totals: Totals = { namespaces: 0, workloads: 0, pods: 0, apps: 0, services: 0 }

  for (const ns of map.namespaces) {
    if (o.selected.size && !o.selected.has(ns.name)) continue
    if (!o.selected.size && o.hideSystem && hasUser && isSystemNs(ns.name)) continue
    // Keep (and group) the workloads that pass the filter.
    const kept = ns.apps
      .map((a) => ({ name: a.name, ws: o.onlyProblems ? a.workloads.filter(hasProblem) : a.workloads }))
      .filter((a) => a.ws.length)
    if ((o.onlyProblems || o.hideEmpty) && !kept.length) continue
    const boxed = kept.filter((a) => a.ws.length > 1)
    const singles = kept.filter((a) => a.ws.length === 1)
    type Item = { r: number; app: AppL; cells: [number, number][]; encl: number }
    const items: Item[] = []
    const mk = (name: string, ws: { w: MapWorkload; app: string }[], isBoxed: boolean): Item => {
      const cells = hexCells(ws.length)
      let encl = 0
      for (const [x, y] of cells) encl = Math.max(encl, Math.hypot(x, y))
      encl += TILE_REACH
      const r = isBoxed ? (encl + 30) / (1 - 0.06) : encl
      const app: AppL = { id: -1, x: 0, y: 0, r, name, ns: ns.name, boxed: isBoxed, path: '', tiles: ws.map(({ w, app: an }, i) => ({ id: -1, x: cells[i][0], y: cells[i][1], w, ns: ns.name, app: an, key: `${ns.name}/${w.kind}/${w.name}` })) }
      return { r, app, cells, encl }
    }
    for (const a of boxed) items.push(mk(a.name, a.ws.map((w) => ({ w, app: a.name })), true))
    if (singles.length) items.push(mk('', singles.map((a) => ({ w: a.ws[0], app: a.name })), false))
    const pos = pack(items.map((i) => i.r), 26)
    const placed = items.map((it, i) => ({ x: pos[i][0], y: pos[i][1], r: it.r }))
    const enc = enclose(placed)
    const encR = Math.max(enc.r, 70)
    const f0 = Math.min(90, Math.max(18, 0.06 * (encR + 60)))
    const pad = 26 + f0 * 1.2, shift = f0 * 1.5
    // An empty namespace is a small island: present (it exists) but not competing for space.
    const empty = !kept.length
    const R = empty ? 64 : (encR + pad + shift) / (1 - BLOB_AMP)
    const isl: IslandL = { id: islands.length, x: 0, y: 0, r: R, ns, hue: hueOf(ns.name), path: '', font: empty ? 14 : f0, labelY: -R + f0 * 1.45 + 6, apps: [], workloads: 0, empty }
    // Item positions are relative to the island center (content shifted down to leave the header band).
    items.forEach((it, i) => {
      it.app.x = pos[i][0] - enc.cx
      it.app.y = pos[i][1] - enc.cy + shift
      for (const t of it.app.tiles) { t.x += it.app.x; t.y += it.app.y }
      isl.apps.push(it.app)
      isl.workloads += it.app.tiles.length
    })
    islands.push(isl)
    totals.namespaces++
    totals.workloads += isl.workloads
    totals.pods += ns.pods.total
    totals.apps += kept.length
    totals.services += ns.services
  }

  // Islands: biggest first at the center.
  const ipos = pack(islands.map((i) => i.r), 90)
  islands.forEach((isl, i) => {
    isl.x = ipos[i][0]
    isl.y = ipos[i][1]
  })
  let b: Bounds = { x0: 0, y0: 0, x1: 1, y1: 1 }
  if (islands.length) {
    b = { x0: Infinity, y0: Infinity, x1: -Infinity, y1: -Infinity }
    for (const i of islands) {
      b.x0 = Math.min(b.x0, i.x - i.r); b.y0 = Math.min(b.y0, i.y - i.r)
      b.x1 = Math.max(b.x1, i.x + i.r); b.y1 = Math.max(b.y1, i.y + i.r)
    }
  }
  // Absolute coordinates, ids and outlines.
  for (const isl of islands) {
    isl.path = blobPath(isl.x, isl.y, isl.r, islandSeed(isl.ns.name))
    isl.labelY += isl.y
    for (const a of isl.apps) {
      a.x += isl.x
      a.y += isl.y
      a.id = appsFlat.length
      appsFlat.push(a)
      if (a.boxed) a.path = blobPath(a.x, a.y, a.r, appSeed(isl.ns.name, a.name), 0.06, 20)
      for (const t of a.tiles) {
        t.x += isl.x
        t.y += isl.y
        t.id = tiles.length
        tiles.push(t)
      }
    }
  }
  return { islands, tiles, appsFlat, bounds: b, totals }
}
