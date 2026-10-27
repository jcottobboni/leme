// SVG stage of the cluster map: islands (namespaces), app regions, workload tiles, minimap and traffic edges.
import { memo, useRef, useState } from 'react'
import type { MapWorkload } from '../../api'
import { kindIcon } from '../shell/nav'
import { Globe, HardDrive, Network, RotateCcw } from 'lucide-react'
import { DX, DY, TILE_R, type AppL, type IslandL, type Layout, type Match, type TileL } from './layout'
import { hexPath, seen, trunc, type VP } from './geometry'
import { arrowOf, type EdgeL } from '../map-webgl/edges'

const HEX = hexPath(TILE_R)
const HEX_INNER = hexPath(TILE_R * 0.84)
const HEX_RING = hexPath(TILE_R * 1.2)

/** Short kind tag shown on the tile (the full kind is in the tooltip and the legend). */
export const KIND_TAG: Record<string, string> = {
  Deployment: 'DEPLOY', StatefulSet: 'STS', DaemonSet: 'DS', CronJob: 'CRON', Job: 'JOB', Pod: 'POD',
  ReplicaSet: 'RS', ReplicationController: 'RC', DeploymentConfig: 'DC',
}
export const kindTag = (k: string) => KIND_TAG[k] ?? k.slice(0, 4).toUpperCase()

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

export const Island = memo(function Island({ isl, k, vp, lod, dr, match, sel }: { isl: IslandL; k: number; vp: VP; lod: number; dr: number; match: Match | null; sel: string }) {
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
export const MiniMap = memo(function MiniMap({ layout, rectRef, onJump }: { layout: Layout; rectRef: React.RefObject<SVGRectElement | null>; onJump: (x: number, y: number) => void }) {
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
export const SvgEdges = memo(function SvgEdges({ edges, k }: { edges: EdgeL[]; k: number }) {
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
