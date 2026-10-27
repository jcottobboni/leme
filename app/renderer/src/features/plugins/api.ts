import { create } from 'zustand'
import { call, qs, type Resource, type Row } from '../../api'

export type PluginMode = 'terminal' | 'output' | 'background'

export interface PluginAction {
  id: string
  plugin: string
  title: string
  description?: string
  scopes: string[]
  command: string
  args: string[]
  mode: PluginMode
  mutates: boolean
  confirm: boolean
  shortCut?: string
  source: string
  format: 'leme' | 'k9s'
  hash: string
  trusted: boolean
}

export interface PluginLink {
  id: string
  plugin: string
  title: string
  scopes: string[]
  url: string
}

export interface PluginFile {
  path: string
  format?: string
  plugin?: string
  origin: 'leme' | 'k9s'
  error?: string
  warnings?: string[]
  actions: number
  links: number
  views: number
  rules: number
}

export interface PluginsInfo {
  dir: string
  k9s: boolean
  files: PluginFile[]
  actions: PluginAction[]
  links: PluginLink[]
  views: { plugin: string; group: string; kind: string; columns: string[] }[]
  rules: { plugin: string; id: string; title: string; severity: string; kinds: string[] }[]
}

/** The object an action or link runs on (k9s variables are built from it). */
export interface PluginInput {
  group: string
  version: string
  resource: string
  namespace?: string
  name: string
  container?: string
  filter?: string
  cols?: Record<string, string>
}

export interface RunResult {
  exitCode: number
  output: string
  truncated?: boolean
  error?: string
  durationMs: number
}

export const pluginsApi = {
  list: () => call<PluginsInfo>('GET', '/api/plugins'),
  reload: () => call<PluginsInfo>('POST', '/api/plugins/reload'),
  trust: (id: string, hash: string) => call<{ trusted: boolean }>('POST', '/api/plugins/trust', { id, hash }),
  revoke: (id: string) => call<{ trusted: boolean }>('DELETE', `/api/plugins/trust/${id.split('/').map(encodeURIComponent).join('/')}`),
  setK9s: (k9s: boolean) => call<PluginsInfo>('PUT', '/api/plugins/settings', { k9s }),
  run: (clusterId: string, action: string, input: PluginInput) => call<RunResult>('POST', `/api/clusters/${clusterId}/plugins/run`, { action, input }),
  fields: (clusterId: string, ref: { group: string; resource: string; namespace?: string; name: string }) =>
    call<{ plugin?: string; fields: { name: string; value: unknown }[] }>('GET', `/api/clusters/${clusterId}/plugins/fields?` + qs({ ...ref })),
}

/** Loaded plugins, shared by every menu. Refreshed on demand and when the window gets focus. */
export const usePlugins = create<{ info?: PluginsInfo; error?: string; at: number; set: (i: PluginsInfo) => void }>((set) => ({
  at: 0,
  set: (info) => set({ info, error: undefined, at: Date.now() }),
}))

let loading: Promise<void> | undefined

/** Loads the plugin list if it is missing or older than maxAge ms. */
export function ensurePlugins(maxAge = 15000) {
  const s = usePlugins.getState()
  if (s.info && Date.now() - s.at < maxAge) return Promise.resolve()
  loading ??= pluginsApi
    .list()
    .then((i) => usePlugins.getState().set(i))
    .catch((e) => usePlugins.setState({ error: (e as Error).message }))
    .finally(() => (loading = undefined))
  return loading
}

if (typeof window !== 'undefined') window.addEventListener('focus', () => void ensurePlugins(2000))

/** Names a k9s/Leme scope may use for a resource: plural, singular (kind), short names, plural.group. */
function namesOf(res: Resource) {
  const n = new Set([res.resource, res.kind.toLowerCase(), ...(res.shortNames ?? [])].map((s) => s.toLowerCase()))
  if (res.group) n.add(`${res.resource}.${res.group}`.toLowerCase())
  // k9s' container view: offered on pods (the run asks which container).
  if (res.group === '' && res.resource === 'pods') n.add('containers')
  return n
}

export function scopeMatches(scopes: string[], res: Resource) {
  if (scopes.includes('all')) return true
  const n = namesOf(res)
  return scopes.some((s) => n.has(s))
}

export function actionsFor(info: PluginsInfo | undefined, res: Resource) {
  return (info?.actions ?? []).filter((a) => scopeMatches(a.scopes, res))
}

export function linksFor(info: PluginsInfo | undefined, res: Resource) {
  return (info?.links ?? []).filter((l) => scopeMatches(l.scopes, res))
}

export function viewFor(info: PluginsInfo | undefined, group: string, kind: string) {
  return info?.views.find((v) => v.group === group && v.kind === kind)
}

export function inputOf(res: Resource, row: Pick<Row, 'name' | 'namespace' | 'cells'>, columns?: { key: string; label: string }[], filter?: string): PluginInput {
  const cols: Record<string, string> = {}
  for (const c of columns ?? []) {
    const v = row.cells?.[c.key]
    if (v !== undefined && v !== null && typeof v !== 'object') cols[c.label] = String(v)
  }
  return { group: res.group, version: res.version, resource: res.resource, namespace: row.namespace, name: row.name, filter: filter || undefined, cols }
}

/** Expands the k9s variables of a link URL; values are URL-encoded. Only http(s) results are returned. */
export function linkURL(l: PluginLink, input: PluginInput, ctx: { context: string; cluster: string; user: string }) {
  const vars: Record<string, string> = {
    NAMESPACE: input.namespace ?? '', NAME: input.name, CONTEXT: ctx.context, CLUSTER: ctx.cluster, USER: ctx.user,
    RESOURCE_GROUP: input.group, RESOURCE_VERSION: input.version, RESOURCE_NAME: input.resource, FILTER: input.filter ?? '',
    POD: input.group === '' && input.resource === 'pods' ? input.name : '', CONTAINER: input.container ?? '',
  }
  for (const [k, v] of Object.entries(input.cols ?? {})) vars[`COL-${k.trim().split(/\s+/).join('_').toUpperCase()}`] = v
  const out = l.url.replace(/\$\{(COL-[A-Z0-9_]+|[A-Z][A-Z0-9_]*)\}|\$(COL-[A-Z0-9_]+|[A-Z][A-Z0-9_]*)/g, (m, a, b) => {
    const k = a ?? b
    return k in vars ? encodeURIComponent(vars[k]) : m
  })
  try {
    const u = new URL(out)
    return u.protocol === 'http:' || u.protocol === 'https:' ? u.toString() : undefined
  } catch {
    return undefined
  }
}

/** The command line as the user will see it before trusting an action. */
export function commandLine(a: Pick<PluginAction, 'command' | 'args'>) {
  const q = (s: string) => (/^[\w./:=@%+,$-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`)
  return [a.command, ...a.args].map(q).join(' ')
}
