import { useEffect, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, openSocket } from '../api'
import { findResource } from '../store'
import { Xterm, type XtermHandle } from './Xterm'

// Follow mode can stream for hours: the download buffer keeps only the most recent ~5 MB.
const BUFFER_MAX = 5 * 1024 * 1024

/** Append-only text buffer capped by size: the oldest chunks are dropped (and counted) past `max` characters. */
export class LogRing {
  private chunks: string[] = []
  private head = 0
  size = 0
  dropped = 0
  constructor(private max = BUFFER_MAX) {}
  push(text: string) {
    this.chunks.push(text)
    this.size += text.length
    while (this.size > this.max && this.head < this.chunks.length - 1) {
      const old = this.chunks[this.head]
      this.chunks[this.head++] = ''
      this.size -= old.length
      this.dropped += old.length
    }
    // Compact the array now and then instead of shifting on every drop.
    if (this.head > 1024 && this.head * 2 > this.chunks.length) {
      this.chunks = this.chunks.slice(this.head)
      this.head = 0
    }
  }
  clear() {
    this.chunks = []
    this.head = 0
    this.size = 0
    this.dropped = 0
  }
  parts(): string[] {
    return this.chunks.slice(this.head)
  }
}

export interface LogsParams {
  clusterId: string
  namespace: string
  pod: string
}

export function LogsPanel({ params }: IDockviewPanelProps<LogsParams>) {
  const { clusterId, namespace, pod } = params
  const [containers, setContainers] = useState<string[]>([])
  const [container, setContainer] = useState('')
  const [follow, setFollow] = useState(true)
  const [previous, setPrevious] = useState(false)
  const [timestamps, setTimestamps] = useState(false)
  const [tail, setTail] = useState(500)
  const [q, setQ] = useState('')
  const [status, setStatus] = useState('conectando…')
  const handle = useRef<XtermHandle>(undefined)
  const [ready, setReady] = useState(false)
  const [ring] = useState(() => new LogRing())
  const [truncated, setTruncated] = useState(false)

  useEffect(() => {
    const res = findResource(clusterId, '', 'pods')
    if (!res) return
    api.object(clusterId, { group: '', version: res.version, resource: 'pods', namespace, name: pod }).then((v) => {
      const names = [...(v.object.spec?.initContainers ?? []), ...(v.object.spec?.containers ?? [])].map((c: { name: string }) => c.name)
      setContainers(names)
      setContainer((c) => c || v.object.spec?.containers?.[0]?.name || names[0] || '')
    })
  }, [clusterId, namespace, pod])

  useEffect(() => {
    if (!container || !ready || !handle.current) return
    const { term } = handle.current
    term.reset()
    ring.clear()
    setTruncated(false)
    setStatus('conectando…')
    const ws = openSocket(`/api/clusters/${clusterId}/logs`, { namespace, pod, container, follow: follow ? 1 : 0, previous: previous ? 1 : 0, timestamps: timestamps ? 1 : 0, tail })
    ws.onopen = () => setStatus(follow ? 'ao vivo' : 'carregado')
    ws.onmessage = (ev) => {
      const text = String(ev.data)
      ring.push(text)
      if (ring.dropped) setTruncated(true)
      term.write(text.replace(/\r?\n/g, '\r\n'))
    }
    ws.onclose = () => setStatus((s) => (s === 'ao vivo' ? 'encerrado' : s === 'conectando…' ? 'falhou' : s))
    return () => ws.close()
  }, [clusterId, namespace, pod, container, follow, previous, timestamps, tail, ready])

  const download = () => {
    const b = ring
    const note = b.dropped ? [`[Leme] arquivo truncado: as primeiras ${(b.dropped / 1024 / 1024).toFixed(1)} MB do stream foram descartadas (limite de ${BUFFER_MAX / 1024 / 1024} MB).\n`] : []
    const blob = new Blob([...note, ...b.parts()], { type: 'text/plain' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${pod}-${container}.log`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <div className="term-panel">
      <div className="toolbar">
        <select value={container} onChange={(e) => setContainer(e.target.value)}>
          {containers.map((c) => (
            <option key={c}>{c}</option>
          ))}
        </select>
        <label className="check"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> follow</label>
        <label className="check"><input type="checkbox" checked={previous} onChange={(e) => setPrevious(e.target.checked)} /> anterior</label>
        <label className="check"><input type="checkbox" checked={timestamps} onChange={(e) => setTimestamps(e.target.checked)} /> timestamps</label>
        <select value={tail} onChange={(e) => setTail(Number(e.target.value))} title="Linhas iniciais">
          {[100, 500, 2000, 10000].map((n) => (
            <option key={n} value={n}>{n} linhas</option>
          ))}
        </select>
        <input className="search" placeholder="Buscar…" value={q} onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handle.current?.search.findNext(q, { caseSensitive: false })} />
        <button onClick={() => handle.current?.term.clear()}>Limpar</button>
        <button onClick={download} title={truncated ? `O download traz só os últimos ${BUFFER_MAX / 1024 / 1024} MB` : undefined}>Baixar</button>
        {truncated && <span className="muted" data-testid="logs-truncated" title={`O buffer de download guarda os últimos ${BUFFER_MAX / 1024 / 1024} MB`}>buffer: últimos {BUFFER_MAX / 1024 / 1024} MB</span>}
        <span className="muted">{status}</span>
      </div>
      <Xterm options={{ disableStdin: true, convertEol: false }} onReady={(h) => {
        handle.current = h
        setReady(true)
        return () => setReady(false)
      }} />
    </div>
  )
}
