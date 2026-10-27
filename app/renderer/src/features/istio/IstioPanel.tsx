import type { IDockviewPanelProps } from 'dockview-react'
import { openPanel, type OpenOptions } from '../../store'

export function openIstio(clusterId: string, opts?: OpenOptions) {
  openPanel(`istio:${clusterId}`, 'istio', 'Istio', { clusterId }, opts)
}

/** Istio mTLS, routes and analyzer — M5, feature istio. */
export function IstioPanel(_: IDockviewPanelProps<{ clusterId: string }>) {
  return <div className="pad muted" data-testid="istio-panel">Istio: em breve.</div>
}
