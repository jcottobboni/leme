import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api3, type Finding } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { openObject } from '../../components/ObjectPanel'
import { ClusterGate, ErrorBanner, Partial, Tabs, useAsync } from '../common'
import { PanelHeader, RefreshBtn } from '../../components/PanelHeader'
import { pressable } from '../../components/a11y'
import { ShieldCheck } from 'lucide-react'
import { SEVERITIES } from './common'
import { Counts, FindingsTable, ScoreRing, Sev } from './common'
import { openNetpol } from '../netpol/NetpolPanel'

type Tab = 'summary' | 'findings' | 'rbac' | 'scanners'

export function openSecurity(clusterId: string, opts?: OpenOptions) {
  openPanel(`security:${clusterId}`, 'security', 'Segurança', { clusterId }, opts)
}

/** Most severe first: critical count, then high, medium, low; then the lowest score. */
function bySeverity(a: { score: number; counts: Record<string, number> }, b: { score: number; counts: Record<string, number> }) {
  for (const s of SEVERITIES) {
    const d = (b.counts[s] ?? 0) - (a.counts[s] ?? 0)
    if (d) return d
  }
  return a.score - b.score
}

function Summary({ clusterId, ns }: { clusterId: string; ns: string }) {
  const { data: r, error, loading, reload } = useAsync(() => api3.securityReport(clusterId, ns), [clusterId, ns])
  if (error) return <ErrorBanner error={error} />
  if (!r) return <div className="pad"><span className="spinner" /></div>
  const cats = Object.entries(r.byCategory).sort((a, b) => b[1] - a[1])
  return (
    <div className="overview">
      <Partial errors={r.errors} />
      <div className="sec-head">
        <ScoreRing score={r.score} />
        <div>
          <div className="sec-title">Postura de segurança {ns ? <>de <b>{ns}</b></> : 'do cluster'}</div>
          <Counts counts={r.counts} />
          <div className="muted small" title="Cada workload começa com 100 e perde pontos por achado (crítico pesa mais que alto, médio e baixo). A nota geral é a média dos workloads, menos 5 por namespace sem NetworkPolicy, entre 0 e 100.">
            {r.workloads.length} workloads avaliados pelas regras do Leme. <span className="help-u">Como a nota é calculada?</span>
          </div>
        </div>
        <span className="spacer" />
        {loading && <span className="spinner" />}
        <RefreshBtn onClick={reload} loading={loading} />
      </div>
      {r.namespacesWithoutNetpol.length > 0 && (
        <section className="card">
          <h4>Namespaces sem NetworkPolicy</h4>
          <div className="chips">
            {r.namespacesWithoutNetpol.map((n) => <button key={n} className="chip clickable" title={`Analisar as NetworkPolicies de ${n}`} onClick={() => { useApp.getState().setNamespace(clusterId, n); openNetpol(clusterId) }}>{n}</button>)}
          </div>
          <p className="small muted">Pods desses namespaces aceitam conexões de qualquer pod do cluster.</p>
        </section>
      )}
      <div className="sec-grid">
        <section className="card">
          <h4>Workloads mais arriscados</h4>
          <table className="table" data-testid="risky-workloads">
            <thead><tr><th>Nota</th><th>Workload</th><th>Achados</th></tr></thead>
            <tbody>
              {[...r.workloads].sort(bySeverity).slice(0, 15).map((w) => (
                <tr key={w.kind + w.namespace + w.name} className="clickable" {...pressable(() => openObject(clusterId, w.group, w.kind, w.name, w.namespace), null)}>
                  <td><span className={`score-chip sc-${w.score >= 80 ? 'ok' : w.score >= 60 ? 'warn' : 'error'}`}>{w.score}</span></td>
                  <td>{w.kind} {w.namespace ? w.namespace + '/' : ''}<b>{w.name}</b></td>
                  <td><Counts counts={w.counts} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
        <section className="card">
          <h4>Por categoria</h4>
          <table className="table">
            <tbody>{cats.map(([c, n]) => <tr key={c}><td>{c}</td><td>{n}</td></tr>)}</tbody>
          </table>
        </section>
      </div>
      <section className="card">
        <h4>Achados críticos e altos</h4>
        <FindingsTable clusterId={clusterId} findings={r.findings.filter((f) => f.severity === 'critical' || f.severity === 'high')} />
      </section>
    </div>
  )
}

function AllFindings({ clusterId, ns }: { clusterId: string; ns: string }) {
  const { data: r, error } = useAsync(() => api3.securityReport(clusterId, ns), [clusterId, ns])
  if (error) return <ErrorBanner error={error} />
  if (!r) return <div className="pad"><span className="spinner" /></div>
  return <FindingsTable clusterId={clusterId} findings={r.findings} />
}

function RBACRisks({ clusterId }: { clusterId: string }) {
  const [refresh, setRefresh] = useState(0)
  const { data, error, loading } = useAsync(() => api3.securityRBAC(clusterId, refresh > 0), [clusterId, refresh])
  const [system, setSystem] = useState(false)
  const risks = (data?.risks ?? []).filter((r) => system || !r.system).sort((a, b) => SEVERITIES.indexOf(a.severity as never) - SEVERITIES.indexOf(b.severity as never))
  return (
    <div className="pad-panel">
      <div className="toolbar">
        <label className="check"><input type="checkbox" checked={system} onChange={(e) => setSystem(e.target.checked)} /> incluir bindings do sistema (system:, kubeadm:, openshift-*)</label>
        <span className="muted">{risks.length} {risks.length === 1 ? 'risco' : 'riscos'}</span>
        <span className="spacer" />
        <RefreshBtn onClick={() => setRefresh((r) => r + 1)} loading={loading} />
      </div>
      <ErrorBanner error={error} />
      <Partial errors={data?.errors} />
      <table className="table" data-testid="rbac-risks">
        <thead><tr><th>Severidade</th><th>Risco</th><th>Subject</th><th>Via</th></tr></thead>
        <tbody>
          {risks.map((r, i) => (
            <tr key={i} title={r.detail}>
              <td><Sev s={r.severity} /></td>
              <td className="wrap">{r.title}<div className="muted small">{r.detail}</div></td>
              <td><span className="chip">{r.subjectKind}</span> {r.subjectNamespace ? r.subjectNamespace + '/' : ''}{r.subjectName}</td>
              <td>
                <button className="link" onClick={() => openObject(clusterId, 'rbac.authorization.k8s.io', r.bindingKind, r.bindingName, r.bindingNamespace)}>{r.bindingKind}/{r.bindingName}</button>
                {' → '}{r.roleKind}/{r.roleName}
              </td>
            </tr>
          ))}
          {data && !risks.length && <tr><td colSpan={4} className="muted">Nenhum risco encontrado entre os bindings visíveis.</td></tr>}
        </tbody>
      </table>
    </div>
  )
}

function Scanners({ clusterId, ns }: { clusterId: string; ns: string }) {
  const { data: tools } = useAsync(() => api3.securityTools(), [])
  const [busy, setBusy] = useState<string>()
  const [result, setResult] = useState<{ label: string; findings: Finding[] }>()
  const [err, setErr] = useState<string>()
  const run = async (tool: 'polaris' | 'kubescape', framework?: string) => {
    const label = tool === 'polaris' ? 'Polaris' : `Kubescape ${framework?.toUpperCase()}`
    setBusy(label)
    setErr(undefined)
    try {
      const r = await api3.securityScan(clusterId, tool, ns, framework)
      setResult({ label, findings: r.findings })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(undefined)
    }
  }
  const has = (n: string) => tools?.find((t) => t.name === n)?.installed
  return (
    <div className="pad-panel">
      <p className="small muted">
        Os scanners rodam <b>na sua máquina</b>, sobre o YAML exportado de {ns ? <b>{ns}</b> : 'todo o cluster'} (sem Secrets nem ConfigMaps, e com valores de variáveis de ambiente mascarados). Eles não acessam o cluster e não enviam resultados para fora.
      </p>
      <div className="row">
        {tools?.map((t) => (
          <span key={t.name} className={`chip ${t.installed ? '' : 'missing'}`} title={t.path ?? 'não encontrado no PATH'}>{t.name}: {t.installed ? 'instalado' : 'não instalado'} · {t.purpose}</span>
        ))}
      </div>
      <div className="row" style={{ margin: '10px 0' }}>
        <button disabled={!has('polaris') || !!busy} onClick={() => run('polaris')} data-testid="run-polaris">Rodar Polaris</button>
        <button disabled={!has('kubescape') || !!busy} onClick={() => run('kubescape', 'nsa')} data-testid="run-kubescape">Kubescape NSA</button>
        <button disabled={!has('kubescape') || !!busy} onClick={() => run('kubescape', 'mitre')}>Kubescape MITRE</button>
        {busy && <span className="muted"><span className="spinner" /> {busy} em execução…</span>}
      </div>
      <ErrorBanner error={err} />
      {result && (
        <>
          <h4>{result.label}: {result.findings.length} achados</h4>
          <FindingsTable clusterId={clusterId} findings={result.findings} />
        </>
      )}
    </div>
  )
}

export function SecurityPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  const [tab, setTab] = useState<Tab>('summary')
  const ns = useApp((s) => s.namespace[params.clusterId] ?? '')
  return (
    <ClusterGate clusterId={params.clusterId}>
      <div className="object-panel">
        <PanelHeader icon={ShieldCheck} title="Relatório de segurança" scope={<NamespaceSelect clusterId={params.clusterId} />} />
        <Tabs<Tab> tabs={[['summary', 'Resumo'], ['findings', 'Todos os achados'], ['rbac', 'Riscos de RBAC'], ['scanners', 'Scanners externos']]} value={tab} onChange={setTab} />
        <div className="obj-body">
          {tab === 'summary' && <Summary clusterId={params.clusterId} ns={ns} />}
          {tab === 'findings' && <AllFindings clusterId={params.clusterId} ns={ns} />}
          {tab === 'rbac' && <RBACRisks clusterId={params.clusterId} />}
          {tab === 'scanners' && <Scanners clusterId={params.clusterId} ns={ns} />}
        </div>
      </div>
    </ClusterGate>
  )
}
