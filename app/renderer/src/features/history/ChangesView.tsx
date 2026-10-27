import { useCallback, useState } from 'react'
import { History } from 'lucide-react'
import { useApp } from '../../store'
import { NamespaceSelect } from '../../components/ResourceGrid'
import { PanelHeader, type HeaderAction } from '../../components/PanelHeader'
import { HistoryList } from './HistoryList'

/** "Mudanças" view of the Timeline: the persistent change history of the selected namespace (or the cluster). */
export function ChangesView({ clusterId, modeAction }: { clusterId: string; modeAction: HeaderAction }) {
  const ns = useApp((s) => s.namespace[clusterId] ?? '')
  const [type, setType] = useState('')
  const [text, setText] = useState('')
  const [info, setInfo] = useState<{ n: number; days: number }>()
  const onCount = useCallback((n: number, days: number) => setInfo({ n, days }), [])
  return (
    <div className="object-panel changes-panel" data-testid="changes-view">
      <PanelHeader icon={History} title="Mudanças" scope={<NamespaceSelect clusterId={clusterId} />} actions={[modeAction]}>
        <select value={type} onChange={(e) => setType(e.target.value)} aria-label="Tipo de mudança" data-testid="changes-type">
          <option value="">Todas</option>
          <option value="created">Criações</option>
          <option value="updated">Alterações</option>
          <option value="deleted">Exclusões</option>
        </select>
        <input className="search" placeholder="Filtrar tipo, nome, campo…" aria-label="Filtrar mudanças" value={text} onChange={(e) => setText(e.target.value)} data-testid="changes-filter" />
        {info && <span className="muted small ph-info">{info.n} registros · retenção {info.days} dias</span>}
      </PanelHeader>
      <HistoryList clusterId={clusterId} query={{ namespace: ns || undefined, type: type || undefined }} showObject text={text} onCount={onCount} />
    </div>
  )
}
