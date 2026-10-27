import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, History } from 'lucide-react'
import { api7, type HistoryEntry, type HistoryQuery } from '../../api'
import { absDate, ago } from '../../format'
import { Empty, ErrorBanner, Loading, useAsync } from '../common'
import { VirtualList } from '../../components/VirtualList'
import { openObject } from '../../components/ObjectPanel'
import { summary } from './format'
import './history.css'

const TYPE_LABEL: Record<HistoryEntry['type'], string> = { created: 'criado', updated: 'alterado', deleted: 'excluído' }
const TYPE_CLASS: Record<HistoryEntry['type'], string> = { created: 'ok', updated: 'info', deleted: 'error' }

function show(v: unknown) {
  if (v === undefined) return ''
  const s = typeof v === 'string' ? v : JSON.stringify(v)
  return s.length > 400 ? s.slice(0, 400) + '…' : s
}

function Ops({ e }: { e: HistoryEntry }) {
  if (!e.ops?.length) return <div className="muted small hist-none">{e.type === 'updated' ? 'Sem detalhes.' : e.type === 'created' ? 'Objeto criado.' : 'Objeto excluído.'}</div>
  return (
    <div className="hist-ops">
      {e.keysOnly && <div className="muted small">Valores não registrados (Secret/ConfigMap): só as chaves que mudaram.</div>}
      <table className="table hist-table">
        <tbody>
          {e.ops.map((o, i) => (
            <tr key={i}>
              <td className={`hist-op hist-${o.op}`}>{o.op === 'add' ? '+' : o.op === 'remove' ? '−' : '~'}</td>
              <td className="mono small hist-path">{o.path}</td>
              <td className="mono small wrap">
                {o.op !== 'add' && o.old !== undefined && <span className="hist-old">{show(o.old)}</span>}
                {o.op === 'replace' && o.old !== undefined && <span className="muted"> → </span>}
                {o.op !== 'remove' && o.value !== undefined && <span className="hist-new">{show(o.value)}</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {e.truncated && <div className="muted small">Mudança grande: só as primeiras operações foram guardadas.</div>}
    </div>
  )
}

/**
 * Change history (newest first) with an expandable JSON Patch per entry. `showObject` adds the object column (namespace
 * or cluster view); `text` filters the loaded entries by kind, name or path.
 */
export function HistoryList({ clusterId, query, showObject, text = '', onCount }: { clusterId: string; query: HistoryQuery; showObject?: boolean; text?: string; onCount?: (n: number, retentionDays: number) => void }) {
  const qkey = JSON.stringify(query)
  const { data, error, loading } = useAsync(() => api7.history(clusterId, { ...query, limit: 200 }), [clusterId, qkey], { every: 5000 })
  const [older, setOlder] = useState<HistoryEntry[]>([])
  const [next, setNext] = useState<number>()
  const [open, setOpen] = useState<Set<number>>(() => new Set())
  useEffect(() => {
    setOlder([])
    setNext(undefined)
  }, [clusterId, qkey])
  useEffect(() => setNext((n) => (older.length ? n : data?.next)), [data, older.length])

  const entries = useMemo(() => {
    const seen = new Set<number>()
    const all = [...(data?.entries ?? []), ...older].filter((e) => !seen.has(e.id) && seen.add(e.id))
    const t = text.trim().toLowerCase()
    return t ? all.filter((e) => `${e.kind} ${e.namespace ?? ''}/${e.name} ${(e.ops ?? []).map((o) => o.path).join(' ')}`.toLowerCase().includes(t)) : all
  }, [data, older, text])
  useEffect(() => onCount?.(entries.length, data?.retentionDays ?? 7), [entries.length, data?.retentionDays, onCount])

  const more = async () => {
    if (!next) return
    const p = await api7.history(clusterId, { ...query, limit: 200, before: next })
    setOlder((o) => [...o, ...p.entries])
    setNext(p.next)
  }
  const toggle = (id: number) => setOpen((s) => {
    const n = new Set(s)
    if (n.has(id)) n.delete(id)
    else n.add(id)
    return n
  })

  if (error) return <ErrorBanner error={error} />
  if (!data && loading) return <Loading label="Carregando histórico…" />
  return (
    <div className="hist-wrap">
    <VirtualList
      className="hist-list"
      testId="history-list"
      items={entries}
      getKey={(e) => e.id}
      rowHeight={36}
      empty={
        <Empty icon={<History size={28} />} title="Nenhuma mudança registrada" testId="history-empty">
          O Leme registra criações, alterações da spec, rótulos e anotações e exclusões enquanto alguma tela observa o recurso
          (grade, topologia, mapa…). O histórico fica neste computador por {data?.retentionDays ?? 7} dias.
        </Empty>
      }
      renderRow={(e) => {
        const isOpen = open.has(e.id)
        return (
          <div className={`hist-row ${isOpen ? 'open' : ''}`} data-testid="history-entry" data-type={e.type}>
            <button className="hist-head" aria-expanded={isOpen} onClick={() => toggle(e.id)}>
              {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <span className="hist-time" title={absDate(e.time)}>{ago(e.time)}</span>
              <span className={`pill pill-${TYPE_CLASS[e.type]}`}>{TYPE_LABEL[e.type]}</span>
              {showObject && (
                <span className="hist-obj" title={`${e.kind} ${e.namespace ? e.namespace + '/' : ''}${e.name}`}>
                  <span className="muted">{e.kind}</span> {e.namespace ? <span className="muted">{e.namespace}/</span> : null}<b>{e.name}</b>
                </span>
              )}
              <span className="hist-sum mono small muted">{summary(e.ops)}</span>
            </button>
            {isOpen && (
              <div className="hist-body">
                <Ops e={e} />
                {showObject && e.type !== 'deleted' && (
                  <button className="link small" onClick={() => openObject(clusterId, e.group, e.kind, e.name, e.namespace)}>Abrir objeto</button>
                )}
              </div>
            )}
          </div>
        )
      }}
      rowClassName="hist-vrow"
    />
    {next !== undefined && next > 0 && (
      <div className="hist-more">
        <button onClick={more} data-testid="history-more">Carregar mais antigos</button>
      </div>
    )}
    </div>
  )
}
