import { useEffect, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api, openSocket } from '../api'
import { findResource } from '../store'
import { Xterm } from './Xterm'

export interface TerminalParams {
  clusterId: string
  namespace: string
  pod: string
}

export function TerminalPanel({ params }: IDockviewPanelProps<TerminalParams>) {
  const { clusterId, namespace, pod } = params
  const [containers, setContainers] = useState<string[]>()
  const [container, setContainer] = useState('')
  const [status, setStatus] = useState('')
  const [session, setSession] = useState(0)

  useEffect(() => {
    const res = findResource(clusterId, '', 'pods')
    if (!res) return
    api.object(clusterId, { group: '', version: res.version, resource: 'pods', namespace, name: pod }).then((v) => {
      const names = (v.object.spec?.containers ?? []).map((c: { name: string }) => c.name)
      setContainers(names)
      setContainer(names[0] ?? '')
    })
  }, [clusterId, namespace, pod])

  return (
    <div className="term-panel">
      <div className="toolbar">
        <select value={container} onChange={(e) => setContainer(e.target.value)}>
          {(containers ?? []).map((c) => (
            <option key={c}>{c}</option>
          ))}
        </select>
        <button onClick={() => setSession((n) => n + 1)}>Reconectar</button>
        <span className="muted">{namespace}/{pod} · {status}</span>
      </div>
      {container && (
        <Xterm
          key={`${container}:${session}`}
          options={{ cursorBlink: true }}
          onReady={({ term, fit }) => {
            setStatus('conectando…')
            const ws = openSocket(`/api/clusters/${clusterId}/exec`, { namespace, pod, container })
            ws.binaryType = 'arraybuffer'
            const enc = new TextEncoder()
            const sendSize = () => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
            ws.onopen = () => {
              setStatus('conectado')
              fit.fit()
              sendSize()
              term.focus()
            }
            ws.onmessage = (ev) => {
              if (typeof ev.data === 'string') {
                const m = JSON.parse(ev.data)
                if (m.type === 'exit') {
                  term.write(`\r\n\x1b[2m[sessão encerrada${m.error ? `: ${m.error}` : ''}]\x1b[0m\r\n`)
                  setStatus(m.error ? 'erro' : 'encerrado')
                }
                return
              }
              term.write(new Uint8Array(ev.data as ArrayBuffer))
            }
            ws.onclose = () => setStatus((s) => (s === 'conectado' ? 'encerrado' : s))
            const d1 = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)))
            const d2 = term.onResize(sendSize)
            return () => {
              d1.dispose()
              d2.dispose()
              ws.close()
            }
          }}
        />
      )}
    </div>
  )
}
