import {
  Activity, AppWindow, ArrowLeftRight, ChartColumn, Blocks, GitPullRequestArrow, ArrowUpWideNarrow, Boxes, Box, Briefcase, Clock, Cloud, Cog, Copy, Cpu, Database, FileCode, FileKey, FileText, Folder, Gauge, GitBranch, Globe, HardDrive,
  Hammer, Image, Key, Layers, Layers2, LayoutDashboard, Link, Lock, Map as MapIcon, type LucideIcon, MoveHorizontal, Network, Package, Plus, Route, Ruler, ScrollText, Server, Settings2,
  Shield, ShieldCheck, Share2, Sparkles, Stethoscope, Terminal, Timer, Users, Waypoints, Webhook, Workflow, Wrench,
} from 'lucide-react'
import type { Resource } from '../../api'

/** English plural used for resource-type labels (Pods, Ingresses, NetworkPolicies, Endpoints). */
export function plural(kind: string) {
  if (kind === 'Event') return 'Events'
  if (/ss$/.test(kind)) return kind + 'es'
  if (/s$/.test(kind)) return kind
  if (/[^aeiou]y$/.test(kind)) return kind.slice(0, -1) + 'ies'
  return kind + 's'
}

const KIND_ICONS: Record<string, LucideIcon> = {
  Pod: Box, Deployment: Layers, StatefulSet: Database, DaemonSet: Server, ReplicaSet: Boxes, ReplicationController: Copy, Job: Briefcase,
  CronJob: Clock, DeploymentConfig: Layers2, BuildConfig: Wrench, Build: Hammer, ImageStream: Image, HorizontalPodAutoscaler: MoveHorizontal,
  PodDisruptionBudget: Shield, Service: Share2, Ingress: Globe, Route: Route, Endpoints: Link, EndpointSlice: Link, IngressClass: Globe,
  NetworkPolicy: Network, ConfigMap: FileText, Secret: FileKey, ResourceQuota: Gauge, LimitRange: Ruler, PersistentVolumeClaim: HardDrive,
  PersistentVolume: HardDrive, StorageClass: Database, ServiceAccount: Users, Role: Key, ClusterRole: Key, RoleBinding: Lock, ClusterRoleBinding: Lock,
  Node: Cpu, Namespace: Folder, Project: Folder, Event: Activity, CustomResourceDefinition: FileCode,
  Lease: Timer, PriorityClass: ArrowUpWideNarrow, RuntimeClass: Cog, MutatingWebhookConfiguration: Webhook, ValidatingWebhookConfiguration: Webhook,
  CSIDriver: HardDrive, CSINode: HardDrive, VolumeAttachment: Link,
}

export function kindIcon(kind?: string): LucideIcon {
  return (kind && KIND_ICONS[kind]) || FileCode
}

/** Icons of the dock components (tabs). Grid and object tabs use the kind icon. */
export const COMPONENT_ICONS: Record<string, LucideIcon> = {
  welcome: AppWindow, logs: ScrollText, terminal: Terminal, forwards: Waypoints, helmReleases: Package, helmRelease: Package, helmCharts: Package,
  rbac: Key, create: Plus, overview: LayoutDashboard, topology: Workflow, map: MapIcon, timeline: Timer, security: ShieldCheck, netpol: Network, buildLog: Hammer, problems: Stethoscope, copilot: Sparkles,
  traffic: ArrowLeftRight, argocd: GitPullRequestArrow, grafana: ChartColumn, istio: Waypoints,
}

export interface NavTool {
  id: string
  label: string
  icon: LucideIcon
}

export interface NavSection {
  id: string
  label: string
  icon: LucideIcon
  tools?: NavTool[]
  kinds: Resource[]
  /** Custom resources are grouped by API group. */
  groups?: { group: string; kinds: Resource[] }[]
  defaultCollapsed?: boolean
}

// Cluster-scoped kinds worth a place in the Cluster section; other core/k8s.io kinds go to "Outros recursos".
const CLUSTER_MAIN = new Set(['Node', 'Namespace', 'Project', 'Event', 'CustomResourceDefinition', 'PriorityClass', 'RuntimeClass', 'Lease'])

// Order of the most used kinds inside each section; the rest follow alphabetically.
const ORDER: Record<string, string[]> = {
  Cluster: ['Node', 'Namespace', 'Project', 'Event', 'CustomResourceDefinition', 'PriorityClass', 'RuntimeClass', 'Lease'],
  Workloads: ['Pod', 'Deployment', 'DaemonSet', 'StatefulSet', 'ReplicaSet', 'Job', 'CronJob', 'DeploymentConfig', 'ReplicationController', 'BuildConfig', 'Build', 'ImageStream'],
  Config: ['ConfigMap', 'Secret', 'ResourceQuota', 'LimitRange', 'HorizontalPodAutoscaler', 'PodDisruptionBudget'],
  Network: ['Service', 'Endpoints', 'Ingress', 'Route', 'NetworkPolicy', 'IngressClass'],
  Storage: ['PersistentVolumeClaim', 'PersistentVolume', 'StorageClass'],
  Access: ['ServiceAccount', 'Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding'],
}

function byOrder(cat: string) {
  const order = ORDER[cat] ?? []
  return (a: Resource, b: Resource) => {
    const pa = order.indexOf(a.kind), pb = order.indexOf(b.kind)
    if (pa !== -1 || pb !== -1) return (pa === -1 ? 99 : pa) - (pb === -1 ? 99 : pb)
    return a.kind.localeCompare(b.kind)
  }
}

/** Builds the navigator sections of a connected cluster. */
export function buildSections(resources: Resource[], filter: string): NavSection[] {
  const f = filter.trim().toLowerCase()
  const seen = new Set<string>()
  const list = resources.filter((r) => {
    if (r.kind === 'Event' && r.group === 'events.k8s.io') return false // the core Event is enough
    if (!r.verbs?.includes('list') && r.verbs?.length) return false
    const k = r.kind + '|' + r.group
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
  const match = (label: string, r?: Resource) =>
    !f || label.toLowerCase().includes(f) || (r && (r.resource.includes(f) || r.shortNames?.includes(f) || r.group.includes(f)))
  const kindsOf = (cat: string) => list.filter((r) => r.category === cat && match(plural(r.kind), r)).sort(byOrder(cat))
  const tools = (t: NavTool[]) => t.filter((x) => match(x.label))

  const crds = list.filter((r) => r.category === 'Custom Resources' && match(plural(r.kind), r))
  const groups = [...new Set(crds.map((r) => r.group))].sort().map((group) => ({ group, kinds: crds.filter((r) => r.group === group).sort(byOrder('')) }))

  const sections: NavSection[] = [
    { id: 'Cluster', label: 'Cluster', icon: Cloud, tools: tools([
      { id: 'overview', label: 'Visão geral', icon: LayoutDashboard },
      { id: 'problems', label: 'Problemas', icon: Stethoscope },
      { id: 'copilot', label: 'Copilot (IA)', icon: Sparkles },
      { id: 'topology', label: 'Topologia', icon: Workflow },
      { id: 'map', label: 'Mapa do cluster', icon: MapIcon },
      { id: 'timeline', label: 'Timeline de eventos', icon: Timer },
    ]), kinds: kindsOf('Cluster').filter((r) => CLUSTER_MAIN.has(r.kind)) },
    { id: 'Workloads', label: 'Workloads', icon: Layers, kinds: kindsOf('Workloads') },
    { id: 'Config', label: 'Configuração', icon: Settings2, kinds: kindsOf('Config') },
    { id: 'Network', label: 'Rede', icon: Network, kinds: kindsOf('Network') },
    { id: 'Storage', label: 'Armazenamento', icon: HardDrive, kinds: kindsOf('Storage') },
    { id: 'Access', label: 'Controle de acesso', icon: Key, tools: tools([{ id: 'rbac', label: 'Permissões (RBAC)', icon: Key }]), kinds: kindsOf('Access') },
    { id: 'Security', label: 'Segurança', icon: ShieldCheck, tools: tools([
      { id: 'security', label: 'Relatório de segurança', icon: ShieldCheck },
      { id: 'netpol', label: 'Análise de NetworkPolicy', icon: Network },
    ]), kinds: [] },
    { id: 'Helm', label: 'Helm', icon: Package, tools: tools([
      { id: 'helm', label: 'Releases', icon: Package },
      { id: 'charts', label: 'Charts', icon: GitBranch },
    ]), kinds: [] },
    // M5 optional integrations: always listed; each panel explains what is missing when absent.
    { id: 'Integrations', label: 'Integrações', icon: Blocks, tools: tools([
      { id: 'traffic', label: 'Tráfego observado', icon: ArrowLeftRight },
      { id: 'argocd', label: 'Argo CD', icon: GitPullRequestArrow },
      { id: 'grafana', label: 'Grafana e Prometheus', icon: ChartColumn },
      { id: 'istio', label: 'Istio', icon: Waypoints },
    ]), kinds: [] },
    { id: 'Custom Resources', label: 'Custom Resources', icon: FileCode, kinds: [], groups },
    { id: 'Other', label: 'Outros recursos', icon: Boxes, kinds: kindsOf('Cluster').filter((r) => !CLUSTER_MAIN.has(r.kind)), defaultCollapsed: true },
  ]
  return sections.filter((s) => s.tools?.length || s.kinds.length || s.groups?.length)
}
