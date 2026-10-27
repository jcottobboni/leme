import type { IDockviewPanelProps } from 'dockview-react'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { ClusterGate, ErrorBanner, useAsync } from '../common'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { CircleCheck, Stethoscope } from 'lucide-react'
import { SEVERITIES, SEV_LABEL, analyzeApi } from './api'
import { FindingCard } from './Finding'
import './analyze.css'

export function openProblems(clusterId: string, opts?: OpenOptions) {
  openPanel(`problems:${clusterId}`, 'problems', 'Problemas', { clusterId }, opts)
}

/** Cluster/namespace problems found by the deterministic analyzer (M4, group G1). */
export function ProblemsPanel({ params }: IDockviewPanelProps<{ clusterId: string }>) {
  const { clusterId } = params
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const { data, error, loading, reload } = useAsync(() => analyzeApi.namespace(clusterId, ns), [clusterId, ns])
  const counts = (s: string) => data?.filter((f) => f.severity === s).length ?? 0
  return (
    <ClusterGate clusterId={clusterId}>
      <div className="object-panel" data-testid="problems">
        <PanelHeader icon={Stethoscope} title="Problemas" scope={<NamespaceSelect clusterId={clusterId} />} actions={[refreshAction(reload, loading, { disabled: loading })]}>
          {data && (
            <span className="an-counts">
              {SEVERITIES.filter(counts).map((s) => <span key={s} className={`sev sev-${s}`}>{counts(s)} {SEV_LABEL[s].toLowerCase()}</span>)}
            </span>
          )}
        </PanelHeader>
        <ErrorBanner error={error} />
        <div className="obj-body">
          {data && data.length === 0 && <div className="empty an-empty"><CircleCheck size={24} className="s-ok" /><span>Nenhum problema encontrado {ns ? <>em <b>{ns}</b></> : 'no cluster'}.</span></div>}
          <div className="an-list">
            {SEVERITIES.filter(counts).map((s) => (
              <section key={s} className="an-group">
                <div className="an-group-title"><span className={`sev sev-${s}`}>{SEV_LABEL[s]}</span><span className="muted">{counts(s)}</span></div>
                {data!.filter((f) => f.severity === s).map((f, i) => <FindingCard key={`${f.id}-${f.object.name}-${i}`} clusterId={clusterId} f={f} showObject />)}
              </section>
            ))}
          </div>
        </div>
      </div>
    </ClusterGate>
  )
}
