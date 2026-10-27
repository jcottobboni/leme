import { openPanel, useApp, type OpenOptions } from '../../store'
import { openMultiLogs } from '../logs/open'

// Openers load their panel module on demand: importing them statically would pull every panel (and xyflow,
// uplot…) into the initial bundle through the sidebar.

export function openHelm(clusterId: string, opts?: OpenOptions) {
  openPanel(`helm-releases:${clusterId}`, 'helmReleases', 'Helm releases', { clusterId }, opts)
}

/** Per-cluster screens shown in the navigator and in the Ctrl+K palette. */
export const TOOLS: { id: string; label: string; open: (clusterId: string, opts?: OpenOptions) => void }[] = [
  { id: 'overview', label: 'Visão geral', open: (id, o) => void import('../overview/OverviewPanel').then((m) => m.openOverview(id, o)) },
  { id: 'problems', label: 'Problemas', open: (id, o) => void import('../analyze/ProblemsPanel').then((m) => m.openProblems(id, o)) },
  { id: 'copilot', label: 'Copilot (IA)', open: (id, o) => void import('../ai/CopilotPanel').then((m) => m.openCopilot(id, undefined, o)) },
  { id: 'topology', label: 'Topologia', open: (id, o) => void import('../topology/Topology').then((m) => m.openTopology(id, undefined, o)) },
  { id: 'projtopo', label: 'Topologia do projeto', open: (id, o) => void import('../projtopo/ProjectTopology').then((m) => m.openProjectTopology(id, o)) },
  { id: 'map', label: 'Mapa do cluster', open: (id, o) => void import('../map/MapPanel').then((m) => m.openMap(id, o)) },
  { id: 'timeline', label: 'Timeline de eventos', open: (id, o) => void import('../timeline/Timeline').then((m) => m.openTimeline(id, o)) },
  { id: 'changes', label: 'Mudanças (histórico)', open: (id, o) => void import('../timeline/Timeline').then((m) => m.openChanges(id, o)) },
  { id: 'multilogs', label: 'Logs por rótulo', open: (id) => openMultiLogs({ clusterId: id, namespace: useApp.getState().namespace[id] || 'default' }) },
  { id: 'helm', label: 'Helm releases', open: (id, o) => openHelm(id, o) },
  { id: 'charts', label: 'Helm charts', open: (id, o) => void import('../helm/HelmReleases').then((m) => m.openCharts(id, o)) },
  { id: 'rbac', label: 'RBAC e permissões', open: (id, o) => void import('../rbac/RbacPanel').then((m) => m.openRbac(id, o)) },
  { id: 'security', label: 'Segurança', open: (id, o) => void import('../security/SecurityPanel').then((m) => m.openSecurity(id, o)) },
  { id: 'netpol', label: 'Políticas de rede', open: (id, o) => void import('../netpol/NetpolPanel').then((m) => m.openNetpol(id, o)) },
  { id: 'traffic', label: 'Tráfego observado', open: (id, o) => void import('../traffic/TrafficPanel').then((m) => m.openTraffic(id, o)) },
  { id: 'argocd', label: 'Argo CD', open: (id, o) => void import('../argocd/ArgoPanel').then((m) => m.openArgo(id, o)) },
  { id: 'grafana', label: 'Grafana e Prometheus', open: (id, o) => void import('../grafana/GrafanaPanel').then((m) => m.openGrafana(id, o)) },
  { id: 'istio', label: 'Istio', open: (id, o) => void import('../istio/IstioPanel').then((m) => m.openIstio(id, o)) },
  { id: 'flux', label: 'Flux', open: (id, o) => void import('../flux/FluxPanel').then((m) => m.openFlux(id, o)) },
  { id: 'create', label: 'Novo recurso (YAML)', open: (id) => void import('../rbac/RbacPanel').then((m) => m.openCreate(id, 'blank', useApp.getState().namespace[id] || 'default')) },
]

export function openTool(id: string, clusterId: string, opts?: OpenOptions) {
  TOOLS.find((t) => t.id === id)?.open(clusterId, opts)
}
