import type { IDockviewPanelProps } from 'dockview-react'
import { api } from '../../api'
import { clusterName, findByKind, openPanel, useApp, type OpenOptions } from '../../store'
import { absDate, age, bytes, cpu } from '../../format'
import { openObject } from '../../components/ObjectPanel'
import { openGrid } from '../shell/Sidebar'
import { Bar, ClusterGate, ErrorBanner, Partial, pct, useAsync } from '../common'
import { Activity, Box, Boxes, Cpu, Gauge as Gauge2, HardDrive, LayoutDashboard, TriangleAlert } from 'lucide-react'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { pressable } from '../../components/a11y'
import { kindIcon } from '../shell/nav'
import { Donut, Gauge } from './Donut'
import { CommitCards, Headroom, NamespacesTable, PHASES, PhaseLegend, TopConsumers, WarnSpark } from './Widgets'
import '../metrics/charts.css'
import { MetricsView } from '../metrics/Metrics'

export function openOverview(clusterId: string, opts?: OpenOptions) {
  openPanel(`overview:${clusterId}`, 'overview', 'Visão geral', { clusterId }, opts)
}

const fmtCpu = (m: number) => cpu(m)
const fmtMem = (b: number) => bytes(b)

const HEALTH_COLORS = { ok: 'var(--ok)', warn: 'var(--warn)', error: 'var(--error)', neutral: 'var(--neutral)' }
const WORKLOAD_LABEL: Record<string, string> = { Deployment: 'Deployments', StatefulSet: 'StatefulSets', DaemonSet: 'DaemonSets', ReplicaSet: 'ReplicaSets', Job: 'Jobs', CronJob: 'CronJobs', DeploymentConfig: 'DeploymentConfigs' }

/** Donut only when there is more than one state; a single state reads better as a bar and a sentence. */
function HealthBody({ slices, extra }: { slices: { label: string; value: number; color: string }[]; extra?: string }) {
  if (slices.length > 1) {
    return (
      <div className="ov-card-body">
        <Donut slices={slices} />
        <Legend items={slices} />
      </div>
    )
  }
  const s = slices[0]
  return (
    <div className="ov-single">
      <div className="healthbar"><i style={{ width: '100%', background: s?.color ?? 'var(--neutral)' }} /></div>
      <span className="muted small">{s ? <><b className="s-strong">{s.value}</b> {s.label}</> : 'nenhum'}{extra ? ` · ${extra}` : ''}</span>
    </div>
  )
}

function Legend({ items }: { items: { label: string; value: number; color: string }[] }) {
  return (
    <div className="legend">
      {items.map((i) => <span key={i.label}><i style={{ background: i.color }} /><b>{i.value}</b> {i.label}</span>)}
    </div>
  )
}

function CapacityCard({ title, use, req, lim, alloc, fmt }: { title: string; use?: number; req: number; lim: number; alloc: number; fmt: (n: number) => string }) {
  return (
    <div className="ov-card gauge-card" data-testid={`gauge-${title}`}>
      <Gauge value={use} max={alloc} label={title} />
      <div className="gauge-lines">
        <div><span className="muted">Uso</span><b>{use !== undefined ? fmt(use) : 'sem metrics-server'}</b></div>
        <div><span className="muted">Requests</span><b>{fmt(req)} · {pct(req, alloc)}</b></div>
        <Bar value={req} max={alloc} />
        <div><span className="muted">Limits</span><b>{fmt(lim)} · {pct(lim, alloc)}</b></div>
        <Bar value={lim} max={alloc} />
        <div><span className="muted">Alocável</span><b>{fmt(alloc)}</b></div>
      </div>
    </div>
  )
}

function Overview({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const version = useApp((s) => s.connected[clusterId]?.version)
  const { data: o, error, loading, reload } = useAsync(() => api.overview(clusterId, ns), [clusterId, ns], 30000)
  if (error) return <ErrorBanner error={error} />
  if (!o) {
    return (
      <div className="ov-skel" data-testid="ov-loading">
        <div className="skeleton" /><div className="skeleton" style={{ height: 160 }} /><div className="skeleton" style={{ height: 220 }} />
      </div>
    )
  }
  const stats = o.namespaceStats ?? []
  const pickNs = (n: string) => useApp.getState().setNamespace(clusterId, n)
  const totalPods = Object.values(o.pods).reduce((a, b) => a + b, 0)
  const podSlices = PHASES.map(([k, color]) => ({ label: k, value: o.pods[k] ?? 0, color })).filter((x) => x.value > 0)
  const readyNodes = o.nodes.filter((n) => n.health === 'ok').length
  const nodeSlices = [
    { label: readyNodes === 1 ? 'pronto' : 'prontos', value: readyNodes, color: HEALTH_COLORS.ok },
    { label: 'sem condição Ready', value: o.nodes.length - readyNodes, color: HEALTH_COLORS.error },
  ].filter((x) => x.value > 0)
  // Built-in kinds first: a CRD named like a built-in (kind Node) must not open in its place.
  const openKind = (k: string) => {
    const res = findByKind(clusterId, k)
    if (res) openGrid(clusterId, res)
  }
  return (
    <div className="overview-panel">
      <PanelHeader icon={LayoutDashboard} title="Visão geral" actions={[refreshAction(reload, loading)]}
        scope={<span className="ph-scope"><b className="s-strong">{clusterName(clusterId)}</b> {version}</span>}>
        {o.scope !== 'cluster' && <span className="badge badge-ro">escopo: {o.scope}</span>}
      </PanelHeader>
      <Partial errors={o.errors} />
      <div className="overview">
        <div className="ov-grid">
          <div className="ov-card clickable" {...pressable(() => openKind('Node'))} aria-label={`Nodes: ${o.nodes.length}`} data-testid="ov-Node">
            <div className="ov-card-head"><Cpu size={16} /> Nodes <span className="num">{o.nodes.length}</span></div>
            <HealthBody slices={nodeSlices} extra={`${o.namespaces} namespaces`} />
          </div>
          <div className="ov-card clickable" {...pressable(() => openKind('Pod'))} aria-label={`Pods: ${totalPods}`} data-testid="ov-pods">
            <div className="ov-card-head"><Box size={16} /> Pods <span className="num">{totalPods}</span></div>
            <HealthBody slices={podSlices} />
          </div>
          {Object.entries(o.workloads).filter(([, h]) => h.total > 0).map(([k, h]) => {
            const slices = [
              { label: h.ok === 1 ? 'saudável' : 'saudáveis', value: h.ok, color: HEALTH_COLORS.ok },
              { label: 'atenção', value: h.warn, color: HEALTH_COLORS.warn },
              { label: 'com erro', value: h.error, color: HEALTH_COLORS.error },
              { label: 'outros', value: Math.max(0, h.total - h.ok - h.warn - h.error), color: HEALTH_COLORS.neutral },
            ].filter((x) => x.value > 0)
            const Icon = kindIcon(k)
            return (
              <div className="ov-card clickable" key={k} {...pressable(() => openKind(k))} aria-label={`${WORKLOAD_LABEL[k] ?? k + 's'}: ${h.total}`} data-testid={`ov-${k}`}>
                <div className="ov-card-head"><Icon size={16} /> {WORKLOAD_LABEL[k] ?? k + 's'} <span className="num">{h.total}</span></div>
                <HealthBody slices={slices} />
              </div>
            )
          })}
        </div>
        <section className="card" data-testid="ov-headroom">
          <div className="ov-pressure">
            <Headroom testid="hb-cpu" title="Uso de CPU" icon={<Cpu size={16} />} use={o.usage?.cpu} req={o.requests.cpu} lim={o.limits.cpu} alloc={o.allocatable.cpu} fmt={fmtCpu} />
            <Headroom testid="hb-mem" title="Uso de memória" icon={<HardDrive size={16} />} use={o.usage?.memory} req={o.requests.memory} lim={o.limits.memory} alloc={o.allocatable.memory} fmt={fmtMem} />
          </div>
          <div className="ov-section-head" style={{ marginTop: 18 }}>
            <div><h4 style={{ margin: 0 }}>Compromisso do alocável</h4><span className="muted small">Requests comparados ao que o cluster pode agendar</span></div>
          </div>
          <CommitCards o={o} />
        </section>
        <div className="ov-two">
          <section className="card" data-testid="ov-ns-card">
            <div className="ov-section-head">
              <h4><Boxes size={14} /> Namespaces <span className="count-pill">{stats.length}</span></h4>
              <span className="spacer" />
              <PhaseLegend />
            </div>
            {stats.length ? <NamespacesTable clusterId={clusterId} stats={stats} active={ns} onPick={pickNs} /> : <div className="ov-empty"><Boxes size={20} />Nenhum namespace com pods.</div>}
          </section>
          <section className="card"><TopConsumers stats={stats} onPick={pickNs} /></section>
        </div>
        <section className="card">
          <h4><Gauge2 size={14} /> Capacidade {o.scope === 'cluster' ? 'do cluster' : `(pods de ${o.scope})`}</h4>
          <div className="gauges">
            <CapacityCard title="CPU" use={o.usage?.cpu} req={o.requests.cpu} lim={o.limits.cpu} alloc={o.allocatable.cpu} fmt={fmtCpu} />
            <CapacityCard title="Memória" use={o.usage?.memory} req={o.requests.memory} lim={o.limits.memory} alloc={o.allocatable.memory} fmt={fmtMem} />
            <div className="ov-card gauge-card">
              <Gauge value={o.requests.pods ?? 0} max={o.allocatable.pods ?? 0} label="Pods" />
              <div className="gauge-lines">
                <div><span className="muted">Agendados</span><b>{o.requests.pods ?? 0}</b></div>
                <div><span className="muted">Capacidade</span><b>{o.allocatable.pods ?? 0}</b></div>
              </div>
            </div>
          </div>
        </section>
        <section className="card">
          <h4><Cpu size={14} /> Nodes</h4>
          <table className="table" data-testid="ov-nodes">
            <thead><tr><th>Node</th><th>Status</th><th>Roles</th><th>Pods</th><th>CPU</th><th>Memória</th></tr></thead>
            <tbody>
              {o.nodes.map((n) => (
                <tr key={n.name} className="clickable" {...pressable(() => openObject(clusterId, '', 'Node', n.name), null)}>
                  <td><span className={`dot d-${n.health}`} />{n.name}</td>
                  <td><span className={`pill pill-${n.health}`}>{n.status}</span></td>
                  <td>{n.roles}</td>
                  <td>{n.pods}/{n.allocatable.pods}</td>
                  <td style={{ minWidth: 200 }}>
                    <Bar value={n.usage?.cpu ?? n.requests.cpu} max={n.allocatable.cpu} />
                    <span className="muted small">{n.usage ? `uso ${cpu(n.usage.cpu)} (${pct(n.usage.cpu, n.allocatable.cpu)})` : 'sem uso'} · req {pct(n.requests.cpu, n.allocatable.cpu)}</span>
                  </td>
                  <td style={{ minWidth: 200 }}>
                    <Bar value={n.usage?.memory ?? n.requests.memory} max={n.allocatable.memory} />
                    <span className="muted small">{n.usage ? `uso ${bytes(n.usage.memory)} (${pct(n.usage.memory, n.allocatable.memory)})` : 'sem uso'} · req {pct(n.requests.memory, n.allocatable.memory)}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
        <section className="card">
          <h4><Activity size={14} /> Histórico do cluster (Prometheus)</h4>
          <MetricsView clusterId={clusterId} scope={{ kind: 'cluster' }} />
        </section>
        <section className="card">
          <h4><TriangleAlert size={14} /> Warnings recentes <span className="count-pill">{o.warnings.length}</span></h4>
          <WarnSpark warnings={o.warnings} />
          <table className="table">
            <thead><tr><th>Objeto</th><th>Motivo</th><th>Mensagem</th><th>Qtd</th><th>Última</th></tr></thead>
            <tbody>
              {o.warnings.map((w, i) => (
                <tr key={i} className="clickable" {...pressable(() => openObject(clusterId, '', w.kind, w.name, w.namespace), null)}>
                  <td>{w.namespace ? `${w.namespace}/` : ''}{w.kind}/{w.name}</td>
                  <td><span className="pill pill-warn">{w.reason}</span></td>
                  <td className="wrap">{w.message}</td>
                  <td>{w.count}</td>
                  <td title={absDate(w.last)}>{age(w.last)}</td>
                </tr>
              ))}
              {!o.warnings.length && <tr><td colSpan={5}><div className="ov-empty"><TriangleAlert size={20} />Nenhum warning recente.</div></td></tr>}
            </tbody>
          </table>
        </section>
      </div>
    </div>
  )
}

export function OverviewPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Overview clusterId={params.clusterId} />
    </ClusterGate>
  )
}
