import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'
import './provider'

export function openTraffic(clusterId: string, opts?: OpenOptions) {
  openPanel(`traffic:${clusterId}`, 'traffic', 'Tráfego observado', { clusterId }, opts)
}

/** Observed traffic (Caretta / Hubble) — M5, feature traffic. */
export function TrafficPanel(_: IDockviewPanelProps<{ clusterId: string }>) {
  return <div className="pad muted" data-testid="traffic-panel">Tráfego observado: em breve.</div>
}
