import { useState } from 'react'
import { Copy, Filter, PanelRightOpen, PanelTopOpen, RotateCw, Scaling, ScrollText, SquareTerminal, Trash2 } from 'lucide-react'
import { api, type Ref, type Resource, type Row } from '../../api'
import { openPanel, useApp } from '../../store'
import { ConfirmDialog, PromptDialog } from '../../components/Dialogs'
import type { MenuItem } from '../shell/menu'

export const SCALABLE = ['Deployment', 'StatefulSet', 'ReplicaSet', 'DeploymentConfig', 'ReplicationController']
export const RESTARTABLE = ['Deployment', 'StatefulSet', 'DaemonSet']

export function refOf(res: Resource, row: Row): Ref {
  return { group: res.group, version: res.version, resource: res.resource, namespace: row.namespace, name: row.name }
}

function desiredReplicas(row: Row) {
  const d = row.cells.desired
  if (typeof d === 'number') return d
  const m = /\/(\d+)$/.exec(String(row.cells.ready ?? ''))
  return m ? Number(m[1]) : 1
}

type Pending = { kind: 'delete' | 'restart' | 'scale'; rows: Row[] }

/** Row actions of a grid (context menu, kebab, bulk bar) and the dialogs they open. */
export function useRowActions(clusterId: string, res: Resource | undefined, open: { drawer: (r: Row) => void; tab: (r: Row) => void }) {
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const toast = useApp((s) => s.toast)
  const [pending, setPending] = useState<Pending>()

  const menuFor = (row: Row): MenuItem[] => {
    if (!res) return []
    const kind = res.kind
    const items: MenuItem[] = [
      { label: 'Ver detalhes', icon: PanelRightOpen, hint: 'Enter', onClick: () => open.drawer(row) },
      { label: 'Abrir em aba', icon: PanelTopOpen, hint: 'duplo clique', onClick: () => open.tab(row) },
    ]
    if (kind === 'Pod') {
      items.push(
        { separator: true },
        { label: 'Logs', icon: ScrollText, onClick: () => openPanel(`logs:${clusterId}:${row.namespace}/${row.name}`, 'logs', `logs ${row.name}`, { clusterId, namespace: row.namespace, pod: row.name }) },
        { label: 'Shell', icon: SquareTerminal, disabled: readOnly, onClick: () => openPanel(`term:${clusterId}:${row.namespace}/${row.name}:${Date.now()}`, 'terminal', `sh ${row.name}`, { clusterId, namespace: row.namespace, pod: row.name }) },
      )
    }
    if (SCALABLE.includes(kind) || RESTARTABLE.includes(kind)) items.push({ separator: true })
    if (SCALABLE.includes(kind)) items.push({ label: 'Scale…', icon: Scaling, disabled: readOnly, onClick: () => setPending({ kind: 'scale', rows: [row] }) })
    if (RESTARTABLE.includes(kind)) items.push({ label: 'Restart…', icon: RotateCw, disabled: readOnly, onClick: () => setPending({ kind: 'restart', rows: [row] }) })
    items.push(
      { separator: true },
      { label: 'Copiar nome', icon: Copy, onClick: () => navigator.clipboard.writeText(row.name).then(() => toast('ok', 'Nome copiado')) },
    )
    if (row.namespace) items.push({ label: `Filtrar namespace ${row.namespace}`, icon: Filter, onClick: () => useApp.getState().setNamespace(clusterId, row.namespace!) })
    items.push({ separator: true }, { label: 'Excluir…', icon: Trash2, danger: true, disabled: readOnly, onClick: () => setPending({ kind: 'delete', rows: [row] }) })
    return items
  }

  const run = async (label: string, rows: Row[], fn: (r: Row) => Promise<unknown>) => {
    setPending(undefined)
    let ok = 0
    const failures: string[] = []
    for (const r of rows) {
      try {
        await fn(r)
        ok++
      } catch (e) {
        failures.push(`${r.name}: ${(e as Error).message}`)
      }
    }
    if (ok) toast('ok', rows.length === 1 ? `${label}: ${rows[0].name}` : `${label}: ${ok} de ${rows.length}`)
    if (failures.length) toast('error', failures.slice(0, 3).join(' · ') + (failures.length > 3 ? ` (+${failures.length - 3})` : ''))
  }

  const names = (rows: Row[]) => (
    <ul className="small">
      {rows.slice(0, 8).map((r) => <li key={r.uid}><b>{r.namespace ? `${r.namespace}/` : ''}{r.name}</b></li>)}
      {rows.length > 8 && <li className="muted">… e mais {rows.length - 8}</li>}
    </ul>
  )

  const dialogs = res && pending && (
    <>
      {pending.kind === 'delete' && (
        <ConfirmDialog
          title={pending.rows.length === 1 ? `Excluir ${res.kind}` : `Excluir ${pending.rows.length} ${res.kind}s`}
          danger
          confirmLabel="Excluir"
          body={<><p>Esta ação não pode ser desfeita.</p>{names(pending.rows)}</>}
          onCancel={() => setPending(undefined)}
          onConfirm={() => run('Excluído', pending.rows, (r) => api.remove(clusterId, refOf(res, r)))}
        />
      )}
      {pending.kind === 'restart' && (
        <ConfirmDialog title="Rollout restart" confirmLabel="Reiniciar" body={<><p>Reiniciar os pods de:</p>{names(pending.rows)}</>}
          onCancel={() => setPending(undefined)} onConfirm={() => run('Restart disparado', pending.rows, (r) => api.restart(clusterId, refOf(res, r)))} />
      )}
      {pending.kind === 'scale' && (
        <PromptDialog title={`Scale ${pending.rows[0].name}`} label="Réplicas" type="number" initial={String(desiredReplicas(pending.rows[0]))} onCancel={() => setPending(undefined)}
          onConfirm={(v) => {
            const n = Number(v)
            if (!Number.isInteger(n) || n < 0) return toast('error', 'Número de réplicas inválido')
            run(`Escalado para ${n}`, pending.rows, (r) => api.scale(clusterId, refOf(res, r), n))
          }} />
      )}
    </>
  )

  return { menuFor, dialogs, readOnly, ask: (kind: Pending['kind'], rows: Row[]) => setPending({ kind, rows }) }
}
