import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, type MapWorkload } from '../../api'
import { clusterName, openPanel, useApp, useUi, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { openTopology } from '../topology/Topology'
import { ClusterGate, ErrorBanner, Partial, useAsync } from '../common'
import { kindIcon } from '../shell/nav'
import { useTheme } from '../shell/theme'
import { Activity, Filter, Globe, HardDrive, Info, Layers, Map as MapIcon, Maximize2, Minus, Network, Plus, RefreshCw, RotateCcw, Search, Server, Settings2, TriangleAlert } from 'lucide-react'
import { computeMatch, countWorkloads, dotRadius, DX, DY, hasProblem, isSystemNs, lodOf, TILE_R, type AppL, type IslandL, type Layout, type Match, type TileL } from './layout'
import { useMapLayout } from '../map-webgl/useMapLayout'
import { GlStage, type GlStageHandle } from '../map-webgl/GlStage'
import { arrowOf, placeEdges, type EdgeL } from '../map-webgl/edges'
import { useFpsProbe, webglAvailable } from '../map-webgl/probe'
import { useTrafficEdges } from '../map-webgl/trafficEdges'
import type { Hit } from '../map-webgl/scene'
import './map.css'
import '../map-webgl/mgl.css'

export function openMap(clusterId: string, opts?: OpenOptions) {
  openPanel(`map:${clusterId}`, 'map', 'Mapa do cluster', { clusterId }, opts)
}

interface View { x: number; y: number; k: number }
type Level = 'namespaces' | 'workloads' | 'pods'
const MAX_K = 14
const LEVELS: [Level, string][] = [['namespaces', 'Namespaces'], ['workloads', 'Workloads'], ['pods', 'Pods']]
const levelOf = (k: number): Level => (TILE_R * k < 11 ? 'namespaces' : TILE_R * k < 48 ? 'workloads' : 'pods')

const hexPath = (r: number, cx = 0, cy = 0) => Array.from({ length: 6 }, (_, i) => {
  const a = (Math.PI / 180) * (60 * i - 90)
  return `${i ? 'L' : 'M'}${(cx + r * Math.cos(a)).toFixed(2)} ${(cy + r * Math.sin(a)).toFixed(2)}`
}).join('') + 'Z'
const HEX = hexPath(TILE_R)
const HEX_INNER = hexPath(TILE_R * 0.84)
const HEX_RING = hexPath(TILE_R * 1.2)

const trunc = (s: string, n: number) => (s.length <= n ? s : n <= 1 ? '' : s.slice(0, n - 1) + '…')

/** Short kind tag shown on the tile (the full kind is in the tooltip and the legend). */
export const KIND_TAG: Record<string, string> = {
  Deployment: 'DEPLOY', StatefulSet: 'STS', DaemonSet: 'DS', CronJob: 'CRON', Job: 'JOB', Pod: 'POD',
  ReplicaSet: 'RS', ReplicationController: 'RC', DeploymentConfig: 'DC',
}
const kindTag = (k: string) => KIND_TAG[k] ?? k.slice(0, 4).toUpperCase()

/** Zoom quantized to quarter octaves: screen-sized text follows the zoom without re-rendering every tile on every frame. */
const quantK = (k: number) => Math.pow(2, Math.round(Math.log2(k) * 4) / 4)

let clipSeq = 0
function HealthBar({ x, y, w, h, c, text }: { x: number; y: number; w: number; h: number; c: { total: number; ok: number; warn: number; error: number }; text?: boolean }) {
  const [id] = useState(() => `mclip${++clipSeq}`)
  if (!c.total) return null
  const other = Math.max(0, c.total - c.ok - c.warn - c.error)
  let cx = x
  const seg = (n: number, cls: string) => {
    if (!n) return null
    const sw = (n / c.total) * w
    const el = <rect key={cls} className={`map-bar-seg ${cls}`} x={cx} y={y} width={sw} height={h} />
    cx += sw
    return el
  }
  return (
    <g pointerEvents="none">
      <clipPath id={id}><rect x={x} y={y} width={w} height={h} rx={h / 2} /></clipPath>
      <rect className="map-bar-bg" x={x} y={y} width={w} height={h} rx={h / 2} />
      <g clipPath={`url(#${id})`}>{seg(c.ok, 'ok')}{seg(c.warn, 'warn')}{seg(c.error, 'error')}{seg(other, 'other')}</g>
      {text && <text className="map-bar-txt" x={x + w / 2} y={y + h * 0.78} fontSize={h * 0.8}>{c.total}</text>}
    </g>
  )
}

// Honeycomb cells (axial q, r) in spiral order from the center, up to 6 rings (127 cells).
const HIVE: [number, number][] = (() => {
  const cells: [number, number, number, number][] = []
  for (let q = -6; q <= 6; q++) {
    for (let r = Math.max(-6, -q - 6); r <= Math.min(6, -q + 6); r++) {
      const x = q + r / 2, y = r * 0.866
      cells.push([q, r, Math.max(Math.abs(q), Math.abs(r), Math.abs(q + r)), Math.atan2(y, x)])
    }
  }
  cells.sort((a, b) => a[2] - b[2] || a[3] - b[3])
  return cells.map(([q, r]) => [q + r / 2, r * 0.866])
})()
const HIVE_CAP = HIVE.length

/** Pods as a honeycomb of small hexagons inside the workload tile: problems at the center, scaled past 127 pods. */
function PodHive({ w }: { w: MapWorkload }) {
  const p = w.pods
  if (!p.total) return <text className="map-nopods" y={6} fontSize={7}>sem pods</text>
  const sc = p.total > HIVE_CAP ? HIVE_CAP / p.total : 1
  const other = Math.max(0, p.total - p.ok - p.warn - p.error)
  const groups: [string, number][] = [['error', p.error], ['warn', p.warn], ['ok', p.ok], ['other', other]]
  const counts = groups.map(([c, n]) => [c, n ? Math.max(1, Math.round(n * sc)) : 0] as [string, number])
  const n = Math.min(HIVE_CAP, counts.reduce((a, [, v]) => a + v, 0))
  let rings = 0
  while (1 + 3 * rings * (rings + 1) < n) rings++
  const d = Math.min(12.5, 17.5 / (rings + 0.6)), m = d * 0.54
  const paths: Record<string, string> = {}
  let i = 0
  for (const [c, cnt] of counts) {
    for (let j = 0; j < cnt && i < n; j++, i++) paths[c] = (paths[c] ?? '') + hexPath(m, HIVE[i][0] * d, 4 + HIVE[i][1] * d)
  }
  return <g pointerEvents="none">{Object.entries(paths).map(([c, dd]) => <path key={c} className={`map-pod ${c}`} d={dd} />)}</g>
}

/** Small icon + count chips under a tile at the closest zoom (services, ingress hosts, PVCs, restarts). */
function Chips({ w, y, fs }: { w: MapWorkload; y: number; fs: number }) {
  const items: [typeof Network, string, string][] = []
  if (w.services?.length) items.push([Network, String(w.services.length), 'svc'])
  if (w.hosts?.length) items.push([Globe, String(w.hosts.length), 'ing'])
  if (w.pvcs) items.push([HardDrive, String(w.pvcs), 'pvc'])
  if (w.restarts) items.push([RotateCcw, String(w.restarts), 'rst'])
  if (!items.length) return null
  const cw = (t: string) => fs * 1.25 + t.length * fs * 0.62 + fs * 0.5
  const total = items.reduce((a, [, t]) => a + cw(t), 0) + (items.length - 1) * fs * 0.35
  let x = -total / 2
  return (
    <g className="map-chips" pointerEvents="none">
      {items.map(([Icon, t, cls]) => {
        const x0 = x
        x += cw(t) + fs * 0.35
        return (
          <g key={cls} className={`map-chip ${cls}`}>
            <rect x={x0} y={y - fs * 0.85} width={cw(t)} height={fs * 1.5} rx={fs * 0.75} />
            <Icon x={x0 + fs * 0.3} y={y - fs * 0.55} width={fs * 0.9} height={fs * 0.9} strokeWidth={2.2} />
            <text x={x0 + fs * 1.35} y={y + fs * 0.32} fontSize={fs}>{t}</text>
          </g>
        )
      })}
    </g>
  )
}

// lod: 1 dot, 2 hexagon + icon (+ problem badge), 3 + name and pod bar, 4 pods honeycomb + kind tag (+ details as room allows).
const Tile = memo(function Tile({ t, lod, kq, dr, dim, match, sel }: { t: TileL; lod: number; kq: number; dr: number; dim: boolean; match: boolean; sel: boolean }) {
  const w = t.w
  if (lod === 1) return <circle className={`map-dot ${w.health}`} data-tile={t.id} data-testid={`map-tile-${w.name}`} cx={t.x} cy={t.y} r={dr} opacity={dim ? 0.2 : 1} />
  const Icon = kindIcon(w.kind)
  const problem = w.health === 'error' || w.pods.error ? 'error' : w.health === 'warn' || w.pods.warn ? 'warn' : ''
  // Text and bars are sized in screen pixels (divided by the zoom), capped so they never outgrow the tile.
  const px = (n: number) => n / kq
  const fs = Math.min(11, px(12.5))
  const under = (DY - 2 * TILE_R) * kq // screen pixels between this tile's bottom and the next row
  const barH = Math.min(6, Math.max(0.4, px(5)))
  const barY = TILE_R + Math.min(6, px(6))
  const nameY = barY + barH + fs * 1.2
  const subFs = Math.min(9.5, px(11))
  const showSub = lod >= 4 && under >= 50
  const showChips = lod >= 4 && under >= 66
  const badgeR = Math.min(8, px(11))
  const tagFs = Math.min(6.4, px(10)), tag = kindTag(w.kind), tagW = tag.length * tagFs * 0.66 + tagFs * 1.25
  return (
    <g className={`map-tile${match ? ' match' : ''}${dim ? ' map-dim' : ''}${sel ? ' sel' : ''}${problem ? ` p-${problem}` : ''}`} data-tile={t.id} data-testid={`map-tile-${w.name}`} transform={`translate(${t.x} ${t.y})`}>
      {sel && <path className="selring" d={HEX_RING} />}
      {problem && <path className={`halo ${problem}`} d={HEX_RING} />}
      <path className={`hex ${w.health}`} d={HEX} />
      <path className="sheen" d={HEX} />
      <path className="inner" d={HEX_INNER} />
      {lod < 4 && <Icon className="ico" x={-11} y={-11} size={22} strokeWidth={1.7} />}
      {lod >= 4 && (
        <>
          <Icon className="ico" x={-5.5} y={-23} size={11} strokeWidth={2} />
          <PodHive w={w} />
          <g className="kindtag" pointerEvents="none">
            <rect x={-tagW / 2} y={-TILE_R - tagFs * 0.7} width={tagW} height={tagFs * 1.4} rx={tagFs * 0.7} />
            <text y={-TILE_R + tagFs * 0.36} fontSize={tagFs}>{tag}</text>
          </g>
        </>
      )}
      {problem && (
        <g className={`badge ${problem}`} transform={`translate(${TILE_R * 0.78} ${-TILE_R * 0.62})`} pointerEvents="none">
          <circle r={badgeR} />
          <text y={badgeR * 0.42} fontSize={badgeR * 1.3}>!</text>
        </g>
      )}
      {lod >= 3 && (
        <>
          <HealthBar x={-20} y={barY} w={40} h={barH} c={w.pods} text={lod >= 4 && barH * kq >= 9} />
          <text className="name" y={nameY} fontSize={fs}>{trunc(w.name, Math.floor((DX * 0.94) / (fs * 0.58)))}</text>
          {showSub && <text className="sub" y={nameY + subFs * 1.35} fontSize={subFs}>{trunc(subOf(w), Math.floor((DX * 0.94) / (subFs * 0.55)))}</text>}
          {showChips && <Chips w={w} y={nameY + subFs * 1.35 + subFs * 1.7} fs={subFs * 0.9} />}
        </>
      )}
    </g>
  )
})

function subOf(w: MapWorkload) {
  const r = w.ready ? (w.kind === 'CronJob' || w.kind === 'Job' ? w.ready : `${w.ready} prontos`) : `${w.pods.total} pod${w.pods.total === 1 ? '' : 's'}`
  return w.status && w.health !== 'ok' && w.health !== 'neutral' ? `${r} · ${w.status}` : r
}

interface VP { x0: number; y0: number; x1: number; y1: number }
const seen = (vp: VP, x: number, y: number, r: number) => x + r >= vp.x0 && x - r <= vp.x1 && y + r >= vp.y0 && y - r <= vp.y1

const Island = memo(function Island({ isl, k, vp, lod, dr, match, sel }: { isl: IslandL; k: number; vp: VP; lod: number; dr: number; match: Match | null; sel: string }) {
  const c = isl.ns.pods
  const lf = Math.min(Math.max(isl.font, 13 / k), 24 / k, isl.r * (isl.empty ? 0.26 : 0.2))
  const cw = lf * 0.7 // uppercase bold with letter spacing
  // The name may run a little past the outline (on its pill) before it is truncated.
  const maxChars = Math.floor((isl.r * 2.3) / cw)
  const name = trunc(isl.ns.name.toUpperCase(), maxChars)
  const pillW = name.length * cw + lf * 1.3, pillH = lf * 1.6
  const pillY = isl.empty ? isl.y - pillH * 0.75 : isl.y - isl.r * 0.74
  const showLabel = maxChars >= 3 && lf * k >= 7
  const metaFs = lf * 0.56
  const showMeta = showLabel && metaFs * k >= 9.5
  const metaY = pillY + pillH / 2 + metaFs * 1.45
  const bw = Math.min(isl.r * 0.9, Math.max(lf * 6, 60)), bh = Math.max(lf * 0.26, 3 / k)
  const barY = showMeta ? metaY + metaFs * 0.6 : pillY + pillH / 2 + lf * 0.35
  const inner = isl.r * k > 25
  const dimIsl = match && !match.islands.has(isl.id)
  const meta = isl.empty ? 'sem workloads' : `${isl.workloads} workload${isl.workloads === 1 ? '' : 's'} · ${c.total} pod${c.total === 1 ? '' : 's'}`
  return (
    <g className={`map-island${isl.empty ? ' empty' : ''}`} data-testid={`map-island-${isl.ns.name}`} style={{ '--h': isl.hue } as React.CSSProperties} opacity={dimIsl && !match!.islandsWithTiles.has(isl.id) ? 0.35 : 1}>
      <title>{isl.ns.name}</title>
      <path className="halo" d={isl.path} />
      <path className="blob" data-isl={isl.id} d={isl.path} />
      <path className="glow" d={isl.path} />
      <path className="hexfill" d={isl.path} />
      {showLabel && (
        <g className="nshead" pointerEvents="none">
          <rect className="nspill" x={isl.x - pillW / 2} y={pillY - pillH / 2} width={pillW} height={pillH} rx={pillH / 2} />
          <text className="nslabel" x={isl.x} y={pillY + lf * 0.36} fontSize={lf}>{name}</text>
          {showMeta && <text className="nsmeta" x={isl.x} y={metaY} fontSize={metaFs}>{meta}</text>}
        </g>
      )}
      {showLabel && <HealthBar x={isl.x - bw / 2} y={barY} w={bw} h={bh} c={c} text={bh * k >= 9} />}
      {inner && isl.apps.map((a) => <AppView key={a.id} a={a} k={k} vp={vp} lod={lod} dr={dr} match={match} sel={sel} />)}
    </g>
  )
})

const AppView = memo(function AppView({ a, k, vp, lod, dr, match, sel }: { a: AppL; k: number; vp: VP; lod: number; dr: number; match: Match | null; sel: string }) {
  if (!seen(vp, a.x, a.y, a.r)) return null
  const showRegion = a.boxed && a.r * k >= 18
  const lf = Math.min(Math.max(12, 11 / k), a.r * 0.14)
  const showLabel = a.boxed && a.r * k >= 70 && !!a.name
  const kq = quantK(k)
  return (
    <g>
      {showRegion && <path className="app" data-app={a.id} d={a.path} />}
      {showLabel && <text className="applabel" x={a.x} y={a.y - a.r + lf * 1.25 + 2} fontSize={lf}>{trunc(a.name, Math.floor((a.r * 1.5) / (lf * 0.7)))}</text>}
      {lod > 0 && a.tiles.map((t) => (seen(vp, t.x, t.y, 48) ? <Tile key={t.id} t={t} lod={lod} kq={kq} dr={dr} dim={!!match && !match.tiles.has(t.id)} match={!!match && match.tiles.has(t.id)} sel={sel === t.key} /> : null))}
    </g>
  )
})

/** Overview in the corner: islands and the visible area; click or drag to move there. */
const MiniMap = memo(function MiniMap({ layout, rectRef, onJump }: { layout: Layout; rectRef: React.RefObject<SVGRectElement | null>; onJump: (x: number, y: number) => void }) {
  const b = layout.bounds
  const pad = (b.x1 - b.x0) * 0.04
  const vb = `${b.x0 - pad} ${b.y0 - pad} ${b.x1 - b.x0 + 2 * pad} ${b.y1 - b.y0 + 2 * pad}`
  const ratio = (b.y1 - b.y0 + 2 * pad) / (b.x1 - b.x0 + 2 * pad)
  const W = 176, H = Math.max(70, Math.min(150, W * ratio))
  const svg = useRef<SVGSVGElement>(null)
  const down = useRef(false)
  const jump = (e: React.PointerEvent) => {
    const m = svg.current?.getScreenCTM()
    if (!m) return
    const p = new DOMPoint(e.clientX, e.clientY).matrixTransform(m.inverse())
    onJump(p.x, p.y)
  }
  const stop = (e: React.SyntheticEvent) => e.stopPropagation()
  return (
    <div className="map-mini" data-testid="map-minimap" onPointerDown={(e) => { stop(e); down.current = true; (e.target as Element).setPointerCapture?.(e.pointerId); jump(e) }}
      onPointerMove={(e) => { stop(e); if (down.current) jump(e) }} onPointerUp={(e) => { stop(e); down.current = false }} onClick={stop} onDoubleClick={stop}>
      <svg ref={svg} width={W} height={H} viewBox={vb} preserveAspectRatio="xMidYMid meet">
        {layout.islands.map((i) => (
          <g key={i.id} style={{ '--h': i.hue } as React.CSSProperties} className={`mini-isl${i.empty ? ' empty' : ''}`}>
            <circle cx={i.x} cy={i.y} r={i.r * 0.95} />
            {i.apps.flatMap((a) => a.tiles).filter((t) => t.w.health === 'error' || t.w.health === 'warn' || t.w.pods.error > 0).map((t) => (
              <circle key={t.id} className={`mini-p ${t.w.health === 'warn' && !t.w.pods.error ? 'warn' : 'error'}`} cx={t.x} cy={t.y} r={Math.max(i.r * 0.06, (b.x1 - b.x0) * 0.008)} />
            ))}
          </g>
        ))}
        <rect ref={rectRef} className="mini-view" />
      </svg>
    </div>
  )
})

/** Traffic edges in world units; stroke and arrow sizes stay in screen pixels (divided by the zoom). */
const SvgEdges = memo(function SvgEdges({ edges, k }: { edges: EdgeL[]; k: number }) {
  if (!edges.length) return null
  return (
    <g className="mgl-edges" data-testid="map-edges">
      {edges.map((e, i) => {
        const a = arrowOf(e, k)
        return (
          <g key={i} data-testid="map-edge">
            <title>{e.label}</title>
            <path className="mgl-edge" d={`M${e.x0} ${e.y0}L${a.ex} ${a.ey}`} strokeWidth={e.w / k} />
            <path className="mgl-arrow" d={`M${a.tip[0]} ${a.tip[1]}L${a.tip[2]} ${a.tip[3]}L${a.tip[4]} ${a.tip[5]}Z`} />
          </g>
        )
      })}
    </g>
  )
})

export function MapPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <MapView clusterId={params.clusterId} />
    </ClusterGate>
  )
}

function MapView({ clusterId }: { clusterId: string }) {
  const nsParam = useApp((s) => s.namespace[clusterId] ?? '')
  const theme = useTheme((s) => s.theme)
  const { data, error, loading, reload } = useAsync(() => api.clusterMap(clusterId, nsParam || undefined), [clusterId, nsParam], 30000)
  // Identical refreshes keep the same object, so the layout is not recomputed and the view does not move.
  const sig = data ? JSON.stringify(data) : ''
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const map = useMemo(() => data, [sig])

  const [q, setQ] = useState('')
  const [onlyProblems, setOnlyProblems] = useState(false)
  const [hideSystem, setHideSystem] = useState(true)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [nsOpen, setNsOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [traffic, setTraffic] = useState(false)
  const [sel, setSel] = useState('')
  const [legend, setLegend] = useState(false)
  const hideEmpty = useUi((s) => s.mapHideEmpty)
  const minimap = useUi((s) => s.mapMinimap)

  // Renderer: SVG below the threshold, WebGL at/above it (SVG with a notice when WebGL cannot start).
  const threshold = useUi((s) => s.mapWebglThreshold)
  const total = useMemo(() => (map ? countWorkloads(map) : 0), [map])
  const big = total >= threshold
  const [glFail, setGlFail] = useState<string | null>(null)
  const renderer: 'svg' | 'webgl' = big && !glFail && webglAvailable() ? 'webgl' : 'svg'
  const [glReady, setGlReady] = useState(false)
  useEffect(() => { if (renderer !== 'webgl') setGlReady(false) }, [renderer])

  const opts = useMemo(() => ({ selected, hideSystem, onlyProblems, hideEmpty }), [selected, hideSystem, onlyProblems, hideEmpty])
  const { layout, pending } = useMapLayout(map, opts, big)
  const match = useMemo(() => (layout ? computeMatch(layout, q) : null), [layout, q])
  // The stage exists once there is a layout (with the worker it arrives after the map).
  const ready = !!map && !!layout

  const trafficQ = useTrafficEdges(clusterId, nsParam || undefined, traffic)
  const placed = useMemo(() => placeEdges(layout, trafficQ.data), [layout, trafficQ.data])
  const fpsRef = useFpsProbe<HTMLDivElement>()
  const glRef = useRef<GlStageHandle>(null)

  const stageRef = useRef<HTMLDivElement>(null)
  const gRef = useRef<SVGGElement>(null)
  const zoomRef = useRef<HTMLDivElement>(null)
  const tipRef = useRef<HTMLDivElement>(null)
  const miniRef = useRef<SVGRectElement>(null)
  const viewRef = useRef<View>({ x: 0, y: 0, k: 1 })
  const [view, setView] = useState<View>(viewRef.current)
  const [size, setSize] = useState({ w: 0, h: 0 })
  const syncTimer = useRef<number>(0)
  const anim = useRef<number>(0)
  const dragged = useRef(0)
  const [hover, setHover] = useState<{ kind: 'tile' | 'isl' | 'app'; id: number } | null>(null)
  const [panning, setPanning] = useState(false)

  const apply = useCallback(() => {
    const v = viewRef.current
    gRef.current?.setAttribute('transform', `translate(${v.x} ${v.y}) scale(${v.k})`)
    glRef.current?.setView(v)
    if (zoomRef.current) zoomRef.current.textContent = `${Math.round(v.k * 100)}%`
    const st = stageRef.current
    if (st) {
      // The dotted backdrop pans with the map (a cheap depth cue).
      st.style.backgroundPosition = `${v.x}px ${v.y}px`
      const r = miniRef.current
      if (r) {
        r.setAttribute('x', String(-v.x / v.k)); r.setAttribute('y', String(-v.y / v.k))
        r.setAttribute('width', String(st.clientWidth / v.k)); r.setAttribute('height', String(st.clientHeight / v.k))
      }
    }
    if (!syncTimer.current) {
      syncTimer.current = window.setTimeout(() => {
        syncTimer.current = 0
        setView({ ...viewRef.current })
      }, 80)
    }
  }, [])

  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setSize({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [ready])
  useEffect(() => () => { window.clearTimeout(syncTimer.current); cancelAnimationFrame(anim.current) }, [])
  useEffect(() => { if (minimap) apply() }, [minimap, layout, size, apply])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setSel('') }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const animateTo = useCallback((to: View, ms = 380) => {
    cancelAnimationFrame(anim.current)
    const from = { ...viewRef.current }
    if (!ms) {
      viewRef.current = to
      apply()
      return
    }
    const t0 = performance.now()
    const step = (now: number) => {
      const p = Math.min(1, (now - t0) / ms), e = 1 - Math.pow(1 - p, 3)
      // Interpolate the zoom in log space so it feels even.
      const k = Math.exp(Math.log(from.k) + (Math.log(to.k) - Math.log(from.k)) * e)
      viewRef.current = { k, x: from.x + (to.x - from.x) * e, y: from.y + (to.y - from.y) * e }
      apply()
      if (p < 1) anim.current = requestAnimationFrame(step)
    }
    anim.current = requestAnimationFrame(step)
  }, [apply])

  const centerOn = useCallback((wx: number, wy: number, k: number): View => ({ k, x: size.w / 2 - wx * k, y: size.h / 2 - wy * k }), [size])
  const fit = useCallback((ms = 380) => {
    if (!layout || !layout.islands.length || !size.w) return
    const b = layout.bounds
    const bw = b.x1 - b.x0, bh = b.y1 - b.y0
    const k = Math.min(1.2, Math.min(size.w / bw, size.h / bh) * 0.92)
    animateTo(centerOn((b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2, k), ms)
  }, [layout, size, animateTo, centerOn])
  const minK = useMemo(() => {
    if (!layout || !layout.islands.length || !size.w) return 0.01
    const b = layout.bounds
    return Math.min(size.w / (b.x1 - b.x0), size.h / (b.y1 - b.y0)) * 0.4
  }, [layout, size])
  const zoomAt = useCallback((factor: number, px: number, py: number, ms = 0) => {
    const v = viewRef.current
    const k = Math.min(MAX_K, Math.max(minK, v.k * factor))
    const to = { k, x: px - (px - v.x) * (k / v.k), y: py - (py - v.y) * (k / v.k) }
    if (ms) animateTo(to, ms)
    else {
      cancelAnimationFrame(anim.current)
      viewRef.current = to
      apply()
    }
  }, [minK, apply, animateTo])
  const setLevel = (l: Level) => {
    if (l === 'namespaces') return fit()
    const target = l === 'workloads' ? 1.0 : 2.4
    const v = viewRef.current
    zoomAt(target / v.k, size.w / 2, size.h / 2, 380)
  }

  // Refit when the shown set changes (cluster, filters), not on a refresh.
  const fitKey = `${clusterId}|${hideSystem}|${onlyProblems}|${[...selected].sort().join(',')}`
  const lastFit = useRef('')
  useEffect(() => {
    if (!layout || pending || !layout.islands.length || !size.w || lastFit.current === fitKey) return
    lastFit.current = fitKey
    fit(0)
  }, [layout, pending, size, fitKey, fit])

  const zoomToBox = useCallback((x: number, y: number, r: number, maxK = MAX_K) => {
    const k = Math.min(maxK, (Math.min(size.w, size.h) * 0.42) / r)
    animateTo(centerOn(x, y, Math.max(minK, k)))
  }, [size, animateTo, centerOn, minK])

  // Search: zoom to the first match once the typing settles.
  const lastQ = useRef('')
  useEffect(() => {
    if (!match?.first || lastQ.current === q) return
    const f = match.first
    const t = setTimeout(() => {
      lastQ.current = q
      if (f.k) animateTo(centerOn(f.x, f.y, f.k))
      else zoomToBox(f.x, f.y, f.r)
    }, 450)
    return () => clearTimeout(t)
  }, [match, q, animateTo, centerOn, zoomToBox])
  useEffect(() => { if (!q) lastQ.current = '' }, [q])

  // Wheel zoom needs a non-passive listener.
  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      zoomAt(Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0016)), e.clientX - r.left, e.clientY - r.top)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [zoomAt, ready])

  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    cancelAnimationFrame(anim.current)
    drag.current = { x: e.clientX, y: e.clientY, vx: viewRef.current.x, vy: viewRef.current.y }
    dragged.current = 0
  }
  const hit = (e: { target: EventTarget | null; clientX: number; clientY: number }): Hit | null => {
    if (renderer === 'webgl') {
      const r = stageRef.current?.getBoundingClientRect()
      return r ? glRef.current?.hitAt(e.clientX - r.left, e.clientY - r.top) ?? null : null
    }
    const el = (e.target as Element | null)?.closest?.('[data-tile],[data-app],[data-isl]') as HTMLElement | null
    if (!el) return null
    if (el.dataset.tile !== undefined) return { kind: 'tile' as const, id: +el.dataset.tile }
    if (el.dataset.app !== undefined) return { kind: 'app' as const, id: +el.dataset.app }
    return { kind: 'isl' as const, id: +el.dataset.isl! }
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = drag.current
    if (d) {
      const dx = e.clientX - d.x, dy = e.clientY - d.y
      dragged.current = Math.max(dragged.current, Math.hypot(dx, dy))
      if (dragged.current > 4) {
        if (!panning) setPanning(true)
        viewRef.current = { ...viewRef.current, x: d.vx + dx, y: d.vy + dy }
        apply()
        if (hover) setHover(null)
      }
      return
    }
    const h = hit(e)
    if (h?.kind !== hover?.kind || h?.id !== hover?.id) setHover(h)
    const tip = tipRef.current, st = stageRef.current
    if (tip && st) {
      const r = st.getBoundingClientRect()
      tip.style.left = Math.min(e.clientX - r.left + 14, r.width - 290) + 'px'
      tip.style.top = Math.min(e.clientY - r.top + 14, r.height - 150) + 'px'
    }
  }
  const onPointerUp = () => {
    drag.current = null
    if (panning) setPanning(false)
  }
  const onClick = (e: React.MouseEvent) => {
    if (dragged.current > 4 || !layout) return
    const h = hit(e)
    if (!h || h.kind !== 'tile') setSel('')
    if (!h) return
    if (h.kind === 'tile') {
      const t = layout.tiles[h.id]
      setSel(t.key)
      openObject(clusterId, t.w.group, t.w.kind, t.w.name, t.ns, { preview: true })
    } else if (h.kind === 'app') {
      const a = layout.appsFlat[h.id]
      zoomToBox(a.x, a.y, a.r)
    } else {
      const i = layout.islands[h.id]
      zoomToBox(i.x, i.y, i.r)
    }
  }
  const onDoubleClick = (e: React.MouseEvent) => {
    if (!layout) return
    const h = hit(e)
    if (h?.kind === 'tile') openTopology(clusterId, layout.tiles[h.id].ns)
    else if (h?.kind === 'isl') openTopology(clusterId, layout.islands[h.id].ns.name)
    else if (h?.kind === 'app') openTopology(clusterId, layout.appsFlat[h.id].ns)
  }

  // Visible world rectangle (with a margin so a short pan does not reveal blanks before the next sync).
  const vp = useMemo<VP>(() => {
    const m = 0.5
    return { x0: (-view.x - size.w * m) / view.k, y0: (-view.y - size.h * m) / view.k, x1: (-view.x + size.w * (1 + m)) / view.k, y1: (-view.y + size.h * (1 + m)) / view.k }
  }, [view, size])
  const lod = lodOf(view.k)
  const dr = dotRadius(view.k)
  const level = levelOf(view.k)

  const hoverNode = useMemo(() => {
    if (!hover || !layout) return null
    if (hover.kind === 'tile') {
      const t = layout.tiles[hover.id]
      if (!t) return null
      const w = t.w
      const Icon = kindIcon(w.kind)
      return (
        <>
          <div className="tip-head">
            <span className={`tip-ico ${w.health}`}><Icon size={14} /></span>
            <div className="tip-title"><h5>{w.name}</h5><span>{w.kind} · {t.ns}</span></div>
            <span className={`tip-health ${w.health}`}>{HEALTH_LABEL[w.health] ?? w.health}</span>
          </div>
          <PodStrip c={w.pods} />
          {t.app && t.app !== w.name && <Row k="App" v={t.app} />}
          {w.status && <Row k="Status" v={w.status} />}
          {w.ready && <Row k="Prontos" v={w.ready} />}
          {!!w.restarts && <Row k="Reinícios" v={String(w.restarts)} warn={w.restarts >= 5} />}
          {!!w.services?.length && <Row k="Services" v={w.services.join(', ')} />}
          {!!w.hosts?.length && <Row k="Ingress" v={w.hosts.join(', ')} />}
          {!!w.pvcs && <Row k="PVCs" v={String(w.pvcs)} />}
          {map?.compact && <div className="muted small" style={{ marginTop: 4 }}>Mapa grande: status e services no painel do objeto</div>}
          <div className="tip-hint">Clique para os detalhes · duplo clique para a topologia</div>
        </>
      )
    }
    if (hover.kind === 'app') {
      const a = layout.appsFlat[hover.id]
      const pods = a.tiles.reduce((x, t) => ({ total: x.total + t.w.pods.total, ok: x.ok + t.w.pods.ok, warn: x.warn + t.w.pods.warn, error: x.error + t.w.pods.error }), { total: 0, ok: 0, warn: 0, error: 0 })
      return (
        <>
          <div className="tip-head"><div className="tip-title"><h5>{a.name}</h5><span>app em {a.ns}</span></div></div>
          <PodStrip c={pods} />
          <Row k="Workloads" v={String(a.tiles.length)} />
          <div className="tip-hint">Clique para ampliar</div>
        </>
      )
    }
    const i = layout.islands[hover.id]
    if (!i) return null
    const n = i.ns
    return (
      <>
        <div className="tip-head">
          <span className="tip-ico ns" style={{ '--h': i.hue } as React.CSSProperties}><Server size={14} /></span>
          <div className="tip-title"><h5>{n.name}</h5><span>namespace</span></div>
          {n.status && <span className={`tip-health ${n.status === 'Active' ? 'ok' : 'warn'}`}>{n.status}</span>}
        </div>
        <PodStrip c={n.pods} />
        <Row k="Workloads" v={String(i.workloads)} />
        <Row k="Pods" v={`${n.pods.total} (${n.pods.ok} ok · ${n.pods.warn} atenção · ${n.pods.error} erro)`} />
        <Row k="Services" v={String(n.services)} /><Row k="Ingresses" v={String(n.ingresses)} /><Row k="PVCs" v={String(n.pvcs)} />
        <div className="tip-hint">Clique para ampliar · duplo clique para a topologia</div>
      </>
    )
  }, [hover, layout, map])

  if (error && !map) return <ErrorBanner error={error} />
  if (!map || !layout) return <div className="pad"><span className="spinner" /></div>

  const podTotals = layout.islands.reduce((a, i) => ({ ok: a.ok + i.ns.pods.ok, warn: a.warn + i.ns.pods.warn, error: a.error + i.ns.pods.error }), { ok: 0, warn: 0, error: 0 })
  const scope = map.scope === 'cluster' ? clusterName(clusterId) : `namespace ${map.scope}`
  const allNs = map.namespaces.map((n) => n.name).sort()
  const hasUserNs = map.namespaces.some((n) => !isSystemNs(n.name))
  const nProblems = map.namespaces.reduce((a, n) => a + n.apps.reduce((b, ap) => b + ap.workloads.filter(hasProblem).length, 0), 0)

  return (
    <div className="map-panel" data-testid="map-panel" data-theme={theme}>
      <div className="map-bar">
        <span className="grid-title"><Layers size={16} />Mapa do cluster</span>
        <label className="map-search">
          <Search size={14} />
          <input data-testid="map-search" placeholder="Buscar workload, app, ns…" value={q} onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter' && match?.first) { lastQ.current = q; match.first.k ? animateTo(centerOn(match.first.x, match.first.y, match.first.k)) : zoomToBox(match.first.x, match.first.y, match.first.r) } if (e.key === 'Escape') setQ('') }} />
        </label>
        {q.trim() && <span className="map-count" data-testid="map-matches">{match ? match.tiles.size + (match.islands.size - match.islandsWithTiles.size) : 0} resultado(s)</span>}
        <button className="map-toggle" data-testid="map-ns-filter" onClick={() => setNsOpen((o) => !o)}><Server size={14} /> Namespaces{selected.size ? ` (${selected.size})` : ''}</button>
        <button className={`map-toggle${hideSystem && hasUserNs ? ' on' : ''}`} data-testid="map-hide-system" onClick={() => setHideSystem((v) => !v)} title="Oculta kube-*, openshift-*">Ocultar sistema</button>
        <button className={`map-toggle${onlyProblems ? ' on' : ''}`} data-testid="map-problems" onClick={() => setOnlyProblems((v) => !v)}><TriangleAlert size={14} /> Só com problemas{nProblems ? ` (${nProblems})` : ''}</button>
        <button className={`map-toggle${traffic ? ' on' : ''}`} data-testid="map-traffic" onClick={() => setTraffic((v) => !v)} title="Arestas de tráfego observado (Caretta ou Hubble), com volume e direção">
          <Activity size={14} /> Tráfego{traffic && placed.edges.length ? ` (${placed.edges.length})` : ''}
        </button>
        {map.truncated && <span className="badge badge-ro" title="O mapa mostra os primeiros workloads">truncado</span>}
        <span className="spacer" />
        {(loading || pending) && <span className="spinner" />}
        <button onClick={reload} data-testid="map-refresh"><RefreshCw size={14} /> Recarregar</button>
        <button className="map-toggle" data-testid="map-settings" title="Configurações do mapa" aria-label="Configurações do mapa" onClick={() => setSettingsOpen((o) => !o)}><Settings2 size={14} /></button>
      </div>
      {settingsOpen && (
        <>
          <div className="map-away" onClick={() => setSettingsOpen(false)} />
          <MapSettings threshold={threshold} renderer={renderer} total={total} onClose={() => setSettingsOpen(false)} />
        </>
      )}
      <Partial errors={map.errors} />
      {error && <ErrorBanner error={error} />}
      {nsOpen && (
        <>
          <div className="map-away" onClick={() => setNsOpen(false)} />
          <div className="map-nspop" data-testid="map-ns-pop">
            <div className="head"><b>Namespaces</b><button onClick={() => setSelected(new Set())}>Todos</button></div>
            <div className="list">
              {allNs.map((n) => (
                <label key={n}>
                  <input type="checkbox" checked={selected.has(n)} onChange={() => setSelected((s) => { const x = new Set(s); x.has(n) ? x.delete(n) : x.add(n); return x })} />
                  <span className="ellipsis">{n}</span>
                  <span className="n">{map.namespaces.find((x) => x.name === n)?.pods.total}</span>
                </label>
              ))}
            </div>
          </div>
        </>
      )}
      <div ref={stageRef} className={`map-stage${panning ? ' panning' : ''}`} data-testid="map-stage"
        onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerLeave={() => { onPointerUp(); setHover(null) }}
        onClick={onClick} onDoubleClick={onDoubleClick}>
        <div ref={fpsRef} className="mgl-renderer" data-testid="map-renderer" data-kind={renderer} data-ready={(renderer === 'webgl' ? glReady : true) ? '1' : '0'}
          data-workloads={total} data-edges={placed.edges.length}>
          {renderer === 'svg' ? (
            <svg className="map-svg">
              <defs>
                <radialGradient id="map-glow" cx="50%" cy="38%" r="65%">
                  <stop offset="0" className="map-glow-a" />
                  <stop offset="1" className="map-glow-b" />
                </radialGradient>
                <linearGradient id="map-sheen" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0" className="map-sheen-a" />
                  <stop offset=".55" className="map-sheen-b" />
                </linearGradient>
                <pattern id="map-hex" width="34.64" height="60" patternUnits="userSpaceOnUse">
                  <path className="map-hexpat" d="M17.32 0L34.64 10V30L17.32 40L0 30V10ZM17.32 40V60" />
                </pattern>
              </defs>
              <g ref={gRef} transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
                {layout.islands.map((isl) => (seen(vp, isl.x, isl.y, isl.r) ? <Island key={isl.ns.name} isl={isl} k={view.k} vp={vp} lod={lod} dr={dr} match={match} sel={sel} /> : null))}
                <SvgEdges edges={placed.edges} k={view.k} />
              </g>
            </svg>
          ) : (
            <GlStage handle={glRef} layout={layout} match={match} edges={placed.edges} hover={hover} view={view} current={() => viewRef.current} size={size} theme={theme}
              onFail={(r) => setGlFail(r)} onReady={() => setGlReady(true)} />
          )}
        </div>
        <div className="mgl-notes">
          {big && renderer === 'svg' && (
            <div className="mgl-note warn" data-testid="map-webgl-fallback">
              <TriangleAlert size={14} />
              <span>WebGL indisponível{glFail ? ` (${glFail})` : ''}: o mapa usa SVG e pode ficar lento com {total} workloads.</span>
            </div>
          )}
          {traffic && <TrafficNote q={trafficQ} dropped={placed.dropped} shown={placed.edges.length} />}
        </div>
        {!layout.islands.length && <div className="map-empty">{onlyProblems ? 'Nenhum workload com problema.' : 'Nada para mostrar neste escopo.'}</div>}
        <div className="map-corner">
          {legend && <Legend onClose={() => setLegend(false)} />}
          {minimap && layout.islands.length > 0 && (
            <MiniMap layout={layout} rectRef={miniRef} onJump={(x, y) => { cancelAnimationFrame(anim.current); viewRef.current = centerOn(x, y, viewRef.current.k); apply() }} />
          )}
        </div>
        <div ref={tipRef} className={`map-tip${hoverNode ? ' show' : ''}`} data-testid="map-tip">{hoverNode}</div>
        <div className="map-ctl">
          <div className="group" data-testid="map-levels">
            {LEVELS.map(([l, label]) => <button key={l} data-testid={`map-level-${l}`} className={level === l ? 'active' : ''} onClick={() => setLevel(l)}>{label}</button>)}
          </div>
          <div className="group">
            <div className="icons">
              <button title="Diminuir" onClick={() => zoomAt(1 / 1.4, size.w / 2, size.h / 2, 200)}><Minus size={14} /></button>
              <button title="Aumentar" onClick={() => zoomAt(1.4, size.w / 2, size.h / 2, 200)}><Plus size={14} /></button>
              <button title="Ajustar ao conteúdo" data-testid="map-fit" onClick={() => fit()}><Maximize2 size={14} /></button>
            </div>
            <div className="icons">
              <button title="Minimapa" aria-pressed={minimap} className={minimap ? 'active' : ''} data-testid="map-minimap-toggle" onClick={() => useUi.getState().update({ mapMinimap: !minimap })}><MapIcon size={14} /></button>
              <button title="Legenda" aria-pressed={legend} className={legend ? 'active' : ''} data-testid="map-legend-toggle" onClick={() => setLegend((v) => !v)}><Info size={14} /></button>
            </div>
            <div className="zoom" ref={zoomRef} data-testid="map-zoom">{Math.round(view.k * 100)}%</div>
          </div>
        </div>
      </div>
      <div className="map-foot" data-testid="map-summary">
        <span>{scope} · <b>{layout.totals.namespaces}</b> namespaces / <b>{layout.totals.workloads}</b> workloads / <b>{layout.totals.pods}</b> pods / <b>{layout.totals.apps}</b> apps / <b>{layout.totals.services}</b> services</span>
        <span className="muted"><Filter size={11} style={{ verticalAlign: -1 }} /> {hideSystem && hasUserNs && !selected.size ? 'sistema oculto' : 'todos os namespaces'}</span>
        <span className="muted" data-testid="map-renderer-label" title="Renderizador do palco (limiar nas configurações do mapa)">{renderer === 'webgl' ? 'WebGL' : 'SVG'}</span>
        <span className="legend">
          <span><i style={{ background: 'var(--ok)' }} />{podTotals.ok} saudáveis</span>
          <span><i style={{ background: 'var(--warn)' }} />{podTotals.warn} atenção</span>
          <span><i style={{ background: 'var(--error)' }} />{podTotals.error} com erro</span>
        </span>
      </div>
    </div>
  )
}

function Row({ k, v, warn }: { k: string; v: string; warn?: boolean }) {
  return <div className="row"><span>{k}</span><b className={warn ? 'warn' : undefined}>{v}</b></div>
}

const HEALTH_LABEL: Record<string, string> = { ok: 'Saudável', warn: 'Atenção', error: 'Erro', neutral: 'Neutro' }

/** Stacked pod bar with the counts, for the tooltip. */
function PodStrip({ c }: { c: { total: number; ok: number; warn: number; error: number } }) {
  if (!c.total) return <div className="tip-strip-empty">Sem pods</div>
  const other = Math.max(0, c.total - c.ok - c.warn - c.error)
  const pct = (n: number) => `${(n / c.total) * 100}%`
  return (
    <div className="tip-strip">
      <div className="bar">
        {!!c.ok && <i className="ok" style={{ width: pct(c.ok) }} />}
        {!!c.warn && <i className="warn" style={{ width: pct(c.warn) }} />}
        {!!c.error && <i className="error" style={{ width: pct(c.error) }} />}
        {!!other && <i className="other" style={{ width: pct(other) }} />}
      </div>
      <div className="nums">
        <span><b>{c.total}</b> pod{c.total === 1 ? '' : 's'}</span>
        {!!c.ok && <span className="ok">{c.ok} ok</span>}
        {!!c.warn && <span className="warn">{c.warn} atenção</span>}
        {!!c.error && <span className="error">{c.error} erro</span>}
      </div>
    </div>
  )
}

function Legend({ onClose }: { onClose: () => void }) {
  const kinds = ['Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job', 'Pod']
  const hex = (cls: string) => <svg width="16" height="18" viewBox="-16 -18 32 36"><path className={`lg-hex ${cls}`} d={hexPath(14)} /></svg>
  return (
    <div className="map-legend" data-testid="map-legend" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
      <div className="lg-head"><b>Legenda</b><button onClick={onClose} aria-label="Fechar legenda">×</button></div>
      <div className="lg-sec">
        <div className="lg-row"><span className="lg-isl" />Namespace (ilha)</div>
        <div className="lg-row"><span className="lg-app" />App (região tracejada)</div>
        <div className="lg-row">{hex('ok')}Workload saudável</div>
        <div className="lg-row">{hex('warn')}Workload com atenção</div>
        <div className="lg-row">{hex('error')}Workload com erro</div>
        <div className="lg-row"><span className="lg-pods"><i className="ok" /><i className="warn" /><i className="error" /></span>Pods (favos dentro do workload)</div>
      </div>
      <div className="lg-sec lg-kinds">
        {kinds.map((k) => { const I = kindIcon(k); return <span key={k}><I size={12} /><b>{kindTag(k)}</b>{k}</span> })}
      </div>
      <div className="lg-sec lg-kinds">
        <span><Network size={12} />Services</span><span><Globe size={12} />Hosts de Ingress</span>
        <span><HardDrive size={12} />PVCs</span><span><RotateCcw size={12} />Reinícios</span>
      </div>
    </div>
  )
}

function TrafficNote({ q, dropped, shown }: { q: ReturnType<typeof useTrafficEdges>; dropped: number; shown: number }) {
  const d = q.data
  let body: React.ReactNode
  if (q.error) body = <span>Tráfego: {q.error}</span>
  else if (q.loading && !d.available && !d.hint) body = <span>Lendo tráfego observado…</span>
  else if (!d.available) {
    body = (
      <span>
        Tráfego observado indisponível. <span className="mgl-sub">{d.hint || 'Precisa do Hubble (Cilium) ou do Caretta com o Prometheus do cluster configurado. Nada é instalado pelo Leme.'}</span>
      </span>
    )
  } else {
    const src = d.source === 'hubble' ? 'Hubble' : d.source === 'caretta' ? 'Caretta' : d.source
    body = (
      <span>
        {shown ? `${shown} conexão(ões)` : 'Nenhuma conexão observada'}{src ? ` · ${src}` : ''}{d.window ? ` · janela ${d.window}` : ''}
        {!!dropped && <span className="mgl-sub"> · {dropped} fora do mapa (nós, externos ou namespaces filtrados)</span>}
        <span className="mgl-sub"> · espessura = volume, seta = direção</span>
      </span>
    )
  }
  return <div className="mgl-note" data-testid="map-traffic-note"><Activity size={14} />{body}</div>
}

function MapSettings({ threshold, renderer, total, onClose }: { threshold: number; renderer: 'svg' | 'webgl'; total: number; onClose: () => void }) {
  const [draft, setDraft] = useState(String(threshold))
  useEffect(() => setDraft(String(threshold)), [threshold])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    // Capture: other Escape handlers of the shell must not swallow it first.
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])
  const commit = () => {
    const n = Math.round(Number(draft))
    if (Number.isFinite(n) && n >= 1 && n <= 1_000_000) useUi.getState().update({ mapWebglThreshold: n })
    else setDraft(String(threshold))
  }
  return (
    <div className="mgl-settings" data-testid="map-settings-pop">
      <b>Mapa do cluster</b>
      <label>
        <span>Usar WebGL a partir de</span>
        <input type="number" min={1} step={100} data-testid="map-webgl-threshold" value={draft} onChange={(e) => setDraft(e.target.value)} onBlur={commit}
          onKeyDown={(e) => { if (e.key === 'Enter') commit() }} />
      </label>
      <label className="mgl-check">
        <input type="checkbox" data-testid="map-hide-empty" checked={useUi.getState().mapHideEmpty} onChange={(e) => useUi.getState().update({ mapHideEmpty: e.target.checked })} />
        <span>Ocultar namespaces sem workloads</span>
      </label>
      <div className="mgl-sub">
        <Info size={12} style={{ verticalAlign: -2 }} /> Agora: <span className="mgl-kind">{renderer === 'webgl' ? 'WebGL' : 'SVG'}</span> com {total} workloads. Abaixo do limiar o mapa usa SVG; a partir dele, WebGL (sem WebGL, continua em SVG).
      </div>
    </div>
  )
}
