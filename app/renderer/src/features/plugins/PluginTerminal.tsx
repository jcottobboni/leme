import { useEffect, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { openSocket } from '../../api'
import { Xterm } from '../../components/Xterm'
import type { PluginInput } from './api'

export interface PluginTermParams {
  clusterId: string
  action: string
  title: string
  input: PluginInput
}

/** Terminal of a plugin action (k9s plugins without background): same protocol as the pod shell. */
export function PluginTerminalPanel({ params, api: panelApi }: IDockviewPanelProps<PluginTermParams>) {
  const { clusterId, action, input, title } = params
  const [status, setStatus] = useState('')
  const [session, setSession] = useState(0)
  useEffect(() => panelApi.setTitle(`${title} · ${input.name}`), [panelApi, title, input.name])
  return (
    <div className="term-panel" data-testid="plugin-term">
      <div className="toolbar">
        <button onClick={() => setSession((n) => n + 1)}>Executar de novo</button>
        <span className="muted">{title} · {input.namespace ? `${input.namespace}/` : ''}{input.name} · <span data-testid="plugin-term-status">{status}</span></span>
      </div>
      <Xterm
        key={session}
        options={{ cursorBlink: true }}
        onReady={({ term, fit }) => {
          try {
            fit.fit()
          } catch {
            /* not visible yet */
          }
          setStatus('iniciando…')
          const ws = openSocket(`/api/clusters/${clusterId}/plugins/term`, { action, input: JSON.stringify(input), cols: term.cols, rows: term.rows })
          ws.binaryType = 'arraybuffer'
          const enc = new TextEncoder()
          const sendSize = () => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
          ws.onopen = () => {
            setStatus('executando')
            term.focus()
          }
          ws.onmessage = (ev) => {
            if (typeof ev.data === 'string') {
              const m = JSON.parse(ev.data)
              // Without a PTY (Windows) the output has bare \n.
              if (m.type === 'info' && m.pty === false) term.options.convertEol = true
              if (m.type === 'exit') {
                term.write(`\r\n\x1b[2m[encerrado: saída ${m.code}${m.error ? ` · ${m.error}` : ''}]\x1b[0m\r\n`)
                setStatus(m.code === 0 && !m.error ? 'concluído' : `saída ${m.code}`)
              }
              return
            }
            term.write(new Uint8Array(ev.data as ArrayBuffer))
          }
          ws.onerror = () => setStatus('falhou (veja o motivo no Problemas ou no log de auditoria)')
          ws.onclose = () => setStatus((s) => (s === 'executando' || s === 'iniciando…' ? 'encerrado' : s))
          const d1 = term.onData((d) => ws.readyState === WebSocket.OPEN && ws.send(enc.encode(d)))
          const d2 = term.onResize(sendSize)
          return () => {
            d1.dispose()
            d2.dispose()
            ws.close()
          }
        }}
      />
    </div>
  )
}
