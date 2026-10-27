// k9s-style filter syntax shared by the palette (Ctrl+K) and the grid search (M7 RF05).
//
//   :deploy [ns]     navigate to a resource type (aliases, plural/singular names, CRD shortNames)
//   -l app=web       label selector, applied by the engine on the watch (Kubernetes syntax)
//   ns:prod          namespace filter
//   !text            negation (also !/regex/)
//   /regex/          regular expression (case-insensitive)
//   text             substring of the name, namespace, labels or cells
// Terms combine with AND. Quotes keep spaces: -l "env in (a, b)".
import type { Resource, Row } from '../../api'

export type TermKind = 'alias' | 'label' | 'ns' | 'text' | 'not' | 'regex' | 'notregex'

export interface Term {
  kind: TermKind
  value: string
  /** The term as typed, used to remove it (filter chips). */
  raw: string
  error?: string
  re?: RegExp
}

export interface Filter {
  terms: Term[]
  alias?: string
  /** Positional argument after the alias: a namespace (`:deploy demo`). */
  aliasArg?: string
  labelSelector?: string
  namespaces: string[]
  include: string[]
  exclude: string[]
  regex: RegExp[]
  notRegex: RegExp[]
  errors: string[]
}

/** k9s / kubectl short names. Discovery adds every CRD's shortNames on top of these. */
export const ALIASES: Record<string, string> = {
  po: 'Pod', pod: 'Pod', pods: 'Pod',
  deploy: 'Deployment', deployment: 'Deployment', deployments: 'Deployment', dp: 'Deployment',
  sts: 'StatefulSet', statefulset: 'StatefulSet', statefulsets: 'StatefulSet',
  ds: 'DaemonSet', daemonset: 'DaemonSet', daemonsets: 'DaemonSet',
  rs: 'ReplicaSet', svc: 'Service', service: 'Service', services: 'Service',
  ing: 'Ingress', ingress: 'Ingress', ingresses: 'Ingress',
  cm: 'ConfigMap', configmap: 'ConfigMap', configmaps: 'ConfigMap',
  secret: 'Secret', secrets: 'Secret', sec: 'Secret',
  ns: 'Namespace', namespace: 'Namespace', namespaces: 'Namespace',
  no: 'Node', node: 'Node', nodes: 'Node',
  pvc: 'PersistentVolumeClaim', pv: 'PersistentVolume', sc: 'StorageClass',
  job: 'Job', jobs: 'Job', cj: 'CronJob', cronjob: 'CronJob', cronjobs: 'CronJob',
  ev: 'Event', events: 'Event', sa: 'ServiceAccount', ep: 'Endpoints',
  hpa: 'HorizontalPodAutoscaler', netpol: 'NetworkPolicy', pdb: 'PodDisruptionBudget', crd: 'CustomResourceDefinition',
}

function tokenize(input: string): string[] {
  const out: string[] = []
  let i = 0
  const s = input
  while (i < s.length) {
    while (i < s.length && /\s/.test(s[i])) i++
    if (i >= s.length) break
    let tok = ''
    // A regex (optionally negated) runs until the closing unescaped slash, spaces included.
    const reStart = s[i] === '/' ? i : s[i] === '!' && s[i + 1] === '/' ? i + 1 : -1
    if (reStart >= 0) {
      let j = reStart + 1
      while (j < s.length && !(s[j] === '/' && s[j - 1] !== '\\')) j++
      if (j < s.length) {
        out.push(s.slice(i, j + 1))
        i = j + 1
        continue
      }
    }
    while (i < s.length && !/\s/.test(s[i])) {
      if (s[i] === '"') {
        const j = s.indexOf('"', i + 1)
        if (j < 0) {
          tok += s.slice(i + 1)
          i = s.length
          break
        }
        tok += s.slice(i + 1, j)
        i = j + 1
        continue
      }
      tok += s[i++]
    }
    out.push(tok)
  }
  return out
}

function compile(src: string): { re?: RegExp; error?: string } {
  try {
    return { re: new RegExp(src, 'i') }
  } catch (e) {
    return { error: `regex inválida: ${(e as Error).message.replace(/^Invalid regular expression: /, '')}` }
  }
}

export function parseFilter(input: string): Filter {
  const f: Filter = { terms: [], namespaces: [], include: [], exclude: [], regex: [], notRegex: [], errors: [] }
  const toks = tokenize(input)
  const labels: string[] = []
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i]
    if (!t) continue
    if (i === 0 && t.startsWith(':') && t.length > 1) {
      f.alias = t.slice(1).toLowerCase()
      f.terms.push({ kind: 'alias', value: f.alias, raw: t })
      continue
    }
    if (t === '-l' || t === '--selector') {
      const v = toks[i + 1]
      if (v === undefined || v === '') {
        f.terms.push({ kind: 'label', value: '', raw: t, error: 'faltou o seletor depois de -l' })
        continue
      }
      i++
      labels.push(v)
      f.terms.push({ kind: 'label', value: v, raw: `${t} ${/\s/.test(v) ? `"${v}"` : v}` })
      continue
    }
    if (t.startsWith('-l') && t.length > 2 && /[=!]/.test(t)) {
      const v = t.slice(2)
      labels.push(v)
      f.terms.push({ kind: 'label', value: v, raw: t })
      continue
    }
    if (/^ns:/i.test(t)) {
      const v = t.slice(3)
      if (v) {
        f.namespaces.push(v)
        f.terms.push({ kind: 'ns', value: v, raw: t })
      }
      continue
    }
    const neg = t.startsWith('!')
    const body = neg ? t.slice(1) : t
    if (body.length >= 2 && body.startsWith('/') && body.endsWith('/')) {
      const { re, error } = compile(body.slice(1, -1))
      const kind = neg ? 'notregex' : 'regex'
      f.terms.push({ kind, value: body.slice(1, -1), raw: t, re, error })
      if (re) (neg ? f.notRegex : f.regex).push(re)
      continue
    }
    if (neg) {
      if (body) {
        f.exclude.push(body.toLowerCase())
        f.terms.push({ kind: 'not', value: body, raw: t })
      }
      continue
    }
    // `:deploy demo`: the first plain word after the alias is the namespace.
    if (f.alias && f.aliasArg === undefined && f.terms.length === 1) {
      f.aliasArg = t
      f.terms.push({ kind: 'ns', value: t, raw: t })
      f.namespaces.push(t)
      continue
    }
    f.include.push(t.toLowerCase())
    f.terms.push({ kind: 'text', value: t, raw: /\s/.test(t) ? `"${t}"` : t })
  }
  if (labels.length) f.labelSelector = labels.join(',')
  f.errors = f.terms.filter((t) => t.error).map((t) => t.error!)
  return f
}

/** The query without one term (removing a filter chip). */
export function withoutTerm(f: Filter, idx: number): string {
  return f.terms.filter((_, i) => i !== idx).map((t) => t.raw).join(' ')
}

/** Text the grid search box keeps after the palette navigates: everything except the alias and its namespace. */
export function gridText(f: Filter): string {
  return f.terms.filter((t) => t.kind !== 'alias' && t.kind !== 'ns').map((t) => t.raw).join(' ')
}

/** True when the query uses structured syntax (shows chips and an explanation). */
export function isStructured(f: Filter) {
  return f.terms.some((t) => t.kind !== 'text')
}

function fields(row: Row): string[] {
  const out = [row.name, row.namespace ?? '']
  for (const [k, v] of Object.entries(row.labels ?? {})) out.push(`${k}=${v}`)
  for (const v of Object.values(row.cells)) if (v !== null && v !== undefined && typeof v !== 'object') out.push(String(v))
  return out
}

/** Client-side part of the filter (the label selector already ran in the engine). */
export function matchRow(row: Row, f: Filter): boolean {
  if (f.namespaces.length && !f.namespaces.includes(row.namespace ?? '')) return false
  if (!f.include.length && !f.exclude.length && !f.regex.length && !f.notRegex.length) return true
  const all = fields(row)
  const lower = all.map((s) => s.toLowerCase())
  if (!f.include.every((t) => lower.some((s) => s.includes(t)))) return false
  if (f.exclude.some((t) => lower.some((s) => s.includes(t)))) return false
  if (!f.regex.every((re) => all.some((s) => re.test(s)))) return false
  if (f.notRegex.some((re) => all.some((s) => re.test(s)))) return false
  return true
}

/** Palette hits only carry name, namespace and kind. */
export function matchHit(hit: { name: string; namespace?: string; kind: string }, f: Filter): boolean {
  if (f.namespaces.length && !f.namespaces.includes(hit.namespace ?? '')) return false
  const all = [hit.name, hit.namespace ?? '']
  const lower = all.map((s) => s.toLowerCase())
  if (f.exclude.some((t) => lower.some((s) => s.includes(t)))) return false
  if (!f.regex.every((re) => all.some((s) => re.test(s)))) return false
  if (f.notRegex.some((re) => all.some((s) => re.test(s)))) return false
  return true
}

/** Resource types an alias names in one cluster: k9s aliases, kind/plural/singular, CRD shortNames. */
export function resolveAlias(resources: Resource[], alias: string): Resource[] {
  const a = alias.toLowerCase()
  if (!a) return []
  const kind = ALIASES[a]
  const exact = resources.filter((r) =>
    (kind && r.kind === kind && (r.group === '' || ['apps', 'batch', 'networking.k8s.io', 'storage.k8s.io', 'policy', 'autoscaling', 'apiextensions.k8s.io', 'events.k8s.io'].includes(r.group))) ||
    r.shortNames?.includes(a) || r.resource === a || r.kind.toLowerCase() === a || `${r.resource}.${r.group}` === a)
  if (exact.length) {
    // Core Events and events.k8s.io Events both exist: keep one per kind, built-in group first.
    const seen = new Set<string>()
    return exact.filter((r) => (seen.has(r.kind + r.resource) ? false : (seen.add(r.kind + r.resource), true)))
  }
  return resources.filter((r) => r.resource.startsWith(a) || r.kind.toLowerCase().startsWith(a)).slice(0, 8)
}

// The palette hands the rest of the query to the grid it opens (or focuses).
const presets = new Map<string, string>()
const presetKey = (clusterId: string, group: string, resource: string) => `${clusterId}|${group}/${resource}`

export function presetGridFilter(clusterId: string, group: string, resource: string, text: string) {
  presets.set(presetKey(clusterId, group, resource), text)
  window.dispatchEvent(new CustomEvent('leme:grid-filter', { detail: { clusterId, group, resource, text } }))
}

export function takeGridPreset(clusterId: string, group: string, resource: string): string | undefined {
  const k = presetKey(clusterId, group, resource)
  const v = presets.get(k)
  presets.delete(k)
  return v
}

/** JSONPath expression in the form the engine keys cells with (`jp:{.spec.replicas}`). */
export function normalizeJP(expr: string): string {
  let e = expr.trim()
  if (!e) return ''
  if (!e.includes('{')) {
    if (!e.startsWith('.') && !e.startsWith('[')) e = '.' + e
    e = `{${e}}`
  }
  return e
}
