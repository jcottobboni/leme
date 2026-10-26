import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useApp } from '../store'

/** Runs an async loader when deps change; optionally re-runs every `every` ms. */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[], every = 0) {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const seq = useRef(0)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(fn, deps)
  const reload = useCallback(async () => {
    const my = ++seq.current
    setLoading(true)
    try {
      const d = await run()
      if (my === seq.current) {
        setData(d)
        setError(undefined)
      }
    } catch (e) {
      if (my === seq.current) setError((e as Error).message)
    } finally {
      if (my === seq.current) setLoading(false)
    }
  }, [run])
  useEffect(() => {
    reload()
    if (!every) return
    const t = setInterval(reload, every)
    return () => clearInterval(t)
  }, [reload, every])
  return { data, error, loading, reload, setData }
}

export function ErrorBanner({ error }: { error?: string }) {
  return error ? <div className="banner error">{error}</div> : null
}

export function Partial({ errors }: { errors?: Record<string, string> | null }) {
  const list = Object.entries(errors ?? {}).filter(([, v]) => v)
  if (!list.length) return null
  return (
    <div className="banner warn" title={list.map(([k, v]) => `${k}: ${v}`).join('\n')}>
      Resultado parcial: sem acesso a {list.map(([k]) => k.replace(/^\//, '')).join(', ')}.
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="pad muted">{children}</div>
}

/** Shown in panels whose cluster is not connected (e.g. after restoring a saved layout). */
export function ClusterGate({ clusterId, children }: { clusterId?: string; children: ReactNode }) {
  const connected = useApp((s) => (clusterId ? !!s.connected[clusterId] : true))
  const connecting = useApp((s) => (clusterId ? s.connecting[clusterId] : false))
  const name = useApp((s) => s.clusters.find((c) => c.id === clusterId)?.name ?? clusterId)
  if (connected) return <>{children}</>
  return (
    <div className="pad">
      <p className="muted">O cluster <b>{name}</b> não está conectado.</p>
      <button className="primary" disabled={connecting} onClick={() => clusterId && useApp.getState().connect(clusterId)}>
        {connecting ? 'Conectando…' : 'Conectar'}
      </button>
    </div>
  )
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: [T, string][]; value: T; onChange: (t: T) => void }) {
  return (
    <div className="tabs">
      {tabs.map(([k, label]) => (
        <button key={k} className={`tab ${value === k ? 'active' : ''}`} onClick={() => onChange(k)}>
          {label}
        </button>
      ))}
    </div>
  )
}

export function Bar({ value, max, label }: { value: number; max: number; label?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0
  const cls = pct >= 90 ? 'error' : pct >= 70 ? 'warn' : 'ok'
  return (
    <div className="bar" title={label}>
      <div className={`bar-fill b-${cls}`} style={{ width: `${pct}%` }} />
    </div>
  )
}

export function pct(v: number, max: number) {
  return max > 0 ? `${Math.round((v / max) * 100)}%` : '—'
}
