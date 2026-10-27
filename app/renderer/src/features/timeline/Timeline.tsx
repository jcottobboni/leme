import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { openSocket, type TimelineEvent } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { ClusterGate } from '../common'
import { PanelHeader } from '../../components/PanelHeader'
import { CircleHelp, Play, Timer } from 'lucide-react'
import { buildRows, density, matches, type Filter, type Row } from './model'

const LABEL_W = 300
const ROW_H = 22
const AXIS_H = 22
const HIST_H = 46
const WINDOWS: [string, number][] = [['15 min', 15 * 60e3], ['1 h', 3600e3], ['6 h', 6 * 3600e3], ['24 h', 24 * 3600e3]]

export function openTimeline(clusterId: string, opts?: OpenOptions) {
  openPanel(`timeline:${clusterId}`, 'timeline', 'Timeline', { clusterId }, opts)
}

function useTimeline(clusterId: string, namespace: string) {
  const [events, setEvents] = useState<TimelineEvent[]>([])
  const [error, setError] = useState<string>()
  const map = useRef(new Map<string, TimelineEvent>())
  useEffect(() => {
    map.current = new Map()
    setEvents([])
    let closed = false
    let ws: WebSocket
    let retry = 0
    const open = () => {
      ws = openSocket(`/api/clusters/${clusterId}/timeline`, { namespace })
      ws.onmessage = (m) => {
        const msg = JSON.parse(m.data)
        if (msg.type === 'error') return setError(msg.error)
        if (msg.type === 'snapshot') map.current = new Map()
        for (const e of (msg.events ?? []) as TimelineEvent[]) map.current.set(e.id, e)
        retry = 0
        setError(undefined)
        setEvents(Array.from(map.current.values()))
      }
      ws.onclose = () => {
        if (closed) return
        retry = Math.min(retry + 1, 6)
        setTimeout(() => !closed && open(), 500 * 2 ** retry)
      }
    }
    open()
    return () => {
      closed = true
      ws?.close()
    }
  }, [clusterId, namespace])
  return { events, error }
}

function cssVar(name: string, fallback: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

function fmtTime(t: number, span: number) {
  const d = new Date(t)
  return span > 2 * 86400e3
    ? d.toLocaleDateString(undefined, { day: '2-digit', month: '2-digit' }) + ' ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: span < 30 * 60e3 ? '2-digit' : undefined })
}

function Timeline({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const { events, error } = useTimeline(clusterId, ns)
  const [win, setWin] = useState(3600e3)
  const [live, setLive] = useState(true)
  const [view, setView] = useState(() => ({ from: Date.now() - 3600e3, to: Date.now() }))
  const [type, setType] = useState<Filter['type']>('')
  const [text, setText] = useState('')
  const [expanded, setExpanded] = useState(new Set<string>())
  const [scroll, setScroll] = useState(0)
  const [hover, setHover] = useState<{ e: TimelineEvent; x: number; y: number }>()
  const [selected, setSelected] = useState<TimelineEvent>()
  const host = useRef<HTMLDivElement>(null)
  const canvas = useRef<HTMLCanvasElement>(null)
  const [size, setSize] = useState({ w: 800, h: 400 })
  const drag = useRef<{ x: number; from: number; to: number; moved: boolean }>(undefined)

  // Follow "now" while live.
  useEffect(() => {
    if (!live) return
    const tick = () => setView({ from: Date.now() - win, to: Date.now() + win * 0.02 })
    tick()
    const t = setInterval(tick, 5000)
    return () => clearInterval(t)
  }, [live, win])

  useEffect(() => {
    const el = host.current!
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const filter: Filter = useMemo(() => ({ type, text, from: view.from, to: view.to }), [type, text, view])
  // Namespace rows open by default; deeper levels start collapsed (aggregated).
  const rows = useMemo(() => buildRows(events, filter, expanded, (depth) => depth > 0), [events, filter, expanded])
  const hist = useMemo(() => density(events, filter, Math.max(10, Math.floor((size.w - LABEL_W) / 4))), [events, filter, size.w])
  const total = useMemo(() => events.filter((e) => matches(e, filter)).length, [events, filter])

  const plotW = Math.max(50, size.w - LABEL_W)
  const xOf = useCallback((t: number) => LABEL_W + ((t - view.from) / (view.to - view.from)) * plotW, [view, plotW])
  const top = HIST_H + AXIS_H
  const visibleRows = Math.ceil((size.h - top) / ROW_H) + 1
  const maxScroll = Math.max(0, rows.length - visibleRows + 2)
  const first = Math.min(scroll, maxScroll)

  useEffect(() => {
    const c = canvas.current
    if (!c) return
    const dpr = window.devicePixelRatio || 1
    c.width = size.w * dpr
    c.height = size.h * dpr
    const g = c.getContext('2d')!
    g.setTransform(dpr, 0, 0, dpr, 0, 0)
    const bg = cssVar('--panel', '#11161d')
    const line = cssVar('--line', '#222b36')
    const muted = cssVar('--muted', '#7d8a99')
    const textC = cssVar('--text', '#d6dde6')
    const warn = cssVar('--error', '#ef4444')
    const normal = cssVar('--accent', '#38bdf8')
    g.fillStyle = bg
    g.fillRect(0, 0, size.w, size.h)
    g.font = '12px Inter, system-ui, sans-serif'
    g.textBaseline = 'middle'

    // Density histogram.
    const n = hist.warn.length
    const bw = plotW / n
    const peak = Math.max(1, ...hist.warn.map((w, i) => w + hist.normal[i]))
    for (let i = 0; i < n; i++) {
      const hn = (hist.normal[i] / peak) * (HIST_H - 8)
      const hw = (hist.warn[i] / peak) * (HIST_H - 8)
      g.fillStyle = normal + '88'
      g.fillRect(LABEL_W + i * bw, HIST_H - hn - hw, Math.max(1, bw - 1), hn)
      g.fillStyle = warn + 'cc'
      g.fillRect(LABEL_W + i * bw, HIST_H - hw, Math.max(1, bw - 1), hw)
    }
    g.fillStyle = muted
    g.fillText('densidade', 10, HIST_H / 2)

    // Time axis.
    const span = view.to - view.from
    const steps = [10e3, 30e3, 60e3, 5 * 60e3, 10 * 60e3, 30 * 60e3, 3600e3, 3 * 3600e3, 6 * 3600e3, 12 * 3600e3, 86400e3]
    const step = steps.find((s) => (s / span) * plotW > 90) ?? 86400e3
    g.strokeStyle = line
    g.beginPath()
    g.moveTo(0, top - 0.5)
    g.lineTo(size.w, top - 0.5)
    g.stroke()
    for (let t = Math.ceil(view.from / step) * step; t < view.to; t += step) {
      const x = Math.round(xOf(t)) + 0.5
      g.strokeStyle = line
      g.beginPath()
      g.moveTo(x, HIST_H)
      g.lineTo(x, size.h)
      g.stroke()
      // Labels that would be cut at the right edge are skipped (the grid line stays).
      const label = fmtTime(t, span)
      if (x + 4 + g.measureText(label).width > size.w - 4) continue
      g.fillStyle = muted
      g.fillText(label, x + 4, HIST_H + AXIS_H / 2)
    }
    const now = xOf(Date.now())
    if (now > LABEL_W && now < size.w) {
      g.strokeStyle = normal
      g.setLineDash([4, 4])
      g.beginPath()
      g.moveTo(now, HIST_H)
      g.lineTo(now, size.h)
      g.stroke()
      g.setLineDash([])
    }

    // Rows.
    g.save()
    g.beginPath()
    g.rect(0, top, size.w, size.h - top)
    g.clip()
    for (let i = first; i < Math.min(rows.length, first + visibleRows); i++) {
      const r = rows[i]
      const y = top + (i - first) * ROW_H
      if (i % 2) {
        g.fillStyle = '#ffffff06'
        g.fillRect(0, y, size.w, ROW_H)
      }
      if (selected && r.events.some((e) => e.id === selected.id)) {
        g.fillStyle = '#38bdf81a'
        g.fillRect(0, y, size.w, ROW_H)
      }
      g.fillStyle = r.depth === 0 ? textC : muted
      const indent = 8 + r.depth * 14
      const chev = r.hasChildren ? (r.expanded ? '▾ ' : '▸ ') : '  '
      let label = chev + r.label + (r.aggregated ? `  (${r.events.length})` : '')
      while (g.measureText(label).width > LABEL_W - indent - 26 && label.length > 4) label = label.slice(0, -2)
      g.fillText(label, indent, y + ROW_H / 2)
      if (r.warnings) {
        g.fillStyle = warn
        g.fillText('●', LABEL_W - 18, y + ROW_H / 2)
      }
      for (const e of r.events) {
        const x1 = xOf(e.first)
        const x2 = xOf(e.last)
        const color = e.type === 'Warning' ? warn : normal
        if (x2 < LABEL_W || x1 > size.w) continue
        const cy = y + ROW_H / 2
        if (x2 - x1 >= 3) {
          g.fillStyle = color + (r.aggregated ? '55' : '88')
          g.fillRect(Math.max(LABEL_W, x1), cy - 4, Math.min(size.w, x2) - Math.max(LABEL_W, x1), 8)
        }
        g.fillStyle = color
        g.beginPath()
        g.arc(Math.max(LABEL_W, x2), cy, r.aggregated ? 2.5 : 3.5, 0, Math.PI * 2)
        g.fill()
        if (e.id === selected?.id) {
          g.strokeStyle = textC
          g.lineWidth = 2
          g.strokeRect(Math.max(LABEL_W, x1) - 3, cy - 6, Math.max(6, x2 - x1 + 6), 12)
          g.lineWidth = 1
        }
      }
    }
    g.restore()
    g.strokeStyle = line
    g.beginPath()
    g.moveTo(LABEL_W - 0.5, 0)
    g.lineTo(LABEL_W - 0.5, size.h)
    g.stroke()
  }, [rows, hist, size, view, first, visibleRows, xOf, plotW, top, selected])

  const hit = (x: number, y: number): { row?: Row; e?: TimelineEvent } => {
    if (y < top) return {}
    const row = rows[first + Math.floor((y - top) / ROW_H)]
    if (!row || x < LABEL_W) return { row }
    let best: TimelineEvent | undefined
    let bestD = 7
    for (const e of row.events) {
      const x1 = xOf(e.first)
      const x2 = xOf(e.last)
      const d = x >= x1 && x <= x2 ? 0 : Math.min(Math.abs(x - x1), Math.abs(x - x2))
      if (d < bestD) {
        bestD = d
        best = e
      }
    }
    return { row, e: best }
  }

  const pos = (ev: React.MouseEvent) => {
    const r = canvas.current!.getBoundingClientRect()
    return { x: ev.clientX - r.left, y: ev.clientY - r.top }
  }

  const onWheel = (ev: WheelEvent) => {
    // Non-passive listener: Ctrl+wheel must zoom the timeline, not the page.
    ev.preventDefault()
    const r = canvas.current!.getBoundingClientRect()
    const x = ev.clientX - r.left
    if (ev.ctrlKey || ev.metaKey || (x > LABEL_W && Math.abs(ev.deltaX) < Math.abs(ev.deltaY) && ev.altKey)) {
      // Zoom the time axis around the cursor.
      const t = view.from + ((x - LABEL_W) / plotW) * (view.to - view.from)
      const k = ev.deltaY > 0 ? 1.25 : 0.8
      const span = Math.max(10e3, Math.min(30 * 86400e3, (view.to - view.from) * k))
      const frac = (t - view.from) / (view.to - view.from)
      setLive(false)
      setView({ from: t - span * frac, to: t + span * (1 - frac) })
      return
    }
    if (ev.shiftKey || Math.abs(ev.deltaX) > Math.abs(ev.deltaY)) {
      const d = ((ev.shiftKey ? ev.deltaY : ev.deltaX) / plotW) * (view.to - view.from)
      setLive(false)
      setView({ from: view.from + d, to: view.to + d })
      return
    }
    setScroll((s) => Math.max(0, Math.min(maxScroll, s + Math.sign(ev.deltaY) * 3)))
  }

  const wheel = useRef(onWheel)
  wheel.current = onWheel
  useEffect(() => {
    const c = canvas.current!
    const h = (ev: WheelEvent) => wheel.current(ev)
    c.addEventListener('wheel', h, { passive: false })
    return () => c.removeEventListener('wheel', h)
  }, [])

  return (
    <div className="object-panel timeline-panel">
      <PanelHeader icon={Timer} title="Timeline de eventos" scope={<NamespaceSelect clusterId={clusterId} />} actions={[
        { key: 'window', label: 'Janela', value: live ? String(win) : '', onSelect: (v) => { setWin(Number(v)); setLive(true) }, options: WINDOWS.map(([l, ms]) => ({ value: String(ms), label: l, title: `Últimos ${l}, ao vivo` })) },
        ...(!live ? [{ key: 'live', label: 'Ao vivo', icon: Play, keep: true, onClick: () => setLive(true) }] : []),
        { key: 'help', label: 'Como navegar', icon: CircleHelp, iconOnly: true, keep: true, title: 'Ctrl + roda: zoom · Shift + roda ou arrastar: mover no tempo · roda: rolar linhas', onClick: () => undefined },
      ]}>
        <select value={type} onChange={(e) => setType(e.target.value as Filter['type'])} aria-label="Tipo de evento">
          <option value="">Todos os tipos</option>
          <option value="Warning">Só Warning</option>
          <option value="Normal">Só Normal</option>
        </select>
        <input className="search" placeholder="Filtrar motivo, mensagem, objeto…" aria-label="Filtrar eventos" value={text} onChange={(e) => setText(e.target.value)} />
        <span className="muted small ph-info">{events.length} eventos retidos · {total} na janela</span>
      </PanelHeader>
      {error && <div className="banner error">{error}</div>}
      <div className="timeline-host" ref={host} data-testid="timeline" data-rows={rows.slice(0, 300).map((r) => r.key).join('\n')}>
        <canvas
          ref={canvas}
          style={{ width: size.w, height: size.h }}
          onMouseDown={(ev) => {
            const { x } = pos(ev)
            if (x > LABEL_W) drag.current = { x: ev.clientX, from: view.from, to: view.to, moved: false }
          }}
          onMouseMove={(ev) => {
            const p = pos(ev)
            if (drag.current) {
              const dx = ev.clientX - drag.current.x
              if (Math.abs(dx) > 3) {
                drag.current.moved = true
                const dt = (-dx / plotW) * (drag.current.to - drag.current.from)
                setLive(false)
                setView({ from: drag.current.from + dt, to: drag.current.to + dt })
              }
              return
            }
            const h = hit(p.x, p.y)
            setHover(h.e ? { e: h.e, x: p.x, y: p.y } : undefined)
          }}
          onMouseUp={(ev) => {
            const moved = drag.current?.moved
            drag.current = undefined
            if (moved) return
            const p = pos(ev)
            const h = hit(p.x, p.y)
            if (h.e) setSelected(h.e)
            else if (h.row?.hasChildren && p.x < LABEL_W) {
              const key = h.row.key
              setExpanded((s) => {
                const n = new Set(s)
                if (n.has(key)) n.delete(key)
                else n.add(key)
                return n
              })
            }
          }}
          onMouseLeave={() => {
            drag.current = undefined
            setHover(undefined)
          }}
        />
        {hover && (
          <div className="tl-tip" style={{ left: Math.min(hover.x + 12, size.w - 320), top: hover.y + 14 }}>
            <b className={hover.e.type === 'Warning' ? 's-error' : ''}>{hover.e.reason}</b> · {hover.e.kind}/{hover.e.name}
            <div className="small">{hover.e.message}</div>
            <div className="small muted">{hover.e.count}× · {new Date(hover.e.first).toLocaleString()}{hover.e.last !== hover.e.first ? ` → ${new Date(hover.e.last).toLocaleTimeString()}` : ''}</div>
          </div>
        )}
        {!events.length && !error && <div className="tl-empty muted">Aguardando eventos…</div>}
      </div>
      {selected && (
        <div className="tl-detail" data-testid="timeline-detail">
          <div>
            <span className={`status s-${selected.type === 'Warning' ? 'error' : 'ok'}`}>{selected.type}</span> <b>{selected.reason}</b> · {selected.namespace}/{selected.kind}/{selected.name}
            {selected.inferred && <span className="muted small"> (hierarquia inferida pelo nome: objeto já removido)</span>}
          </div>
          <div className="wrap">{selected.message}</div>
          <div className="muted small">
            {selected.count}× · primeiro {new Date(selected.first).toLocaleString()} · último {new Date(selected.last).toLocaleString()} {selected.source ? `· ${selected.source}` : ''}
          </div>
          <div className="row">
            <button onClick={() => openObject(clusterId, selected.group ?? '', selected.kind, selected.name, selected.namespace)}>Abrir objeto</button>
            <button onClick={() => setSelected(undefined)}>Fechar</button>
          </div>
        </div>
      )}
    </div>
  )
}

export function TimelinePanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Timeline clusterId={params.clusterId} />
    </ClusterGate>
  )
}
