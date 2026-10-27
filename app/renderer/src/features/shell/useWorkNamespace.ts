import { useMemo, useRef } from 'react'
import { useApp } from '../../store'
import { useWatch } from '../../useWatch'

const SYSTEM = /^(kube-|openshift|default$)/

/**
 * Namespace for screens that always work on one namespace (Topologia, NetworkPolicy, RBAC).
 * The global "ns:" wins; then the panel's own initial namespace; then the context's namespace
 * when it is not "default"; otherwise the namespace with the most workloads (user namespaces first),
 * so the screen never opens on an empty "default". Undefined while that count is loading.
 */
export function useWorkNamespace(clusterId: string, initial?: string): string | undefined {
  const selected = useApp((s) => s.namespace[clusterId] ?? '')
  const ctx = useApp((s) => s.connected[clusterId]?.defaultNamespace ?? '')
  const fixed = selected || initial || (ctx && ctx !== 'default' ? ctx : '')
  const need = !fixed
  const deps = useWatch(clusterId, 'apps', 'deployments', '', need)
  const sts = useWatch(clusterId, 'apps', 'statefulsets', '', need)
  const ds = useWatch(clusterId, 'apps', 'daemonsets', '', need)
  const busiest = useMemo(() => {
    if (!need) return ''
    const count = new Map<string, number>()
    for (const r of [...deps.rows, ...sts.rows, ...ds.rows]) if (r.namespace) count.set(r.namespace, (count.get(r.namespace) ?? 0) + 1)
    const ranked = [...count].sort((a, b) => Number(SYSTEM.test(a[0])) - Number(SYSTEM.test(b[0])) || b[1] - a[1] || a[0].localeCompare(b[0]))
    return ranked[0]?.[0] ?? ''
  }, [need, deps.rows, sts.rows, ds.rows])
  // The first pick sticks: later watch deltas must not move the screen to another namespace.
  const pick = useRef<{ cluster: string; ns: string }>(undefined)
  if (fixed) return fixed
  if (pick.current?.cluster === clusterId) return pick.current.ns
  if ([deps, sts, ds].some((w) => w.loading && !w.error)) return undefined
  pick.current = { cluster: clusterId, ns: busiest || ctx || 'default' }
  return pick.current.ns
}
