// Client for the local engine. Token and API base come from the URL fragment.

export type Health = 'ok' | 'warn' | 'error' | 'neutral'

export interface ClusterInfo {
  id: string
  name: string
  cluster: string
  user: string
  namespace?: string
  server: string
  source: string
  current: boolean
  prod: boolean
  local: boolean
  provider: string
  connected: boolean
  readOnly: boolean
  inAppLogin?: boolean
}

export interface Resource {
  group: string
  version: string
  resource: string
  kind: string
  namespaced: boolean
  category: string
  verbs: string[]
  shortNames?: string[]
}

export interface Column {
  key: string
  label: string
  type?: 'text' | 'number' | 'age' | 'status' | 'containers'
  width?: number
}

export interface Row {
  uid: string
  name: string
  namespace?: string
  created?: string
  health: Health
  labels?: Record<string, string>
  cells: Record<string, unknown>
}

export interface Ref {
  group: string
  version: string
  resource: string
  namespace?: string
  name: string
}

export interface ObjectView {
  object: Record<string, any>
  yaml: string
  masked: boolean
}

export interface ApplyResult {
  dryRun: boolean
  created: boolean
  yaml: string
}

export interface Forward {
  id: string
  clusterId: string
  context: string
  namespace: string
  kind: string
  name: string
  pod: string
  remotePort: number
  localPort: number
  url: string
  started: string
  error?: string
}

export interface KubeEvent {
  type: string
  reason: string
  message: string
  count?: number
  lastTimestamp?: string
  eventTime?: string
  firstTimestamp?: string
  source?: { component?: string }
  metadata: { uid: string; creationTimestamp?: string }
}

export type Usage = { cpu: number; memory: number }

// --- Helm ---
export interface HelmRelease {
  name: string
  namespace: string
  revision: number
  status: string
  chart: string
  chartVersion: string
  appVersion: string
  updated: string
  description?: string
  icon?: string
}
export interface HelmObject { apiVersion: string; kind: string; name: string; namespace?: string }
export interface HelmDetail extends HelmRelease {
  notes: string
  values: string
  computed: string
  manifest: string
  objects: HelmObject[]
  history: HelmRelease[]
  masked: boolean
}
export interface HelmResult { dryRun: boolean; revision: number; status: string; previous: string; manifest: string; notes?: string }
export interface HelmRepo { name: string; url: string; charts: number; indexed: boolean }
export interface ChartHit { repo: string; name: string; ref: string; version: string; appVersion: string; description: string; icon?: string; deprecated?: boolean; versions?: string[] }
export interface ChartInfo { name: string; version: string; appVersion: string; description: string; home?: string; icon?: string; readme: string; values: string }
export interface UpgradeRequest { chart?: string; version?: string; values?: string; replaceValues: boolean; dryRun: boolean }
export interface InstallRequest { namespace: string; name: string; chart: string; version?: string; values?: string; createNamespace: boolean; dryRun: boolean }

// --- RBAC ---
export interface AccessRequest { verb: string; group: string; resource: string; subresource?: string; name?: string; namespace?: string }
export interface MatrixRow { group: string; resource: string; kind: string; namespaced: boolean; verbs: Record<string, boolean> }
export interface MyRules {
  namespace: string
  incomplete: boolean
  evaluationError?: string
  rules: { verbs: string[]; apiGroups?: string[]; resources?: string[]; resourceNames?: string[] }[]
  nonResource: { verbs: string[]; nonResourceURLs: string[] }[]
  matrix: MatrixRow[]
}
export interface PolicyRule { verbs: string[]; apiGroups?: string[]; resources?: string[]; resourceNames?: string[]; nonResourceURLs?: string[] }
export interface Grant { bindingKind: string; bindingName: string; bindingNamespace?: string; roleKind: string; roleName: string; roleFound: boolean; rules?: PolicyRule[] }
export interface Subject { kind: string; name: string; namespace?: string; grants: Grant[] }
export interface WhoCanEntry { kind: string; name: string; namespace?: string; bindingKind: string; bindingName: string; bindingNamespace?: string; roleKind: string; roleName: string }

// --- Prometheus ---
export interface PromConfig { mode?: 'service' | 'url' | ''; namespace?: string; service?: string; port?: string; scheme?: string; path?: string; url?: string; sendToken?: boolean }
export interface PromScope { kind: 'pod' | 'workload' | 'node' | 'namespace' | 'cluster'; namespace?: string; name?: string; pods?: string[] }
export interface PromSeries { name: string; points: [number, number][]; ref?: string }
export interface PromChart { metric: string; unit: 'cores' | 'bytes' | 'bytes/s'; series: PromSeries[]; queries: string[]; step: number }

// --- Overview ---
export interface Res { cpu: number; memory: number; pods?: number }
export interface HealthCount { total: number; ok: number; warn: number; error: number }
export interface NodeSummary { name: string; health: Health; status: string; roles: string; allocatable: Res; requests: Res; limits: Res; usage?: Usage; pods: number }
export interface Overview {
  scope: string
  nodes: NodeSummary[]
  capacity: Res
  allocatable: Res
  requests: Res
  limits: Res
  usage?: Usage
  pods: Record<string, number>
  workloads: Record<string, HealthCount>
  namespaces: number
  warnings: { namespace: string; kind: string; name: string; reason: string; message: string; count: number; last: string }[]
  /** Per-namespace breakdown (pods by phase, requests/limits, usage when metrics-server answers). */
  namespaceStats?: NamespaceStat[]
  errors?: Record<string, string>
}
export interface NamespaceStat { name: string; pods: Record<string, number>; requests: Res; limits: Res; usage?: Usage }

// --- Graph ---
export interface GraphNode { id: string; group: string; kind: string; name: string; namespace?: string; health: Health; status?: string; count?: number; pods?: Record<string, number>; ref?: boolean; missing?: boolean; app?: string }
export interface GraphEdge { from: string; to: string; rel: string }
export interface Graph { root?: string; nodes: GraphNode[]; edges: GraphEdge[]; truncated?: boolean; errors?: Record<string, string> }

// --- Cluster map (namespaces as islands, apps as regions) ---
export interface MapWorkload { group: string; kind: string; name: string; health: Health; status?: string; ready?: string; pods: HealthCount; services?: string[]; pvcs?: number; restarts?: number; hosts?: string[] }
export interface MapApp { name: string; workloads: MapWorkload[] }
export interface MapNamespace { name: string; status?: string; pods: HealthCount; apps: MapApp[]; services: number; ingresses: number; pvcs: number }
export interface ClusterMap { scope: string; namespaces: MapNamespace[]; truncated?: boolean; compact?: boolean; errors?: Record<string, string> }

// --- Search, rollout, timeline ---
export interface SearchHit { group: string; kind: string; name: string; namespace?: string; match: string }
export interface Revision { revision: number; replicaSet: string; created: string; images: string[]; replicas: number; changeCause?: string; current: boolean; template: string }
export interface TimelineEvent {
  id: string
  type: string
  reason: string
  message: string
  count: number
  first: number
  last: number
  namespace: string
  group?: string
  kind: string
  name: string
  source?: string
  path: string[]
  inferred?: boolean
}

// --- Security (M3) ---
export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info'
export interface Finding {
  id: string
  severity: Severity
  category: string
  title: string
  detail?: string
  remediation?: string
  group: string
  kind: string
  namespace?: string
  name: string
  container?: string
  source: string
  effective?: boolean
}
export interface WorkloadScore { group: string; kind: string; namespace?: string; name: string; score: number; counts: Record<string, number> }
export interface SecurityReport {
  scope: string
  score: number
  counts: Record<string, number>
  byCategory: Record<string, number>
  findings: Finding[]
  workloads: WorkloadScore[]
  namespacesWithoutNetpol: string[]
  errors?: Record<string, string>
}
export interface RBACRisk {
  severity: Severity
  title: string
  detail: string
  subjectKind: string
  subjectName: string
  subjectNamespace?: string
  bindingKind: string
  bindingName: string
  bindingNamespace?: string
  roleKind: string
  roleName: string
  system: boolean
}
export interface ScanTool { name: string; installed: boolean; path?: string; purpose: string }
export interface Vuln { id: string; package: string; installed: string; fixed?: string; severity: Severity; title?: string }
export interface ImageReport { image: string; container?: string; source: string; scanned: string; os?: string; counts: Record<string, number>; vulns: Vuln[]; error?: string }

// --- NetworkPolicy ---
export interface NetGroup { id: string; label: string; pods: string[]; ingressPolicies: string[]; egressPolicies: string[]; ingressIsolated: boolean; egressIsolated: boolean }
export interface NetFlow { from: string; to: string; ports: string; policy: string }
export interface NetPolicySummary { name: string; selector: string; types: string[]; ingress: string[]; egress: string[]; selects: string[] }
export interface NetAnalysis { namespace: string; groups: NetGroup[]; peers: { id: string; label: string }[]; flows: NetFlow[]; policies: NetPolicySummary[] }
export interface NetVerdict { isolated: boolean; allowed: boolean; policies: string[]; allowedBy?: string[] }
export interface NetCheck { allowed: boolean; egress: NetVerdict; ingress: NetVerdict; reason: string }
export interface NetEndpoint { namespace?: string; pod?: string; ip?: string }

// The token arrives in the URL fragment (never sent to the server), is kept for this tab only
// and removed from the address so it doesn't linger in history or screenshots.
const params = new URLSearchParams(location.hash.replace(/^#/, ''))
const token = (() => {
  const fromHash = params.get('token')
  try {
    if (fromHash) sessionStorage.setItem('leme.token', fromHash)
    return fromHash ?? sessionStorage.getItem('leme.token') ?? ''
  } catch {
    return fromHash ?? ''
  }
})()
if (params.has('token')) history.replaceState(null, '', location.pathname + location.search)

// A separate API origin is only honoured in dev (Vite server) and only on loopback.
const base = (() => {
  const api = params.get('api')
  if (import.meta.env.DEV && api && /^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(api)) return api
  return location.origin
})()

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

export async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(base + path, {
    method,
    headers: { Authorization: `Bearer ${token}`, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) throw new ApiError(res.status, data?.error ?? res.statusText)
  return data as T
}

export function qs(o: Record<string, string | number | boolean | undefined>) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(o)) if (v !== undefined && v !== '') p.set(k, String(v))
  return p.toString()
}

export function wsURL(path: string, query: Record<string, string | number | boolean | undefined>) {
  const u = new URL(base + path)
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
  u.search = qs(query)
  return u.toString()
}

/** Opens an authenticated WebSocket; the token travels as a subprotocol, never in the URL. */
export function openSocket(path: string, query: Record<string, string | number | boolean | undefined>) {
  return new WebSocket(wsURL(path, query), ['leme', `bearer.${token}`])
}

export const api = {
  hasToken: () => token !== '',
  clusters: () => call<{ clusters: ClusterInfo[]; kubeconfigs: string[] }>('GET', '/api/clusters'),
  connect: (id: string) => call<{ resources: Resource[]; namespace: string; readOnly: boolean; version: string }>('POST', `/api/clusters/${id}/connect`),
  disconnect: (id: string) => call('POST', `/api/clusters/${id}/disconnect`),
  setReadOnly: (id: string, readOnly: boolean, confirm = '') => call<{ readOnly: boolean }>('POST', `/api/clusters/${id}/readonly`, { readOnly, confirm }),
  object: (id: string, ref: Ref, reveal = false) =>
    call<ObjectView>('GET', `/api/clusters/${id}/object?` + qs({ ...ref, reveal: reveal ? 1 : undefined })),
  apply: (id: string, yaml: string, dryRun: boolean) => call<ApplyResult>('POST', `/api/clusters/${id}/apply`, { yaml, dryRun }),
  remove: (id: string, ref: Ref) => call('POST', `/api/clusters/${id}/delete`, ref),
  scale: (id: string, ref: Ref, replicas: number) => call('POST', `/api/clusters/${id}/scale`, { ...ref, replicas }),
  restart: (id: string, ref: Ref) => call('POST', `/api/clusters/${id}/restart`, ref),
  events: (id: string, namespace: string, uid: string) => call<KubeEvent[]>('GET', `/api/clusters/${id}/events?` + qs({ namespace, uid })),
  podMetrics: (id: string, namespace: string) => call<Record<string, Usage>>('GET', `/api/clusters/${id}/metrics/pods?` + qs({ namespace })),
  nodeMetrics: (id: string) => call<Record<string, Usage>>('GET', `/api/clusters/${id}/metrics/nodes`),
  forwards: () => call<Forward[]>('GET', '/api/portforwards'),
  startForward: (id: string, req: { namespace: string; kind: 'pod' | 'service'; name: string; remotePort: number; localPort: number }) =>
    call<Forward>('POST', `/api/clusters/${id}/portforwards`, req),
  stopForward: (fid: string) => call('DELETE', `/api/portforwards/${fid}`),

  helmReleases: (id: string, namespace: string) => call<HelmRelease[]>('GET', `/api/clusters/${id}/helm/releases?` + qs({ namespace })),
  helmRelease: (id: string, ns: string, name: string, reveal = false) =>
    call<HelmDetail>('GET', `/api/clusters/${id}/helm/releases/${enc(ns)}/${enc(name)}?` + qs({ reveal: reveal ? 1 : undefined })),
  helmManifest: (id: string, ns: string, name: string, revision: number) =>
    call<{ manifest: string }>('GET', `/api/clusters/${id}/helm/releases/${enc(ns)}/${enc(name)}/manifest?` + qs({ revision })),
  helmUpgrade: (id: string, ns: string, name: string, req: UpgradeRequest) => call<HelmResult>('POST', `/api/clusters/${id}/helm/releases/${enc(ns)}/${enc(name)}/upgrade`, req),
  helmRollback: (id: string, ns: string, name: string, revision: number, dryRun: boolean) =>
    call<HelmResult>('POST', `/api/clusters/${id}/helm/releases/${enc(ns)}/${enc(name)}/rollback`, { revision, dryRun }),
  helmUninstall: (id: string, ns: string, name: string, dryRun: boolean) => call<HelmResult>('POST', `/api/clusters/${id}/helm/releases/${enc(ns)}/${enc(name)}/uninstall`, { dryRun }),
  helmInstall: (id: string, req: InstallRequest) => call<HelmResult>('POST', `/api/clusters/${id}/helm/install`, req),
  helmRepos: () => call<HelmRepo[]>('GET', '/api/helm/repos'),
  helmAddRepo: (name: string, url: string) => call('POST', '/api/helm/repos', { name, url }),
  helmRemoveRepo: (name: string) => call('DELETE', `/api/helm/repos/${enc(name)}`),
  helmUpdateRepos: (names: string[] = []) => call<{ errors: Record<string, string> }>('POST', '/api/helm/repos/update', { names }),
  helmSearch: (q: string) => call<ChartHit[]>('GET', '/api/helm/charts?' + qs({ q })),
  helmShow: (ref: string, version?: string) => call<ChartInfo>('GET', '/api/helm/chart?' + qs({ ref, version })),

  rbacMine: (id: string, namespace: string) => call<MyRules>('GET', `/api/clusters/${id}/rbac/mine?` + qs({ namespace })),
  rbacCan: (id: string, req: AccessRequest) => call<{ allowed: boolean; denied: boolean; reason?: string }>('POST', `/api/clusters/${id}/rbac/can`, req),
  rbacSubjects: (id: string, refresh = false) =>
    call<{ subjects: Subject[]; errors: Record<string, string> }>('GET', `/api/clusters/${id}/rbac/subjects?` + qs({ refresh: refresh ? 1 : undefined })),
  rbacWhoCan: (id: string, req: AccessRequest) => call<{ entries: WhoCanEntry[]; errors: Record<string, string> }>('GET', `/api/clusters/${id}/rbac/whocan?` + qs({ ...req })),

  promConfig: (id: string, refresh = false) =>
    call<{ active?: PromConfig; saved: boolean; candidates: (PromConfig & { score: number })[] }>('GET', `/api/clusters/${id}/prometheus?` + qs({ refresh: refresh ? 1 : undefined })),
  promSave: (id: string, cfg: PromConfig) => call('PUT', `/api/clusters/${id}/prometheus`, cfg),
  promTest: (id: string, cfg: PromConfig) => call<{ ok: boolean; error?: string; targets?: number }>('POST', `/api/clusters/${id}/prometheus/test`, cfg),
  promChart: (id: string, scope: PromScope, metrics: string[], range: string) =>
    call<{ charts: (PromChart | null)[]; errors: string[]; source: PromConfig }>('POST', `/api/clusters/${id}/prometheus/chart`, { scope, metrics, range }),

  overview: (id: string, namespace: string) => call<Overview>('GET', `/api/clusters/${id}/overview?` + qs({ namespace })),
  clusterMap: (id: string, namespace?: string) => call<ClusterMap>('GET', `/api/clusters/${id}/map?` + qs({ namespace })),
  graph: (id: string, namespace: string, kind?: string, name?: string) => call<Graph>('GET', `/api/clusters/${id}/graph?` + qs({ namespace, kind, name })),
  search: (id: string, q: string) => call<{ hits: SearchHit[]; errors?: Record<string, string> }>('GET', `/api/clusters/${id}/search?` + qs({ q })),
  rolloutHistory: (id: string, namespace: string, name: string) => call<Revision[]>('GET', `/api/clusters/${id}/rollout/history?` + qs({ namespace, name })),
  rolloutUndo: (id: string, namespace: string, name: string, revision: number, dryRun: boolean) =>
    call<{ dryRun: boolean; current: string; proposed: string }>('POST', `/api/clusters/${id}/rollout/undo`, { namespace, name, revision, dryRun }),
}

export const api3 = {
  securityTools: () => call<ScanTool[]>('GET', '/api/security/tools'),
  securityReport: (id: string, namespace: string) => call<SecurityReport>('GET', `/api/clusters/${id}/security/report?` + qs({ namespace })),
  securityObject: (id: string, kind: string, namespace: string, name: string) =>
    call<{ findings: Finding[]; score: number; applicable: boolean }>('GET', `/api/clusters/${id}/security/object?` + qs({ kind, namespace, name })),
  securityRBAC: (id: string, refresh = false) => call<{ risks: RBACRisk[]; errors: Record<string, string> }>('GET', `/api/clusters/${id}/security/rbac?` + qs({ refresh: refresh ? 1 : undefined })),
  securityScan: (id: string, tool: 'polaris' | 'kubescape', namespace: string, framework?: string) =>
    call<{ findings: Finding[] }>('POST', `/api/clusters/${id}/security/scan`, { tool, namespace, framework }),
  securityImages: (id: string, group: string, kind: string, namespace: string, name: string) =>
    call<{ images: { image: string; container: string }[]; operator: boolean; reports?: ImageReport[]; operatorError?: string }>('GET', `/api/clusters/${id}/security/images?` + qs({ group, kind, namespace, name })),
  scanImages: (id: string, images: string[], refresh = false) => call<ImageReport[]>('POST', `/api/clusters/${id}/security/images/scan`, { images, refresh }),
  netpol: (id: string, namespace: string) => call<{ analysis: NetAnalysis; errors: Record<string, string> }>('GET', `/api/clusters/${id}/netpol?` + qs({ namespace })),
  netpolCheck: (id: string, src: NetEndpoint, dst: NetEndpoint, port: number, protocol = 'TCP') =>
    call<{ result: NetCheck; errors: Record<string, string> }>('POST', `/api/clusters/${id}/netpol/check`, { src, dst, port, protocol }),
  rolloutLatest: (id: string, namespace: string, name: string) => call<{ revision: number }>('POST', `/api/clusters/${id}/openshift/rollout`, { namespace, name }),
  startBuild: (id: string, namespace: string, name: string) => call<{ build: string }>('POST', `/api/clusters/${id}/openshift/build`, { namespace, name }),
  oauthInfo: (id: string) => call<{ available: boolean; issuer?: string; inAppToken?: boolean; insecure?: boolean; error?: string }>('GET', `/api/clusters/${id}/oauth`),
  oauthWebCancel: (id: string) => call('POST', `/api/clusters/${id}/oauth/web/cancel`),
  oauthLogout: (id: string) => call('DELETE', `/api/clusters/${id}/oauth`),
  oauthPassword: (id: string, username: string, password: string) => call<{ user: string }>('POST', `/api/clusters/${id}/oauth/password`, { username, password }),
  oauthWebStart: (id: string) => call<{ url: string }>('POST', `/api/clusters/${id}/oauth/web/start`),
  oauthWebWait: (id: string) => call<{ user: string }>('POST', `/api/clusters/${id}/oauth/web/wait`),
}

function enc(s: string) {
  return encodeURIComponent(s)
}

export function refOf(r: Resource, name: string, namespace?: string): Ref {
  return { group: r.group, version: r.version, resource: r.resource, name, namespace: r.namespaced ? namespace : undefined }
}
