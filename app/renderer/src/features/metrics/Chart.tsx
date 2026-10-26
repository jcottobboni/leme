import { useEffect, useMemo, useRef, useState } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { Maximize2, Minimize2, RefreshCw, SearchX, ZoomOut } from 'lucide-react'
import type { PromChart } from '../../api'
import { bytes, cpu } from '../../format'
import { useTheme, type Theme } from '../shell/theme'
import './charts.css'

export function formatValue(unit: PromChart['unit'], v: number | null | undefined) {
  if (v === null || v === undefined || Number.isNaN(v)) return '—'
  if (unit === 'cores') {
    const m = v * 1000
    if (m > 0 && m < 10) return `${m.toFixed(m < 1 ? 2 : 1)}m`
    return cpu(Math.round(m))
  }
  if (unit === 'bytes') return bytes(v)
  return `${bytes(v)}/s`
}

// Categorical palettes (colorblind-safe order: blue, orange, teal, violet, pink, lime, cyan, gold), one per theme.
const SERIES_COLORS: Record<Theme, string[]> = {
  dark: ['#4aa3ff', '#ff9f43', '#2dd4bf', '#b79cff', '#ff7eb6', '#a3e635', '#38bdf8', '#facc15'],
  light: ['#1f6feb', '#d9730d', '#0f9d8a', '#7c4dff', '#d6336c', '#5c940d', '#0c8599', '#b8860b'],
}
const REF_COLORS: Record<Theme, Record<string, string>> = {
  dark: { capacity: '#8b95a1', allocatable: '#2fbf71', requests: '#9a8cff', limits: '#f0524f' },
  light: { capacity: '#687280', allocatable: '#1a9d55', requests: '#5b5bd6', limits: '#d1302d' },
}
const REF_LABELS: Record<string, string> = { capacity: 'Capacidade', allocatable: 'Alocável', requests: 'Requests', limits: 'Limits' }
const NAME_LABELS: Record<string, string> = { rx: 'Recebido (RX)', tx: 'Enviado (TX)', read: 'Leitura', write: 'Escrita' }
const TITLES: Record<string, string> = { cpu: 'CPU', memory: 'Memória', network: 'Rede', fs: 'Disco (I/O)' }
const TOTAL_LABELS: Record<string, string> = { cpu: 'Uso de CPU', memory: 'Uso de memória' }

type RawSeries = PromChart['series'][number] & { ref?: string }

export interface SeriesMeta {
  key: string
  label: string
  ref?: string
  color: string
  mirrored: boolean
}

/** Orders usage series before reference lines and merges them into uPlot's column format (tx negated on network). */
export function prepare(chart: PromChart, theme: Theme): { data: uPlot.AlignedData; meta: SeriesMeta[] } {
  const raw = chart.series as RawSeries[]
  const ordered = [...raw.filter((s) => !s.ref), ...raw.filter((s) => s.ref)]
  const ts = new Set<number>()
  for (const s of ordered) for (const [t] of s.points) ts.add(t)
  const xs = [...ts].sort((a, b) => a - b)
  const idx = new Map(xs.map((t, i) => [t, i]))
  const colors = SERIES_COLORS[theme]
  let ui = 0
  const meta: SeriesMeta[] = ordered.map((s) => {
    if (s.ref) return { key: s.ref, label: REF_LABELS[s.ref] ?? s.ref, ref: s.ref, color: REF_COLORS[theme][s.ref] ?? colors[0], mirrored: false }
    const color = colors[ui++ % colors.length]
    const label = s.name === 'total' ? (TOTAL_LABELS[chart.metric] ?? 'Total') : (NAME_LABELS[s.name] ?? s.name)
    return { key: s.name, label, color, mirrored: chart.metric === 'network' && s.name === 'tx' }
  })
  if (chart.metric === 'network') {
    // RX above the axis, TX mirrored below it: fixed colors so the pair reads the same everywhere.
    meta.forEach((m) => {
      if (m.key === 'rx') m.color = colors[0]
      if (m.key === 'tx') m.color = colors[1]
    })
  }
  const cols: (number | null)[][] = ordered.map(() => new Array(xs.length).fill(null))
  ordered.forEach((s, si) => {
    const sign = meta[si].mirrored ? -1 : 1
    for (const [t, v] of s.points) cols[si][idx.get(t)!] = Number.isFinite(v) ? v * sign : null
  })
  return { data: [xs, ...cols] as uPlot.AlignedData, meta }
}

function gradient(u: uPlot, color: string, mirrored: boolean): CanvasGradient {
  const { top, height } = u.bbox
  const zero = Math.min(top + height, Math.max(top, u.valToPos(0, 'y', true)))
  const g = u.ctx.createLinearGradient(0, mirrored ? top + height : top, 0, zero)
  g.addColorStop(0, color + '66')
  g.addColorStop(1, color + '05')
  return g
}

const pad2 = (n: number) => String(n).padStart(2, '0')
/** pt-BR axis labels: HH:mm, with the day when the window spans more than ~a day. */
function timeTicks(u: uPlot, splits: number[]) {
  const sc = u.scales.x
  const span = (sc.max ?? 0) - (sc.min ?? 0)
  return splits.map((t) => {
    const d = new Date(t * 1000)
    const hm = `${pad2(d.getHours())}:${pad2(d.getMinutes())}`
    if (span > 2 * 86400) return d.getHours() === 0 && d.getMinutes() === 0 ? `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)}` : hm
    return span > 86400 ? `${pad2(d.getDate())}/${pad2(d.getMonth() + 1)} ${hm}` : hm
  })
}

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)

const lastOf = (col: (number | null)[]) => {
  for (let i = col.length - 1; i >= 0; i--) if (col[i] !== null && col[i] !== undefined) return col[i]
  return null
}

/** Reference lines far above usage would flatten it: start them hidden (the chip stays to toggle them). */
function defaultHidden(data: uPlot.AlignedData, meta: SeriesMeta[]): Set<string> {
  let usageMax = 0
  meta.forEach((m, i) => {
    if (!m.ref) for (const v of data[i + 1]) if (v !== null && v !== undefined) usageMax = Math.max(usageMax, Math.abs(v))
  })
  const hidden = new Set<string>()
  meta.forEach((m, i) => {
    if (!m.ref) return
    const mx = Math.max(0, ...(data[i + 1] as (number | null)[]).map((v) => v ?? 0))
    if (usageMax > 0 && mx > usageMax * 6) hidden.add(m.key)
  })
  return hidden
}

interface Props {
  chart: PromChart
  height?: number
  onRefresh?: () => void
  refreshing?: boolean
  expanded?: boolean
  onToggleExpand?: () => void
}

/** Metric card: header actions, legend chips with live values, area chart with crosshair tooltip, zoom and range brush. */
export function MetricChart({ chart, height = 190, onRefresh, refreshing, expanded, onToggleExpand }: Props) {
  const theme = useTheme((s) => s.theme)
  const [localExpanded, setLocalExpanded] = useState(false)
  const isExpanded = expanded ?? localExpanded
  const toggleExpand = onToggleExpand ?? (() => setLocalExpanded((v) => !v))
  const h = isExpanded ? Math.max(height, 320) : height
  const { data, meta } = useMemo(() => prepare(chart, theme), [chart, theme])
  const mainEl = useRef<HTMLDivElement>(null)
  const brushEl = useRef<HTMLDivElement>(null)
  const plots = useRef<{ main?: uPlot; brush?: uPlot }>({})
  const resetRef = useRef<() => void>(() => undefined)
  const hiddenRef = useRef<Set<string>>(undefined as unknown as Set<string>)
  const zoomRef = useRef<{ min: number; max: number } | null>(null)
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  const [cursor, setCursor] = useState<number | null>(null)
  const [zoomed, setZoomed] = useState(false)
  const empty = chart.series.length === 0 || data[0].length === 0
  const mirrored = meta.some((m) => m.mirrored)

  useEffect(() => {
    // The first data decides which reference lines start hidden; later refreshes keep the user's choice.
    if (!hiddenRef.current) hiddenRef.current = defaultHidden(data, meta)
    setHidden(new Set(hiddenRef.current))
  }, [data, meta])

  useEffect(() => {
    if (empty || !mainEl.current || !brushEl.current) return
    const host = mainEl.current
    const brushHost = brushEl.current
    const css = getComputedStyle(document.documentElement)
    const grid = css.getPropertyValue('--line-soft').trim() || '#1e2228'
    const text = css.getPropertyValue('--muted').trim() || '#7d8a99'
    const xs = data[0] as number[]
    const full = { min: xs[0], max: xs[xs.length - 1] }
    const hide = hiddenRef.current ?? new Set<string>()
    const range = (_u: uPlot, min: number, max: number): [number, number] => {
      if (mirrored) {
        const m = Math.max(Math.abs(min), max) * 1.15 // symmetric around the axis so RX and TX read alike
        return m > 0 ? [-m, m] : [-1, 1]
      }
      return [0, max > 0 ? max * 1.15 : 1]
    }
    const absFmt = (v: number | null) => formatValue(chart.unit, v === null ? v : Math.abs(v))
    let syncing = false

    const tip = document.createElement('div')
    tip.className = 'mc-tip'
    tip.style.display = 'none'

    const main = new uPlot(
      {
        width: host.clientWidth || 400,
        height: h,
        legend: { show: false },
        padding: [8, 8, 0, 0],
        cursor: { drag: { x: true, y: false, setScale: true }, points: { size: 7 }, y: false },
        scales: { x: { time: true }, y: { range } },
        axes: [
          { stroke: text, grid: { stroke: grid, width: 1 }, ticks: { show: false }, font: '11px Inter, sans-serif', gap: 6, size: 28, values: timeTicks },
          { stroke: text, grid: { stroke: grid, width: 1 }, ticks: { show: false }, size: 62, font: '11px Inter, sans-serif', values: (_u, vals) => vals.map((v) => (v === 0 && mirrored ? '0' : absFmt(v))) },
        ],
        series: [
          {},
          ...meta.map((m): uPlot.Series => ({
            label: m.label,
            stroke: m.color,
            width: m.ref ? 1.25 : 1.6,
            dash: m.ref === 'limits' ? [6, 4] : m.ref ? [2, 3] : undefined,
            fill: m.ref ? undefined : (u) => gradient(u, m.color, m.mirrored),
            show: !hide.has(m.key),
            points: { show: false },
            spanGaps: false,
          })),
        ],
        hooks: {
          init: [(u) => u.over.appendChild(tip)],
          setCursor: [
            (u) => {
              const i = u.cursor.idx
              setCursor((prev) => (prev === (i ?? null) ? prev : (i ?? null)))
              if (i === null || i === undefined || u.cursor.left === undefined || u.cursor.left < 0) {
                tip.style.display = 'none'
                return
              }
              const rows = meta
                .map((m, k) => ({ m, v: u.data[k + 1][i], on: u.series[k + 1].show }))
                .filter((r) => r.on && r.v !== null && r.v !== undefined)
                .map((r) => `<div><i style="background:${r.m.color}"></i><span>${esc(r.m.label)}</span><b>${absFmt(r.v as number)}</b></div>`)
              const when = new Date((u.data[0][i] as number) * 1000).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
              tip.innerHTML = `<div class="mc-tip-time">${when}</div>${rows.join('')}`
              tip.style.display = 'block'
              const left = u.cursor.left + 14
              tip.style.left = `${left + tip.offsetWidth > u.over.clientWidth ? Math.max(0, u.cursor.left - 14 - tip.offsetWidth) : left}px`
              tip.style.top = `${Math.max(0, Math.min(u.over.clientHeight - tip.offsetHeight, (u.cursor.top ?? 0) - 10))}px`
            },
          ],
          setScale: [
            (u, key) => {
              if (key !== 'x' || syncing) return
              const { min, max } = u.scales.x as { min: number; max: number }
              const isFull = min <= full.min + 1 && max >= full.max - 1
              zoomRef.current = isFull ? null : { min, max }
              setZoomed(!isFull)
              const b = plots.current.brush
              if (!b) return
              if (isFull) b.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false)
              else {
                const l = b.valToPos(min, 'x')
                b.setSelect({ left: l, top: 0, width: b.valToPos(max, 'x') - l, height: b.over.clientHeight }, false)
              }
            },
          ],
        },
      },
      data,
      host,
    )

    // Range brush: a small overview of the usage series; the selection drives the main x scale.
    const usage = meta.map((m, i) => ({ m, i })).filter((x) => !x.m.ref)
    const brush = new uPlot(
      {
        width: brushHost.clientWidth || 400,
        height: 44,
        legend: { show: false },
        padding: [2, 8, 2, 62],
        cursor: { drag: { x: true, y: false, setScale: false }, points: { show: false }, x: false, y: false },
        select: { show: true, left: 0, top: 0, width: 0, height: 0 },
        scales: { x: { time: true }, y: { range } },
        axes: [{ show: false }, { show: false }],
        series: [{}, ...usage.map(({ m }): uPlot.Series => ({ stroke: m.color, width: 1, fill: (u) => gradient(u, m.color, m.mirrored), points: { show: false } }))],
        hooks: {
          setSelect: [
            (u) => {
              if (u.select.width < 3) return
              syncing = true
              main.setScale('x', { min: u.posToVal(u.select.left, 'x'), max: u.posToVal(u.select.left + u.select.width, 'x') })
              syncing = false
              const { min, max } = main.scales.x as { min: number; max: number }
              zoomRef.current = { min, max }
              setZoomed(true)
            },
          ],
        },
      },
      [data[0], ...usage.map(({ i }) => data[i + 1])] as uPlot.AlignedData,
      brushHost,
    )
    plots.current = { main, brush }
    const reset = () => {
      main.setScale('x', full)
      brush.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false)
    }
    main.over.addEventListener('dblclick', reset)
    brush.over.addEventListener('dblclick', reset)
    resetRef.current = reset
    if (zoomRef.current && zoomRef.current.max > full.min && zoomRef.current.min < full.max) main.setScale('x', zoomRef.current)
    else zoomRef.current = null
    const ro = new ResizeObserver(() => {
      main.setSize({ width: host.clientWidth, height: h })
      brush.setSize({ width: brushHost.clientWidth, height: 44 })
    })
    ro.observe(host)
    return () => {
      ro.disconnect()
      plots.current = {}
      main.destroy()
      brush.destroy()
    }
  }, [data, meta, h, empty, mirrored, chart.unit, theme])

  const toggle = (m: SeriesMeta, i: number) => {
    const next = new Set(hiddenRef.current)
    const show = next.has(m.key)
    if (show) next.delete(m.key)
    else next.add(m.key)
    hiddenRef.current = next
    setHidden(next)
    plots.current.main?.setSeries(i + 1, { show })
    const usageIdx = meta.filter((x) => !x.ref).indexOf(m)
    if (usageIdx >= 0) plots.current.brush?.setSeries(usageIdx + 1, { show })
  }

  return (
    <div className={`metric-chart mc-card${isExpanded ? ' expanded' : ''}`} data-testid={`chart-${chart.metric}`}>
      <div className="mc-head">
        <span className="mc-title">{TITLES[chart.metric] ?? chart.metric}</span>
        <span className="mc-sub muted small" title={chart.queries.join('\n')}>{chart.metric === 'network' || chart.metric === 'fs' ? 'bytes/s' : chart.unit === 'cores' ? 'cores' : 'bytes'} · PromQL</span>
        <span className="spacer" />
        {zoomed && <button className="icon-btn" title="Redefinir zoom" aria-label="Redefinir zoom" onClick={() => resetRef.current()}><ZoomOut size={14} /></button>}
        <button className="icon-btn" title="Atualizar" aria-label="Atualizar" onClick={onRefresh} disabled={!onRefresh}><RefreshCw size={14} className={refreshing ? 'mc-spin' : ''} /></button>
        <button className="icon-btn" title={isExpanded ? 'Recolher' : 'Expandir'} aria-label={isExpanded ? 'Recolher' : 'Expandir'} data-testid={`chart-expand-${chart.metric}`} onClick={toggleExpand}>
          {isExpanded ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
        </button>
      </div>
      {empty ? (
        <div className="mc-empty"><SearchX size={22} /><span>Sem dados para esta janela.</span><span className="muted small">O Prometheus não retornou séries para este escopo.</span></div>
      ) : (
        <>
          <div className="mc-chips">
            {meta.map((m, i) => {
              const col = data[i + 1] as (number | null)[]
              const v = cursor !== null ? col[cursor] : lastOf(col)
              const off = hidden.has(m.key)
              return (
                <button key={m.ref ?? m.key} className={`mc-chip${off ? ' off' : ''}${m.ref ? ' ref' : ''}`} onClick={() => toggle(m, i)} title={off ? 'Mostrar série' : 'Ocultar série'} data-testid={`chip-${chart.metric}-${m.ref ?? m.key}`}>
                  <i className={m.ref === 'limits' ? 'dash' : ''} style={{ background: m.color, color: m.color }} />
                  <span>{m.label}</span>
                  <b>{formatValue(chart.unit, v === null || v === undefined ? null : Math.abs(v))}</b>
                </button>
              )
            })}
          </div>
          <div ref={mainEl} className="mc-plot" />
          <div ref={brushEl} className="mc-brush" title="Arraste para selecionar o período; duplo clique redefine" />
        </>
      )}
    </div>
  )
}
