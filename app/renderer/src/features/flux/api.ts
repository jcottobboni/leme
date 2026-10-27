import { call, qs } from '../../api'

export interface FluxRef { kind: string; namespace?: string; name: string }
export interface FluxCondition { type: string; status: string; reason?: string; message?: string; lastTransitionTime?: string }

/** A Kustomization, HelmRelease or source (GitRepository, OCIRepository, HelmRepository, Bucket). */
export interface FluxObject {
  group: string
  kind: string
  namespace: string
  name: string
  ready: 'True' | 'False' | 'Unknown' | string
  reason?: string
  message?: string
  suspended: boolean
  reconciling?: boolean
  interval?: string
  conditions: FluxCondition[]
  revision?: string
  attempted?: string
  source?: FluxRef
  path?: string
  chart?: string
  url?: string
  ref?: string
  targetNamespace?: string
  requestedAt?: string
  handledAt?: string
  updatedAt?: string
}

export interface FluxOverview {
  installed: boolean
  groups: Record<string, boolean>
  kustomizations: FluxObject[]
  helmReleases: FluxObject[]
  sources: FluxObject[]
  hint?: string
  errors?: Record<string, string>
}

export interface FluxProposal {
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

export type FluxAction = 'reconcile' | 'suspend' | 'resume'

const base = (clusterId: string) => `/api/clusters/${encodeURIComponent(clusterId)}/flux`

export const fluxApi = {
  list: (clusterId: string, namespace = '') => call<FluxOverview>('GET', `${base(clusterId)}?${qs({ namespace })}`),
  propose: (clusterId: string, o: { kind: string; namespace: string; name: string }, action: FluxAction) =>
    call<FluxProposal>('POST', `${base(clusterId)}/${encodeURIComponent(o.kind)}/${encodeURIComponent(o.namespace)}/${encodeURIComponent(o.name)}/propose`, { action }),
  apply: (clusterId: string, token: string) => call<{ applied: boolean }>('POST', `${base(clusterId)}/apply/${encodeURIComponent(token)}`),
}

/** The state shown for a Flux object: suspension first, then the Ready condition. */
export function fluxState(o: FluxObject): { label: string; tone: 'ok' | 'warn' | 'error' | 'neutral' } {
  if (o.suspended) return { label: 'Suspenso', tone: 'warn' }
  if (o.ready === 'True') return { label: 'Pronto', tone: 'ok' }
  if (o.ready === 'False') return { label: 'Falhou', tone: 'error' }
  if (o.reconciling) return { label: 'Reconciliando', tone: 'warn' }
  return { label: 'Desconhecido', tone: 'neutral' }
}

/** Shortens "main@sha1:<40 hex>" and bare digests for the table. */
export function shortRevision(r?: string) {
  if (!r) return '—'
  return r.replace(/(sha1|sha256):([0-9a-f]{12})[0-9a-f]+/g, '$1:$2').replace(/^([0-9a-f]{12})[0-9a-f]{28}$/, '$1')
}
