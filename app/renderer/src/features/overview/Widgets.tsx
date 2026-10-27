import { useMemo, useState } from 'react'
import { ArrowDown, ArrowUp, Box, Cpu, Gauge as GaugeIcon, Network, Server } from 'lucide-react'
import type { NamespaceStat, Overview } from '../../api'
import { bytes, cpu } from '../../format'
import { openTopology } from '../topology/Topology'
import '../metrics/charts.css'
import { pressable } from '../../components/a11y'

export const PHASES: [string, string][] = [['Running', 'var(--ok)'], ['Pending', 'var(--warn)'], ['Succeeded', 'var(--accent)'], ['Failed', 'var(--error)'], ['Unknown', 'var(--neutral)']]

const level = (p: number) => (p >= 0.9 ? 'var(--error)' : p >= 0.7 ? 'var(--warn)' : 'var(--ok)')
const percent = (v: number, max: number) => (max > 0 ? Math.round((v / max) * 100) : 0)
const percentLabel = (v: number, max: number) => (max <= 0 ? '—' : v > 0 && v / max < 0.01 ? '<1%' : `${percent(v, max)}%`)

/** Big-number pressure bar: usage over allocatable, tick at requests, limit underneath. */
export function Headroom({ title, icon, use, req, lim, alloc, fmt, testid }: { title: string; icon: React.ReactNode; use?: number; req: number; lim: number; alloc: number; fmt: (n: number) => string; testid: string }) {
  const basis = use ?? req
  const p = alloc > 0 ? Math.min(1, basis / alloc) : 0
  return (
    <div className="hb" data-testid={testid}>
      <div className="hb-top">
        <span className="ov-icon">{icon}</span>
        <span className="hb-title">{title}</span>
        <span className="hb-pct" title={use === undefined ? 'Sem metrics-server: baseado nos requests' : 'Uso atual sobre o alocável'}>{percentLabel(basis, alloc)}</span>
      </div>
      <div className="hb-track">
        <div className="hb-fill" style={{ width: `${p * 100}%`, background: level(p) }} />
        {alloc > 0 && req > 0 && <div className="hb-req" style={{ left: `calc(${Math.min(1, req / alloc) * 100}% - 1px)` }} title={`Reservado ${fmt(req)}`} />}
      </div>
      <div className="hb-meta">
        <div><span>Uso</span><b>{use !== undefined ? `${fmt(use)} / ${fmt(alloc)}` : `sem metrics-server · ${fmt(alloc)}`}</b></div>
        <div><span>Reservado</span><b>{fmt(req)} req</b></div>
      </div>
      <div className="hb-limit">Limit <b>{fmt(lim)}</b> · {percent(lim, alloc)}% do alocável</div>
    </div>
  )
}

function Commit({ icon, title, req, alloc, fmt, unit }: { icon: React.ReactNode; title: string; req: number; alloc: number; fmt: (n: number) => string; unit?: string }) {
  const p = alloc > 0 ? Math.min(1, req / alloc) : 0
  return (
    <div className="commit" data-testid={`commit-${title}`}>
      <div className="commit-top">{icon}{title}<span className="pct">{percentLabel(req, alloc)}</span></div>
      <div className="bar"><div className="bar-fill" style={{ width: `${p * 100}%`, background: level(p) }} /></div>
      <div className="commit-sub"><b>{fmt(req)}</b>{unit} solicitados de {fmt(alloc)}</div>
    </div>
  )
}

export function CommitCards({ o }: { o: Overview }) {
  return (
    <div className="commit-grid">
      <Commit icon={<Box size={15} />} title="Pods" req={o.requests.pods ?? 0} alloc={o.allocatable.pods ?? 0} fmt={String} />
      <Commit icon={<Cpu size={15} />} title="CPU" req={o.requests.cpu} alloc={o.allocatable.cpu} fmt={(m) => cpu(m)} />
      <Commit icon={<Server size={15} />} title="Memória" req={o.requests.memory} alloc={o.allocatable.memory} fmt={bytes} />
    </div>
  )
}

export function PhaseLegend() {
  return <div className="ns-legend">{PHASES.map(([k, c]) => <span key={k}><i style={{ background: c }} />{k}</span>)}</div>
}

export function PodBar({ pods }: { pods: Record<string, number> }) {
  const total = Object.values(pods).reduce((a, b) => a + b, 0)
  if (!total) return <div className="ns-pods"><div className="ns-bar empty" /><b className="muted">0</b></div>
  return (
    <div className="ns-pods">
      <div className="ns-bar" title={PHASES.filter(([k]) => pods[k]).map(([k]) => `${k}: ${pods[k]}`).join('\n')}>
        {PHASES.filter(([k]) => pods[k]).map(([k, c]) => <i key={k} style={{ width: `${(pods[k] / total) * 100}%`, background: c }} />)}
      </div>
      <b>{total}</b>
    </div>
  )
}

type SortKey = 'name' | 'pods' | 'cpu' | 'mem' | 'ucpu' | 'umem'
const podCount = (s: NamespaceStat) => Object.values(s.pods).reduce((a, b) => a + b, 0)
const SORTERS: Record<SortKey, (s: NamespaceStat) => number | string> = {
  name: (s) => s.name,
  pods: podCount,
  cpu: (s) => s.requests.cpu,
  mem: (s) => s.requests.memory,
  ucpu: (s) => s.usage?.cpu ?? -1,
  umem: (s) => s.usage?.memory ?? -1,
}

export function NamespacesTable({ clusterId, stats, active, onPick }: { clusterId: string; stats: NamespaceStat[]; active: string; onPick: (ns: string) => void }) {
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: 'pods', dir: -1 })
  const hasUsage = stats.some((s) => s.usage)
  const rows = useMemo(() => {
    const f = SORTERS[sort.key]
    return [...stats].sort((a, b) => {
      const x = f(a)
      const y = f(b)
      const c = typeof x === 'string' ? x.localeCompare(y as string) : (x as number) - (y as number)
      return (c || a.name.localeCompare(b.name)) * sort.dir
    })
  }, [stats, sort])
  const th = (key: SortKey, label: string, cls = '') => (
    <th className={`sortable ${cls}`} {...pressable(() => setSort((s) => ({ key, dir: s.key === key ? (-s.dir as 1 | -1) : key === 'name' ? 1 : -1 })), null)} aria-sort={sort.key === key ? (sort.dir === 1 ? 'ascending' : 'descending') : undefined}>
      {label}{sort.key === key && <span className="arrow">{sort.dir === 1 ? <ArrowUp size={11} /> : <ArrowDown size={11} />}</span>}
    </th>
  )
  return (
    <table className="table" data-testid="ov-namespaces">
      <thead>
        <tr>{th('name', 'Namespace')}{th('pods', 'Pods')}{th('cpu', 'CPU req', 'num')}{th('mem', 'Mem req', 'num')}{hasUsage && th('ucpu', 'CPU uso', 'num')}{hasUsage && th('umem', 'Mem uso', 'num')}<th className="ns-row-actions" /></tr>
      </thead>
      <tbody>
        {rows.map((s) => (
          <tr key={s.name} className={`clickable${active === s.name ? ' ns-active' : ''}`} data-testid={`ns-row-${s.name}`} {...pressable(() => onPick(active === s.name ? '' : s.name), null)} aria-selected={active === s.name} title="Filtrar por este namespace">
            <td>{s.name}</td>
            <td style={{ minWidth: 220 }}><PodBar pods={s.pods} /></td>
            <td className="num">{cpu(s.requests.cpu)}</td>
            <td className="num">{bytes(s.requests.memory)}</td>
            {hasUsage && <td className="num">{s.usage ? cpu(s.usage.cpu) : '—'}</td>}
            {hasUsage && <td className="num">{s.usage ? bytes(s.usage.memory) : '—'}</td>}
            <td className="ns-row-actions"><button className="icon-btn" title="Abrir topologia" aria-label={`Topologia de ${s.name}`} onClick={(e) => { e.stopPropagation(); openTopology(clusterId, s.name) }}><Network size={14} /></button></td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/** Horizontal bars of the top namespaces by CPU or memory (usage when metrics-server answers, else requests). */
export function TopConsumers({ stats, onPick }: { stats: NamespaceStat[]; onPick: (ns: string) => void }) {
  const [metric, setMetric] = useState<'cpu' | 'memory'>('cpu')
  const live = stats.some((s) => s.usage)
  const val = (s: NamespaceStat) => (live ? (s.usage?.[metric] ?? 0) : s.requests[metric])
  const top = [...stats].sort((a, b) => val(b) - val(a)).filter((s) => val(s) > 0).slice(0, 8)
  const max = top[0] ? val(top[0]) : 0
  const fmt = metric === 'cpu' ? cpu : bytes
  return (
    <div data-testid="ov-top">
      <div className="ov-section-head">
        <h4 title={live ? 'Por uso atual (metrics-server)' : 'Por requests (sem metrics-server)'}><GaugeIcon size={14} /> Top consumidores</h4>
        <span className="spacer" />
        <div className="seg">
          <button className={metric === 'cpu' ? 'on' : ''} onClick={() => setMetric('cpu')}>CPU</button>
          <button className={metric === 'memory' ? 'on' : ''} onClick={() => setMetric('memory')}>Memória</button>
        </div>
      </div>
      {top.length === 0 && <div className="ov-empty"><GaugeIcon size={20} />Nenhum consumo para exibir.</div>}
      <div className="top-bars">
        {top.map((s) => (
          <div className="top-row" key={s.name} {...pressable(() => onPick(s.name))} title={`Filtrar por ${s.name}`}>
            <span className="top-name">{s.name}</span>
            <div className="top-track"><div className={`top-fill${metric === 'memory' ? ' mem' : ''}`} style={{ width: `${max ? (val(s) / max) * 100 : 0}%` }} /></div>
            <span className="top-val">{fmt(val(s))}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

/** Histogram of warning events over time (24 buckets from the oldest warning to now). */
export function WarnSpark({ warnings }: { warnings: Overview['warnings'] }) {
  const times = warnings.map((w) => new Date(w.last).getTime()).filter((t) => t > 0)
  if (times.length < 1) return null
  const now = Date.now()
  const start = Math.min(...times)
  const span = Math.max(now - start, 10 * 60 * 1000)
  const N = 24
  const buckets = new Array(N).fill(0)
  for (const t of times) buckets[Math.min(N - 1, Math.floor(((t - (now - span)) / span) * N))]++
  const max = Math.max(...buckets, 1)
  const W = 480
  const bw = W / N
  const label = span >= 3600_000 ? `há ${Math.round(span / 3600_000)}h` : `há ${Math.round(span / 60_000)}m`
  return (
    <div className="spark-wrap">
    <svg className="spark" viewBox={`0 0 ${W} 44`} preserveAspectRatio="none" role="img" aria-label="Warnings ao longo do tempo" data-testid="warn-spark">
      <line x1="0" x2={W} y1="40.5" y2="40.5" vectorEffect="non-scaling-stroke" stroke="var(--line)" />
      {buckets.map((c, i) => c > 0 && (
        <rect key={i} x={i * bw + 1} width={bw - 2} y={40 - (c / max) * 36} height={(c / max) * 36} rx="1.5" fill="var(--warn)" opacity="0.85"><title>{`${c} warning(s)`}</title></rect>
      ))}
    </svg>
    <div className="spark-axis"><span>{label}</span><span>agora</span></div>
    </div>
  )
}
