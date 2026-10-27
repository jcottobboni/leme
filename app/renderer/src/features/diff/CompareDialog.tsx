import { useState } from 'react'
import { Modal } from '../../components/Dialogs'
import { clusterName, findResource, openPanel, useApp } from '../../store'
import { useWatch } from '../../useWatch'
import type { DiffSide } from '../../api'
import './diff.css'

export interface DiffParams {
  clusterId: string // the left side's cluster (the tab's cluster)
  kind: string
  left: DiffSide
  right: DiffSide
}

export function openDiff(p: DiffParams) {
  const side = (s: DiffSide) => `${s.cluster}:${s.namespace ?? ''}/${s.name}`
  openPanel(`diff:${p.left.group}/${p.left.resource}:${side(p.left)}~${side(p.right)}`, 'diff', `diff ${p.left.name}`, { ...p })
}

/** "Comparar com…": another connected cluster and/or namespace, the same name by default. */
export function CompareDialog({ clusterId, group, resource, kind, namespace, name, onClose }: {
  clusterId: string; group: string; resource: string; kind: string; namespace?: string; name: string; onClose: () => void
}) {
  const connected = useApp((s) => s.connected)
  const clusters = Object.keys(connected)
  const [target, setTarget] = useState(clusterId)
  const [ns, setNs] = useState(namespace ?? '')
  const [other, setOther] = useState(name)
  const hasType = !!findResource(target, group, resource)
  const { rows } = useWatch(target, '', 'namespaces', '', !!namespace && !!findResource(target, '', 'namespaces'))
  const same = target === clusterId && (ns || '') === (namespace ?? '') && other === name
  const ok = hasType && !!other.trim() && (!namespace || !!ns.trim()) && !same

  const go = () => {
    if (!ok) return
    openDiff({
      clusterId, kind,
      left: { cluster: clusterId, group, resource, namespace, name },
      right: { cluster: target, group, resource, namespace: namespace ? ns.trim() : undefined, name: other.trim() },
    })
    onClose()
  }

  return (
    <Modal title={`Comparar ${kind} ${name}`} onClose={onClose} width={480} testId="compare-dialog">
      <div className="cmp-form">
        <label className="field">
          Cluster
          <select value={target} onChange={(e) => setTarget(e.target.value)} data-testid="compare-cluster">
            {clusters.map((c) => <option key={c} value={c}>{clusterName(c)}{c === clusterId ? ' (este)' : ''}</option>)}
          </select>
        </label>
        {namespace !== undefined && (
          <label className="field">
            Namespace
            <input className="input" list="compare-ns" value={ns} onChange={(e) => setNs(e.target.value)} data-testid="compare-namespace" autoFocus />
            <datalist id="compare-ns">{rows.map((r) => <option key={r.uid} value={r.name} />)}</datalist>
          </label>
        )}
        <label className="field">
          Nome
          <input className="input" value={other} onChange={(e) => setOther(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go()} data-testid="compare-name" />
        </label>
        {!hasType && <div className="banner warn">{kind} não existe no cluster {clusterName(target)}.</div>}
        {same && <p className="muted small">Escolha outro cluster, namespace ou nome.</p>}
        <p className="muted small">Só leitura. O YAML é normalizado no engine (sem managedFields, resourceVersion, uid, status e anotações de controle) e valores de Secrets ficam ocultos nos dois lados.</p>
      </div>
      <div className="modal-actions">
        <button onClick={onClose}>Cancelar</button>
        <button className="primary" disabled={!ok} onClick={go} data-testid="compare-go">Comparar</button>
      </div>
    </Modal>
  )
}
