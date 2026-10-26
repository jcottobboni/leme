import { useState } from 'react'
import { api } from '../../api'
import { useApp } from '../../store'
import { age } from '../../format'
import { ErrorBanner, useAsync } from '../common'
import { Review, type ReviewState } from '../helm/Review'

/** Rollout history of a Deployment with rollback to a previous revision (dry-run diff first). */
export function Revisions({ clusterId, namespace, name, readOnly }: { clusterId: string; namespace: string; name: string; readOnly: boolean }) {
  const toast = useApp((s) => s.toast)
  const { data, error, reload } = useAsync(() => api.rolloutHistory(clusterId, namespace, name), [clusterId, namespace, name], 10000)
  const [review, setReview] = useState<ReviewState>()

  const preview = async (revision: number) => {
    try {
      const r = await api.rolloutUndo(clusterId, namespace, name, revision, true)
      setReview({
        title: `Rollback de ${name} para a revisão ${revision}`,
        subtitle: 'pod template atual × revisão escolhida',
        previous: r.current,
        next: r.proposed,
        confirmLabel: 'Fazer rollback',
        onConfirm: async () => {
          await api.rolloutUndo(clusterId, namespace, name, revision, false).then(() => {
            toast('ok', `Rollback de ${name} para a revisão ${revision} disparado`)
            reload()
          }, (e) => toast('error', `Rollback falhou: ${e.message}`))
        },
      })
    } catch (e) {
      toast('error', (e as Error).message)
    }
  }

  if (review) return <Review r={review} onClose={() => setReview(undefined)} />
  return (
    <div className="pad-panel">
      <ErrorBanner error={error} />
      <table className="table" data-testid="revisions">
        <thead><tr><th>Revisão</th><th>ReplicaSet</th><th>Imagens</th><th>Pods</th><th>Criada</th><th>Causa</th><th /></tr></thead>
        <tbody>
          {data?.map((r) => (
            <tr key={r.revision} data-testid={`rev-${r.revision}`}>
              <td>{r.revision}{r.current && <span className="badge badge-ro"> atual</span>}</td>
              <td>{r.replicaSet}</td>
              <td className="mono small">{r.images.join(', ')}</td>
              <td>{r.replicas}</td>
              <td>{age(r.created)}</td>
              <td className="wrap small">{r.changeCause}</td>
              <td>{!r.current && <button disabled={readOnly} title={readOnly ? 'Cluster em somente leitura' : ''} onClick={() => preview(r.revision)}>Rollback</button>}</td>
            </tr>
          ))}
          {data && !data.length && <tr><td colSpan={7} className="muted">Sem revisões.</td></tr>}
        </tbody>
      </table>
    </div>
  )
}
