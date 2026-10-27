import { ErrorBanner, useAsync } from '../common'
import { analyzeApi } from './api'
import { FindingCard } from './Finding'
import './analyze.css'

/** "Diagnóstico" tab of an object: findings of the deterministic analyzer (M4, group G1). */
export function DiagnosisTab({ clusterId, group, kind, namespace, name }: { clusterId: string; group: string; kind: string; namespace?: string; name: string }) {
  const { data, error, loading, reload } = useAsync(() => analyzeApi.object(clusterId, { group, kind, namespace, name }), [clusterId, group, kind, namespace, name])
  return (
    <div data-testid="diagnosis">
      <div className="toolbar">
        <button onClick={reload} disabled={loading}>Atualizar</button>
        {loading && <span className="spinner" />}
      </div>
      <ErrorBanner error={error} />
      {data && data.length === 0 && <div className="an-empty">Nenhum problema encontrado</div>}
      {data && data.length > 0 && (
        <div className="an-list">
          {data.map((f, i) => <FindingCard key={`${f.id}-${f.object.kind}-${f.object.name}-${i}`} clusterId={clusterId} f={f} showObject={f.object.name !== name || f.object.kind !== kind} />)}
        </div>
      )}
    </div>
  )
}
