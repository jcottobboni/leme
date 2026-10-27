import { useCallback, useState } from 'react'
import { HistoryList } from './HistoryList'

/** "Histórico" tab of the object panel: the recorded changes of this object (lazy chunk). */
export function HistoryTab({ clusterId, group, resource, namespace, name }: { clusterId: string; group: string; resource: string; namespace?: string; name: string }) {
  const [info, setInfo] = useState<{ n: number; days: number }>()
  const onCount = useCallback((n: number, days: number) => setInfo({ n, days }), [])
  return (
    <div className="hist-tab" data-testid="history-tab">
      <div className="toolbar hist-tab-bar">
        <span className="muted small">
          {info ? `${info.n} ${info.n === 1 ? 'mudança registrada' : 'mudanças registradas'} · guardadas neste computador por ${info.days} dias` : 'Histórico local de mudanças'}
        </span>
      </div>
      <HistoryList clusterId={clusterId} query={{ group, resource, namespace, name }} onCount={onCount} />
    </div>
  )
}
