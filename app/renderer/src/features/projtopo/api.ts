import { call, type Health } from '../../api'

export type Phase = 'running' | 'notReady' | 'pending' | 'failed' | 'succeeded' | 'terminating' | 'unknown'
export type PodPhases = Record<Phase, number>

export interface TopoPod { name: string; phase: Phase; ready: string; restarts: number }
export interface TopoBuild { config: string; name?: string; phase?: string }

export interface TopoNode {
  id: string
  group: string
  kind: string
  name: string
  app?: string
  release?: string
  health: Health
  status?: string
  ready?: string
  desired?: number
  phases: PodPhases
  pods: TopoPod[]
  services: string[]
  url?: string
  vcs?: string
  runtime?: string
  build?: TopoBuild
  labels?: Record<string, string>
}

export interface ProjectTopology {
  namespace: string
  nodes: TopoNode[]
  edges: { source: string; target: string }[]
  errors?: Record<string, string>
}

export const getProjectTopology = (clusterId: string, namespace: string) =>
  call<ProjectTopology>('GET', `/api/clusters/${encodeURIComponent(clusterId)}/projtopo?namespace=${encodeURIComponent(namespace)}`)

// Ring order and labels, as in the OpenShift console pod ring.
export const PHASES: [Phase, string][] = [
  ['running', 'Rodando'], ['notReady', 'Não pronto'], ['pending', 'Pendente'], ['failed', 'Com falha'],
  ['succeeded', 'Concluído'], ['terminating', 'Encerrando'], ['unknown', 'Desconhecido'],
]

export const podTotal = (p: PodPhases) => PHASES.reduce((a, [k]) => a + (p[k] ?? 0), 0)

export const KIND_BADGE: Record<string, [string, string]> = {
  Deployment: ['D', 'deploy'], DeploymentConfig: ['DC', 'deploy'], ReplicaSet: ['RS', 'deploy'], ReplicationController: ['RC', 'deploy'],
  StatefulSet: ['SS', 'set'], DaemonSet: ['DS', 'set'], CronJob: ['CJ', 'job'], Job: ['J', 'job'], Pod: ['P', 'pod'],
}
export const badgeOf = (kind: string): [string, string] => KIND_BADGE[kind] ?? [(kind.replace(/[a-z]/g, '') || kind).slice(0, 2), 'other']

export const isDefaultProject = (ns: string) => ns === 'default' || ns.startsWith('kube-') || ns.startsWith('openshift')
