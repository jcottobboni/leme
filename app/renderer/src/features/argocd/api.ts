import { call, qs } from '../../api'

export interface ArgoSource { repoURL: string; path?: string; targetRevision?: string; chart?: string }
export interface ArgoDestination { server?: string; namespace?: string; name?: string }

export interface ArgoApp {
  namespace: string
  name: string
  project: string
  sync: string
  health: string
  source?: ArgoSource
  sources?: ArgoSource[]
  destination: ArgoDestination
  revision?: string
  operationPhase?: string
  automated?: boolean
  local: boolean
}

export interface ArgoProject { namespace: string; name: string; description?: string; sourceRepos?: string[]; destinations?: ArgoDestination[] }

export interface ArgoOverview {
  installed: boolean
  apps: ArgoApp[]
  projects: ArgoProject[]
  hint?: string
  errors?: Record<string, string>
}

export interface ArgoResource {
  group: string
  version?: string
  kind: string
  namespace?: string
  name: string
  status?: string
  health?: string
  message?: string
  exists?: boolean
}

export interface ArgoCondition { type: string; message?: string; lastTransitionTime?: string }
export interface ArgoHistory { id: number; revision?: string; revisions?: string[]; deployedAt?: string; source?: ArgoSource; initiatedBy?: string }
export interface ArgoOperation { phase?: string; message?: string; startedAt?: string; finishedAt?: string; revision?: string; initiatedBy?: string }

export interface ArgoDetail extends ArgoApp {
  resources: ArgoResource[]
  conditions: ArgoCondition[]
  history: ArgoHistory[]
  operationState?: ArgoOperation
  pendingOperation?: boolean
  healthMessage?: string
  reconciledAt?: string
  checked: boolean
}

export interface ArgoProposal {
  token: string
  ref: { group: string; kind: string; namespace?: string; name: string }
  summary: string
  before: string
  after: string
  dryRun: boolean
  dryRunError?: string
  rbacAllowed: boolean
  risks?: string[]
  notes?: string[]
  expires: string
}

export type ArgoAction = { action: 'sync'; prune?: boolean } | { action: 'refresh'; hard?: boolean }

const base = (clusterId: string) => `/api/clusters/${encodeURIComponent(clusterId)}/argocd`
const appPath = (clusterId: string, ns: string, name: string) => `${base(clusterId)}/apps/${encodeURIComponent(ns)}/${encodeURIComponent(name)}`

export const argoApi = {
  list: (clusterId: string, namespace = '') => call<ArgoOverview>('GET', `${base(clusterId)}?${qs({ namespace })}`),
  detail: (clusterId: string, ns: string, name: string) => call<ArgoDetail>('GET', appPath(clusterId, ns, name)),
  propose: (clusterId: string, ns: string, name: string, a: ArgoAction) => call<ArgoProposal>('POST', `${appPath(clusterId, ns, name)}/propose`, a),
  apply: (clusterId: string, token: string) => call<{ applied: boolean }>('POST', `${base(clusterId)}/apply/${encodeURIComponent(token)}`),
}

/** Maps Argo CD sync and health statuses onto the app's health colors. */
export function syncTone(s?: string) {
  if (s === 'Synced') return 'ok'
  if (s === 'OutOfSync') return 'warn'
  return 'neutral'
}

export function healthTone(h?: string) {
  switch (h) {
    case 'Healthy': return 'ok'
    case 'Progressing': case 'Suspended': return 'warn'
    case 'Degraded': case 'Missing': return 'error'
    default: return 'neutral'
  }
}

export function phaseTone(p?: string) {
  if (p === 'Succeeded') return 'ok'
  if (p === 'Running' || p === 'Terminating') return 'warn'
  if (p === 'Failed' || p === 'Error') return 'error'
  return 'neutral'
}

export const SYNC_LABEL: Record<string, string> = { Synced: 'Sincronizado', OutOfSync: 'Fora de sincronia', Unknown: 'Desconhecido' }
export const HEALTH_LABEL: Record<string, string> = {
  Healthy: 'Saudável', Progressing: 'Progredindo', Degraded: 'Degradado', Suspended: 'Suspenso', Missing: 'Ausente', Unknown: 'Desconhecido',
}
