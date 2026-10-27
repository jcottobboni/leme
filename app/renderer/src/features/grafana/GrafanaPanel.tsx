import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'

export function openGrafana(clusterId: string, opts?: OpenOptions) {
  openPanel(`grafana:${clusterId}`, 'grafana', 'Grafana e Prometheus', { clusterId }, opts)
}

/** Grafana / Prometheus UIs through a local port-forward — M5, feature grafana. */
export function GrafanaPanel(_: IDockviewPanelProps<{ clusterId: string }>) {
  return <div className="pad muted" data-testid="grafana-panel">Grafana e Prometheus: em breve.</div>
}
