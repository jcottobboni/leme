import { useEffect, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { openSocket } from '../../api'
import { Xterm, type XtermHandle } from '../../components/Xterm'
import { ClusterGate } from '../common'

interface Params {
  clusterId: string
  namespace: string
  build: string
}

function BuildLog({ clusterId, namespace, build }: Params) {
  const handle = useRef<XtermHandle>(undefined)
  const [ready, setReady] = useState(false)
  const [status, setStatus] = useState('conectando…')
  useEffect(() => {
    if (!ready || !handle.current) return
    const { term } = handle.current
    term.reset()
    const ws = openSocket(`/api/clusters/${clusterId}/openshift/buildlog`, { namespace, name: build, follow: 1 })
    ws.onopen = () => setStatus('ao vivo')
    ws.onmessage = (ev) => term.write(String(ev.data).replace(/\r?\n/g, '\r\n'))
    ws.onclose = () => setStatus('encerrado')
    return () => ws.close()
  }, [clusterId, namespace, build, ready])
  return (
    <div className="term-panel">
      <div className="toolbar">
        <b>build {build}</b>
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

export function BuildLogPanel({ params }: IDockviewPanelProps<Params>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <BuildLog {...params} />
    </ClusterGate>
  )
}
