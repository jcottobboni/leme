import { openOverview } from '../overview/OverviewPanel'
import { openTopology } from '../topology/Topology'
import { openMap } from '../map/MapPanel'
import { openTimeline } from '../timeline/Timeline'
import { openRbac, openCreate } from '../rbac/RbacPanel'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { openCharts } from '../helm/HelmReleases'
import { openSecurity } from '../security/SecurityPanel'
import { openNetpol } from '../netpol/NetpolPanel'

export function openHelm(clusterId: string, opts?: OpenOptions) {
  openPanel(`helm-releases:${clusterId}`, 'helmReleases', 'Helm releases', { clusterId }, opts)
}

/** Per-cluster screens shown in the navigator and in the Ctrl+K palette. */
export const TOOLS: { id: string; label: string; open: (clusterId: string, opts?: OpenOptions) => void }[] = [
  { id: 'overview', label: 'Visão geral', open: (id, o) => openOverview(id, o) },
  { id: 'topology', label: 'Topologia', open: (id, o) => openTopology(id, undefined, o) },
  { id: 'map', label: 'Mapa do cluster', open: (id, o) => openMap(id, o) },
  { id: 'timeline', label: 'Timeline de eventos', open: (id, o) => openTimeline(id, o) },
  { id: 'helm', label: 'Helm releases', open: (id, o) => openHelm(id, o) },
  { id: 'charts', label: 'Helm charts', open: (id, o) => openCharts(id, o) },
  { id: 'rbac', label: 'RBAC e permissões', open: (id, o) => openRbac(id, o) },
  { id: 'security', label: 'Segurança', open: (id, o) => openSecurity(id, o) },
  { id: 'netpol', label: 'NetworkPolicy', open: (id, o) => openNetpol(id, o) },
  { id: 'create', label: 'Novo recurso (YAML)', open: (id) => openCreate(id, 'blank', useApp.getState().namespace[id] || 'default') },
]

export function openTool(id: string, clusterId: string, opts?: OpenOptions) {
  TOOLS.find((t) => t.id === id)?.open(clusterId, opts)
}
