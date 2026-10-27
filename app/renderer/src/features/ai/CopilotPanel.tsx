import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'

export interface CopilotRef {
  group: string
  kind: string
  namespace?: string
  name: string
}

export function openCopilot(clusterId: string, ref?: CopilotRef, opts?: OpenOptions) {
  openPanel(`copilot:${clusterId}`, 'copilot', 'Copilot', { clusterId, ref }, opts)
}

/** AI Copilot of a cluster (M4, group G3). */
export function CopilotPanel(_: IDockviewPanelProps<{ clusterId: string; ref?: CopilotRef }>) {
  return <div className="pad muted">Copilot em construção.</div>
}
