import { useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { ArrowDown, ArrowLeftRight, ArrowRight, ArrowUp, Globe } from 'lucide-react'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { openObject } from '../../components/ObjectPanel'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { ClusterGate, ErrorBanner, PanelHeader, RefreshButton, useAsync } from '../common'
import { kindIcon } from '../shell/nav'
import { openTopology } from '../topology/Topology'
import type { TrafficEdge, TrafficEndpoint } from '../map-webgl/trafficEdges'
import { TRAFFIC_WINDOWS, endpointLabel, fmtRate, sourceLabel, trafficApi, type TrafficWindow } from './api'
import './provider'
import './traffic.css'

export function openTraffic(clusterId: string, opts?: OpenOptions) {
  openPanel(`traffic:${clusterId}`, 'traffic', 'Tráfego observado', { clusterId }, opts)
}

type SortKey = 'from' | 'to' | 'port' | 'rate'

const SORTS: Record<SortKey, (a: TrafficEdge, b: TrafficEdge) => number> = {
  from: (a, b) => endpointLabel(a.from).localeCompare(endpointLabel(b.from)),
  to: (a, b) => endpointLabel(a.to).localeCompare(endpointLabel(b.to)),
  port: (a, b) => (a.port ?? 0) - (b.port ?? 0),
  rate: (a, b) => a.rate - b.rate,
}

function EndpointLink({ clusterId, e }: { clusterId: string; e: TrafficEndpoint }) {
  const Icon = e.kind === 'External' ? Globe : kindIcon(e.kind)
  if (e.kind === 'External') {
    return <span className="trf-ep ext" title="Fora do cluster ou não resolvido"><Icon size={13} />{e.name}<span className="trf-tag">externo</span></span>
  }
  return (
    <button className="trf-ep link" title={`Abrir ${e.kind} ${endpointLabel(e)}`} onClick={() => openObject(clusterId, e.group ?? '', e.kind, e.name, e.namespace)}>
      <Icon size={13} />
      <span className="trf-ep-name">{e.name}</span>
      <span className="muted small">{e.kind}{e.namespace ? ` · ${e.namespace}` : ''}</span>
    </button>
  )
}

function Traffic({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const [win, setWin] = useState<TrafficWindow>('5m')
  const [rev, setRev] = useState(0)
  const [sort, setSort] = useState<{ key: SortKey; desc: boolean }>({ key: 'rate', desc: true })
  const { data, error, loading } = useAsync(() => trafficApi.snapshot(clusterId, ns || undefined, win), [clusterId, ns, win, rev], 30_000)

  const rows = useMemo(() => {
    const list = [...(data?.edges ?? [])]
    const cmp = SORTS[sort.key]
    list.sort((a, b) => (sort.desc ? -cmp(a, b) : cmp(a, b)))
    return list
  }, [data, sort])

  const head = (key: SortKey, label: string, cls = '') => {
    const on = sort.key === key
    const Arrow = sort.desc ? ArrowDown : ArrowUp
    return (
      <th className={cls} aria-sort={on ? (sort.desc ? 'descending' : 'ascending') : 'none'}>
        <button className="trf-sort" data-testid={`traffic-sort-${key}`} onClick={() => setSort((s) => ({ key, desc: s.key === key ? !s.desc : key === 'rate' }))}>
          {label}{on && <Arrow size={11} />}
        </button>
      </th>
    )
  }
  const showTopology = (namespace?: string) => {
    const target = namespace || ns
    if (target && ns && target !== ns) useApp.getState().setNamespace(clusterId, target)
    openTopology(clusterId, target || undefined, undefined, { traffic: true })
  }
  const notes = Object.entries(data?.errors ?? {})

  return (
    <div className="object-panel trf-panel" data-testid="traffic-panel">
      <PanelHeader icon={<ArrowLeftRight size={16} />} title="Tráfego observado">
        <NamespaceSelect clusterId={clusterId} />
        <select className="ns-select" value={win} onChange={(e) => setWin(e.target.value as TrafficWindow)} title="Janela de observação" data-testid="traffic-window">
          {TRAFFIC_WINDOWS.map((w) => <option key={w} value={w}>últimos {w}</option>)}
        </select>
        {data && (
          <span className={`pill ${data.available ? 'pill-ok' : 'pill-neutral'}`} data-testid="traffic-source">
            Fonte: {sourceLabel(data.source)}{data.available ? '' : ' (indisponível)'}
          </span>
        )}
        {data?.available && <span className="muted small">{rows.length} conex{rows.length === 1 ? 'ão' : 'ões'}</span>}
        <span className="spacer" />
        <button onClick={() => showTopology()} data-testid="traffic-open-topology" disabled={!ns} title={ns ? 'Abrir a Topologia com o tráfego' : 'Escolha um namespace para abrir a Topologia'}>
          <ArrowLeftRight size={14} /> Ver na Topologia
        </button>
        <RefreshButton onClick={() => setRev((r) => r + 1)} loading={loading} />
      </PanelHeader>
      <ErrorBanner error={error} />
      {notes.length > 0 && (
        <div className="banner warn trf-notes" data-testid="traffic-errors">
          {notes.map(([k, v]) => <span key={k}><b>{k}:</b> {v}</span>)}
        </div>
      )}
      <div className="obj-body">
        {data && !data.available && (
          <div className="empty trf-empty" data-testid="traffic-empty">
            <ArrowLeftRight size={24} />
            <span><b>Sem dados de tráfego observado.</b></span>
            <span className="trf-hint" data-testid="traffic-hint">{data.hint || 'A fonte de tráfego não respondeu.'}</span>
          </div>
        )}
        {data?.available && !rows.length && (
          <div className="empty" data-testid="traffic-none">
            <ArrowLeftRight size={22} />
            <span>Nenhuma conexão observada {ns ? <>em <b>{ns}</b> </> : ''}nos últimos {data.window ?? win}.</span>
          </div>
        )}
        {data?.available && rows.length > 0 && (
          <table className="table trf-table" data-testid="traffic-table">
            <thead>
              <tr>{head('from', 'Origem')}<th aria-hidden />{head('to', 'Destino')}{head('port', 'Porta', 'num')}{head('rate', 'Volume', 'num')}<th /></tr>
            </thead>
            <tbody>
              {rows.map((e, i) => (
                <tr key={i} data-testid={`traffic-row-${e.from.name}-${e.to.name}`}>
                  <td><EndpointLink clusterId={clusterId} e={e.from} /></td>
                  <td className="trf-arrow"><ArrowRight size={13} /></td>
                  <td><EndpointLink clusterId={clusterId} e={e.to} /></td>
                  <td className="num">{e.port ? `${e.port}${e.protocol ? '/' + e.protocol : ''}` : e.protocol || '—'}</td>
                  <td className="num trf-rate">{fmtRate(e.rate, e.unit)}</td>
                  <td className="num">
                    <button className="trf-mini" onClick={() => showTopology(e.from.namespace || e.to.namespace)} title="Ver na Topologia">Topologia</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}

/** Observed traffic (Caretta / Hubble metrics in the configured Prometheus) — M5, feature traffic. */
export function TrafficPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Traffic clusterId={params.clusterId} />
    </ClusterGate>
  )
}
