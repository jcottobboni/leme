import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { Download, Eraser, Logs, Pause, Regex, RotateCw, ScrollText, Timer } from 'lucide-react'
import { openSocket } from '../../api'
import { PALETTE } from '../../store'
import { PanelHeader } from '../../components/PanelHeader'
import { Xterm, type XtermHandle } from '../../components/Xterm'
import { BUFFER_MAX, LogRing } from '../../components/logRing'
import type { MultiLogsParams } from './open'
import './multilogs.css'

interface PodInfo { name: string; containers: string[]; phase?: string; streaming: boolean }
interface LogLine { pod: string; container: string; ts?: string; text: string }
type Msg =
  | { type: 'selector'; selector: string; namespace: string; max: number }
  | { type: 'pods'; pods?: PodInfo[]; matched?: number; max?: number }
  | { type: 'join' | 'leave'; pod: string; time: string }
  | { type: 'lines'; lines: LogLine[] }
  | { type: 'dropped'; dropped: number }
  | { type: 'error'; error: string }

/** One entry of the buffer: a log line or a marker (pod joined/left, lines dropped). */
type Rec =
  | { k: 'line'; pod: string; ctr: string; ts?: string; text: string }
  | { k: 'mark'; ev: 'join' | 'leave' | 'dropped'; pod?: string; time?: string; n?: number }

const recSize = (r: Rec) => (r.k === 'line' ? r.text.length + r.pod.length + r.ctr.length + 48 : 64)

const RESET = '\x1b[0m'
const DIM = '\x1b[2m'

function rgb(hex: string) {
  const n = parseInt(hex.slice(1), 16)
  return `\x1b[38;2;${(n >> 16) & 255};${(n >> 8) & 255};${n & 255}m`
}

// Log text comes from the cluster: keep colors (SGR) but drop every other escape sequence (titles, hyperlinks,
// cursor moves, mode switches) so a log line cannot rewrite the screen or the window.
// eslint-disable-next-line no-control-regex
const ESCAPES = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b\[[0-?]*[ -/]*[@-ln-~]|\x1b(?!\[)|[\x00-\x08\x0b-\x1a\x1c-\x1f\x7f]/g
export function sanitize(text: string) {
  return text.replace(ESCAPES, '')
}

function hhmmss(ts?: string) {
  if (!ts) return ''
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleTimeString(undefined, { hour12: false }) + '.' + String(d.getMilliseconds()).padStart(3, '0')
}

interface Filter { re?: RegExp; text: string; hidden: Set<string> }

function visible(r: Rec, f: Filter) {
  if (r.k === 'mark') return true
  if (f.hidden.has(r.pod)) return false
  if (f.re) return f.re.test(r.text)
  return !f.text || r.text.toLowerCase().includes(f.text)
}

export function MultiLogsPanel({ params }: IDockviewPanelProps<MultiLogsParams>) {
  const { clusterId, namespace } = params
  const [selInput, setSelInput] = useState(params.selector ?? '')
  const [selector, setSelector] = useState(params.selector ?? '')
  const [resolved, setResolved] = useState('')
  const [pods, setPods] = useState<PodInfo[]>([])
  const [matched, setMatched] = useState(0)
  const [max, setMax] = useState(20)
  const [status, setStatus] = useState('conectando…')
  const [error, setError] = useState<string>()
  const [follow, setFollow] = useState(true)
  const [paused, setPaused] = useState(false)
  const [timestamps, setTimestamps] = useState(false)
  const [q, setQ] = useState('')
  const [regex, setRegex] = useState(false)
  const [hidden, setHidden] = useState<Set<string>>(() => new Set())
  const [gen, setGen] = useState(0)
  const [count, setCount] = useState(0)
  const [truncated, setTruncated] = useState(false)
  const [ready, setReady] = useState(false)
  const handle = useRef<XtermHandle>(undefined)
  const ring = useMemo(() => new LogRing<Rec>(BUFFER_MAX, recSize), [])
  const colors = useRef(new Map<string, string>())
  const multi = useRef(new Set<string>()) // pods with more than one container: prefix shows the container
  const width = useRef(12)

  const reError = useMemo(() => {
    if (!regex || !q) return undefined
    try {
      new RegExp(q)
      return undefined
    } catch (e) {
      return (e as Error).message
    }
  }, [q, regex])
  const filter: Filter = useMemo(() => ({ re: regex && q && !reError ? new RegExp(q, 'i') : undefined, text: regex ? '' : q.toLowerCase(), hidden }), [q, regex, reError, hidden])
  const view = useRef({ filter, timestamps, paused, follow })
  view.current = { filter, timestamps, paused, follow }

  const colorOf = (pod: string) => {
    let c = colors.current.get(pod)
    if (!c) {
      c = PALETTE[colors.current.size % PALETTE.length]
      colors.current.set(pod, c)
    }
    return c
  }

  const format = useCallback((r: Rec, ts: boolean) => {
    if (r.k === 'mark') {
      const when = r.time ? ` ${hhmmss(r.time)}` : ''
      if (r.ev === 'join') return `\x1b[32m── ▶ pod ${r.pod} entrou${when} ──${RESET}\r\n`
      if (r.ev === 'leave') return `\x1b[33m── ■ pod ${r.pod} saiu${when} ──${RESET}\r\n`
      return `\x1b[31m── ${r.n} linhas descartadas (o painel não acompanhou o ritmo) ──${RESET}\r\n`
    }
    const prefix = multi.current.has(r.pod) ? `${r.pod}/${r.ctr}` : r.pod
    const pad = prefix.length < width.current ? ' '.repeat(width.current - prefix.length) : ''
    return `${ts && r.ts ? `${DIM}${hhmmss(r.ts)}${RESET} ` : ''}${rgb(colorOf(r.pod))}${prefix}${pad}${RESET} │ ${sanitize(r.text)}${RESET}\r\n`
  }, [])

  // Writes the whole buffer again (filter, timestamps or pod visibility changed, or resumed).
  const redraw = useCallback(() => {
    const term = handle.current?.term
    if (!term) return
    term.reset()
    const { filter: f, timestamps: ts } = view.current
    let out = ''
    for (const r of ring.parts()) if (visible(r, f)) out += format(r, ts)
    term.write(out)
    term.scrollToBottom()
  }, [ring, format])

  useEffect(() => {
    if (!paused) redraw()
  }, [filter, timestamps, paused, redraw])

  useEffect(() => {
    if (!ready) return
    ring.clear()
    colors.current.clear()
    setCount(0)
    setTruncated(false)
    setError(undefined)
    setPods([])
    handle.current?.term.reset()
    setStatus('conectando…')
    const query = selector
      ? { namespace, selector }
      : { namespace, group: params.group, resource: params.resource, kind: params.kind, name: params.name }
    const ws = openSocket(`/api/clusters/${clusterId}/logs/multi`, query)
    let lines = 0
    const add = (recs: Rec[]) => {
      let out = ''
      for (const r of recs) {
        ring.push(r)
        if (r.k === 'line') lines++
        if (!view.current.paused && visible(r, view.current.filter)) out += format(r, view.current.timestamps)
      }
      if (out && handle.current) {
        handle.current.term.write(out)
        if (view.current.follow) handle.current.term.scrollToBottom()
      }
      setCount(lines)
      if (ring.dropped) setTruncated(true)
    }
    ws.onopen = () => setStatus('ao vivo')
    ws.onmessage = (ev) => {
      let m: Msg
      try {
        m = JSON.parse(String(ev.data))
      } catch {
        return
      }
      switch (m.type) {
        case 'selector':
          setResolved(m.selector)
          setSelInput((s) => s || m.selector)
          if (m.max) setMax(m.max)
          break
        case 'pods':
          for (const p of m.pods ?? []) {
            colorOf(p.name)
            if (p.containers.length > 1) multi.current.add(p.name)
            const w = (p.containers.length > 1 ? p.name.length + 1 + Math.max(...p.containers.map((c) => c.length)) : p.name.length)
            width.current = Math.min(48, Math.max(width.current, w))
          }
          setPods(m.pods ?? [])
          setMatched(m.matched ?? 0)
          if (m.max) setMax(m.max)
          break
        case 'join':
        case 'leave':
          add([{ k: 'mark', ev: m.type, pod: m.pod, time: m.time }])
          break
        case 'lines':
          add(m.lines.map((l) => ({ k: 'line', pod: l.pod, ctr: l.container, ts: l.ts, text: l.text })))
          break
        case 'dropped':
          add([{ k: 'mark', ev: 'dropped', n: m.dropped }])
          break
        case 'error':
          setError(m.error)
          break
      }
    }
    ws.onclose = () => setStatus((s) => (s === 'conectando…' ? 'falhou' : 'encerrado'))
    return () => ws.close()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterId, namespace, selector, params.group, params.resource, params.kind, params.name, gen, ready])

  const download = () => {
    const note = ring.dropped ? [`[Leme] arquivo truncado: as linhas mais antigas foram descartadas (limite de ${BUFFER_MAX / 1024 / 1024} MB).\n`] : []
    const body = ring.parts().map((r) => {
      if (r.k === 'mark') return r.ev === 'dropped' ? `[Leme] ${r.n} linhas descartadas\n` : `[Leme] pod ${r.pod} ${r.ev === 'join' ? 'entrou' : 'saiu'} ${r.time ?? ''}\n`
      return `${r.ts ? r.ts + ' ' : ''}${r.pod}/${r.ctr} ${r.text}\n`
    })
    const blob = new Blob([...note, ...body], { type: 'text/plain' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${params.name ?? 'pods'}-${namespace}.log`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const toggle = (pod: string) => setHidden((h) => {
    const n = new Set(h)
    if (n.has(pod)) n.delete(pod)
    else n.add(pod)
    return n
  })
  const streaming = pods.filter((p) => p.streaming).length

  return (
    <div className="term-panel ml-panel" data-testid="multilogs" data-lines={count}>
      <PanelHeader icon={Logs} title={params.name ? `Logs de ${params.name}` : 'Logs por rótulo'} scope={<span className="chip ml-ns" title="Namespace">{namespace}</span>} actions={[
        { key: 'follow', label: 'Seguir', icon: ScrollText, pressed: follow, title: 'Rolar até a última linha', onClick: () => setFollow((v) => !v) },
        { key: 'pause', label: 'Pausar', icon: Pause, pressed: paused, testid: 'ml-pause', title: 'Pausar a exibição (o stream continua no buffer)', onClick: () => setPaused((v) => !v) },
        { key: 'ts', label: 'Timestamps', icon: Timer, pressed: timestamps, onClick: () => setTimestamps((v) => !v) },
        { key: 'clear', label: 'Limpar', icon: Eraser, onClick: () => { ring.clear(); setCount(0); handle.current?.term.reset() } },
        { key: 'download', label: 'Baixar', icon: Download, title: `Baixar o buffer (últimos ${BUFFER_MAX / 1024 / 1024} MB)`, onClick: download },
        { key: 'reconnect', label: 'Reconectar', icon: RotateCw, iconOnly: true, keep: true, onClick: () => setGen((g) => g + 1) },
      ]}>
        <form className="ml-sel" onSubmit={(e) => { e.preventDefault(); setSelector(selInput.trim()) }}>
          <input className="search mono" aria-label="Seletor de rótulos" placeholder={resolved || 'app=web,tier!=db'} value={selInput} onChange={(e) => setSelInput(e.target.value)} data-testid="ml-selector" title="Seletor de rótulos (Enter aplica)" />
        </form>
        <input className={`search ${reError ? 'invalid' : ''}`} aria-label="Filtrar linhas" placeholder={regex ? 'regex…' : 'Filtrar…'} value={q} onChange={(e) => setQ(e.target.value)} title={reError} data-testid="ml-filter" />
        <button className={`ph-tog icon-btn ${regex ? 'on' : ''}`} aria-pressed={regex} aria-label="Expressão regular" title="Expressão regular" onClick={() => setRegex((v) => !v)}><Regex size={14} /></button>
        <span className="muted small ph-info" data-testid="ml-status">{streaming} {streaming === 1 ? 'pod' : 'pods'} · {paused ? 'pausado' : status}</span>
      </PanelHeader>
      {error && <div className="banner error" data-testid="ml-error">{error}</div>}
      {matched > max && <div className="banner warn" data-testid="ml-cap">Mostrando {max} de {matched} pods (limite de {max}). Refine o seletor para ver os outros.</div>}
      {truncated && <div className="banner info small">Buffer cheio: as linhas mais antigas foram descartadas (limite de {BUFFER_MAX / 1024 / 1024} MB).</div>}
      <div className="ml-legend" role="group" aria-label="Pods">
        {pods.map((p) => (
          <button key={p.name} className={`ml-pod ${hidden.has(p.name) ? 'off' : ''} ${p.streaming ? '' : 'waiting'}`} aria-pressed={!hidden.has(p.name)} data-testid={`ml-pod-${p.name}`} data-streaming={p.streaming}
            title={`${p.name} · ${p.containers.join(', ')}${p.streaming ? '' : ' · fora do limite'}${hidden.has(p.name) ? ' · oculto' : ''}`} onClick={() => toggle(p.name)}>
            <span className="ml-dot" style={{ background: colorOf(p.name) }} />
            {p.name}
            {!p.streaming && <span className="muted small">aguardando</span>}
          </button>
        ))}
        {!pods.length && !error && <span className="muted small">{status === 'ao vivo' ? 'Nenhum pod corresponde ao seletor.' : 'Conectando…'}</span>}
      </div>
      <Xterm options={{ disableStdin: true, convertEol: false }} onReady={(h) => {
        handle.current = h
        setReady(true)
        return () => setReady(false)
      }} />
    </div>
  )
}
