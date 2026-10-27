import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy, RefreshCw } from 'lucide-react'
import { useApp } from '../store'
import { usePolling as usePollingInternal } from '../hooks/usePolling'
import './base.css'

export { usePolling, type PollingOptions } from '../hooks/usePolling'
export { usePanelVisible } from '../hooks/visibility'

export interface AsyncOptions {
  /** Re-run every `every` ms while the panel is visible (paused when hidden, one refresh when shown again). */
  every?: number
  /**
   * Shares results between `useAsync` calls with the same key: an in-flight request is reused, and a result younger
   * than `maxAge` is served without a new request. The key must identify the request (include its arguments, e.g.
   * `security:${clusterId}:${ns}`). An explicit `reload()` always fetches again (and refreshes the cache).
   */
  key?: string
  /** How long a cached result stays fresh, in ms (default 5000). Only with `key`. */
  maxAge?: number
}

interface CacheEntry { t: number; p: Promise<unknown> }
const asyncCache = new Map<string, CacheEntry>()
const CACHE_MAX = 200

function cached<T>(key: string, maxAge: number, force: boolean, fn: () => Promise<T>): Promise<T> {
  const hit = asyncCache.get(key)
  if (!force && hit && Date.now() - hit.t < maxAge) return hit.p as Promise<T>
  const p = fn()
  const entry = { t: Date.now(), p }
  asyncCache.delete(key)
  asyncCache.set(key, entry)
  // A failure is never served from the cache.
  p.catch(() => asyncCache.get(key) === entry && asyncCache.delete(key))
  if (asyncCache.size > CACHE_MAX) asyncCache.delete(asyncCache.keys().next().value!)
  return p
}

/**
 * Runs an async loader when deps change. The third argument is either the polling interval in ms (legacy form) or
 * options (`every`, `key`, `maxAge`). Polling pauses while the panel or its window is hidden.
 */
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[], opts: number | AsyncOptions = 0) {
  const o: AsyncOptions = typeof opts === 'number' ? { every: opts } : opts
  const every = o.every ?? 0
  const key = o.key
  const maxAge = o.maxAge ?? 5000
  const [data, setData] = useState<T>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(true)
  const seq = useRef(0)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const run = useCallback(fn, deps)
  const load = useCallback(async (force: boolean) => {
    const my = ++seq.current
    setLoading(true)
    try {
      const d = await (key ? cached(key, maxAge, force, run) : run())
      if (my === seq.current) {
        setData(d)
        setError(undefined)
      }
    } catch (e) {
      if (my === seq.current) setError((e as Error).message)
    } finally {
      if (my === seq.current) setLoading(false)
    }
  }, [run, key, maxAge])
  // `reload` is often passed straight to onClick: it takes no arguments and always fetches fresh data.
  const reload = useCallback(() => load(true), [load])
  const { markRun } = usePollingInternal(() => load(false), every)
  useEffect(() => {
    markRun()
    load(false)
  }, [load, markRun])
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

/** Standard refresh action for tool-panel toolbars; place it after the `.spacer`. */
export function RefreshButton({ onClick, loading }: { onClick: () => void; loading?: boolean }) {
  return (
    <button onClick={onClick}>
      <RefreshCw size={14} className={loading ? 'spin-icon' : ''} /> Recarregar
    </button>
  )
}

/** Standard tool-panel header: icon + title, followed by the panel's own controls. */
export function PanelHeader({ icon, title, children }: { icon?: ReactNode; title: string; children?: ReactNode }) {
  return (
    <div className="toolbar">
      <span className="grid-title">{icon}{title}</span>
      {children}
    </div>
  )
}

/** Standard loading state: a spinner and an optional label, inline or centered in the panel. */
export function Loading({ label, inline }: { label?: string; inline?: boolean }) {
  return (
    <div className={inline ? 'ui-loading inline' : 'ui-loading'} role="status" aria-live="polite" data-testid="loading">
      <span className="spinner" />
      {label && <span className="muted">{label}</span>}
    </div>
  )
}

/** Copyable code block (commands, YAML snippets) with a copy button. */
export function CodeBlock({ code, label = 'Copiar' }: { code: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(code).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      },
      () => {},
    )
  }
  return (
    <div className="ui-code">
      <pre>{code}</pre>
      <button className="icon-btn ui-copy" title={label} aria-label={label} onClick={copy} data-testid="code-copy">
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </div>
  )
}

/**
 * Empty state that teaches: optional icon, title, text (children), a copyable code block and actions.
 * `<Empty>texto</Empty>` keeps the compact look (muted padded text).
 */
export function Empty({ icon, title, children, code, actions, testId }: { icon?: ReactNode; title?: string; children?: ReactNode; code?: string; actions?: ReactNode; testId?: string }) {
  if (!icon && !title && !code && !actions) return <div className="pad muted" data-testid={testId}>{children}</div>
  return (
    <div className="ui-empty" data-testid={testId ?? 'empty'}>
      {icon && <div className="ui-empty-icon">{icon}</div>}
      {title && <div className="ui-empty-title">{title}</div>}
      {children && <div className="ui-empty-text">{children}</div>}
      {code && <CodeBlock code={code} />}
      {actions && <div className="ui-empty-actions">{actions}</div>}
    </div>
  )
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
