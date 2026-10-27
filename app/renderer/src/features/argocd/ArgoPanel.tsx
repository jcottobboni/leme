import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'

export function openArgo(clusterId: string, opts?: OpenOptions) {
  openPanel(`argocd:${clusterId}`, 'argocd', 'Argo CD', { clusterId }, opts)
}

/** Argo CD Applications — M5, feature argocd. */
export function ArgoPanel(_: IDockviewPanelProps<{ clusterId: string }>) {
  return <div className="pad muted" data-testid="argocd-panel">Argo CD: em breve.</div>
}
