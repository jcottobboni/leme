import { useMemo, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { api } from '../../api'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { absDate, age } from '../../format'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { ClusterGate, ErrorBanner, useAsync } from '../common'
import { Package, Plus } from 'lucide-react'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { pressable } from '../../components/a11y'

export function helmStatusHealth(s: string) {
  if (s === 'deployed') return 'ok'
  if (s === 'failed') return 'error'
  if (s === 'superseded' || s === 'uninstalled') return 'neutral'
  return 'warn'
}

export function openRelease(clusterId: string, namespace: string, name: string, opts?: OpenOptions) {
  openPanel(`helm:${clusterId}:${namespace}/${name}`, 'helmRelease', name, { clusterId, namespace, name }, opts)
}

export function openCharts(clusterId?: string, opts?: OpenOptions) {
  openPanel('helm-charts', 'helmCharts', 'Helm charts', { clusterId }, opts)
}

function Releases({ clusterId }: { clusterId: string }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const { data, error, loading, reload } = useAsync(() => api.helmReleases(clusterId, ns), [clusterId, ns], 15000)
  const [q, setQ] = useState('')
  const rows = useMemo(() => (data ?? []).filter((r) => !q || `${r.name} ${r.namespace} ${r.chart}`.toLowerCase().includes(q.toLowerCase())), [data, q])
  return (
    <div className="grid-panel">
      <PanelHeader icon={Package} title="Releases do Helm" scope={<NamespaceSelect clusterId={clusterId} />} actions={[
        { key: 'install', label: 'Instalar chart…', icon: Plus, primary: true, onClick: () => openCharts(clusterId) },
        refreshAction(reload, loading),
      ]}>
        <input className="search" placeholder="Buscar release…" aria-label="Buscar release" value={q} onChange={(e) => setQ(e.target.value)} />
        <span className="muted">{rows.length} {rows.length === 1 ? 'release' : 'releases'}</span>
      </PanelHeader>
      <ErrorBanner error={error} />
      {!loading && !rows.length ? (
        <div className="empty">
          <Package size={22} />
          <span>Nenhum release de Helm {ns ? <>em <b>{ns}</b></> : 'no cluster'}.</span>
          <button className="primary" onClick={() => openCharts(clusterId)}>Instalar chart…</button>
        </div>
      ) : (
      <div className="scroll">
        <table className="table">
          <thead><tr><th>Release</th><th>Namespace</th><th>Revisão</th><th>Status</th><th>Chart</th><th>App</th><th>Atualizado</th></tr></thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.namespace + '/' + r.name} className="clickable" data-testid={`release-${r.name}`} {...pressable(() => openRelease(clusterId, r.namespace, r.name), null)}>
                <td><span className={`dot d-${helmStatusHealth(r.status)}`} />{r.name}</td>
                <td>{r.namespace}</td>
                <td>{r.revision}</td>
                <td><span className={`pill pill-${helmStatusHealth(r.status)}`}>{r.status}</span></td>
                <td>{r.chart}-{r.chartVersion}</td>
                <td>{r.appVersion}</td>
                <td title={absDate(r.updated)}>{age(r.updated)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      )}
    </div>
  )
}

export function HelmReleasesPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Releases clusterId={params.clusterId} />
    </ClusterGate>
  )
}
