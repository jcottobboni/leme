import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'

export function openProblems(clusterId: string, opts?: OpenOptions) {
  openPanel(`problems:${clusterId}`, 'problems', 'Problemas', { clusterId }, opts)
}

/** Cluster/namespace problems found by the deterministic analyzer (M4, group G1). */
export function ProblemsPanel(_: IDockviewPanelProps<{ clusterId: string }>) {
  return <div className="pad muted">Problemas em construção.</div>
}
