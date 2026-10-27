import { useEffect, useMemo, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api } from '../../api'
import { clusterName, openPanel, useApp, useUi, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { openTopology } from '../topology/Topology'
import { ClusterGate, ErrorBanner, Partial, useAsync } from '../common'
import { usePanelVisible } from '../../hooks/visibility'
import { useTheme } from '../shell/theme'
import { Activity, Filter, Info, Layers, Map as MapIcon, Maximize2, Minus, Plus, RefreshCw, Search, Server, Settings2, TriangleAlert } from 'lucide-react'
import { computeMatch, countWorkloads, dotRadius, hasProblem, isSystemNs, lodOf, TILE_R } from './layout'
import { seen } from './geometry'
import { useMapViewport } from './useMapViewport'
import { Island, MiniMap, SvgEdges } from './SvgStage'
import { tooltipContent, type HoverTarget } from './MapTooltip'
import { Legend, TrafficNote } from './MapOverlays'
import { MapSettings } from './MapSettings'
import { useMapLayout } from '../map-webgl/useMapLayout'
import { GlStage, type GlStageHandle } from '../map-webgl/GlStage'
import { placeEdges } from '../map-webgl/edges'
import { useFpsProbe, webglAvailable } from '../map-webgl/probe'
import { useTrafficEdges } from '../map-webgl/trafficEdges'
import type { Hit } from '../map-webgl/scene'
import './map.css'
import '../map-webgl/mgl.css'

export { KIND_TAG } from './SvgStage'

export function openMap(clusterId: string, opts?: OpenOptions) {
  openPanel(`map:${clusterId}`, 'map', 'Mapa do cluster', { clusterId }, opts)
}

type Level = 'namespaces' | 'workloads' | 'pods'
const LEVELS: [Level, string][] = [['namespaces', 'Namespaces'], ['workloads', 'Workloads'], ['pods', 'Pods']]
const levelOf = (k: number): Level => (TILE_R * k < 11 ? 'namespaces' : TILE_R * k < 48 ? 'workloads' : 'pods')

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

  const visible = usePanelVisible()
  const { stageRef, gRef, zoomRef, miniRef, viewRef, view, size, vp, apply, stop, animateTo, centerOn, fit, zoomAt, zoomToBox, jumpTo } =
    useMapViewport({ layout, ready, minimap, glRef })
  const tipRef = useRef<HTMLDivElement>(null)
  const dragged = useRef(0)
  const [hover, setHover] = useState<HoverTarget | null>(null)
  const [panning, setPanning] = useState(false)

  // Escape clears the selection ring, only in the map tab that is on screen.
  useEffect(() => {
    if (!visible) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setSel('') }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible])

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


  const drag = useRef<{ x: number; y: number; vx: number; vy: number } | null>(null)
  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return
    stop()
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

  const lod = lodOf(view.k)
  const dr = dotRadius(view.k)
  const level = levelOf(view.k)

  const hoverNode = useMemo(() => tooltipContent(hover, layout, map?.compact), [hover, layout, map])

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
            <MiniMap layout={layout} rectRef={miniRef} onJump={(x, y) => jumpTo(centerOn(x, y, viewRef.current.k))} />
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
