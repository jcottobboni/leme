import { openPanel } from '../../store'

/** What the aggregated logs follow: a workload (its spec.selector, resolved by the engine) or a label selector. */
export interface MultiLogsParams {
  clusterId: string
  namespace: string
  /** Label selector typed by the user (takes precedence over the workload). */
  selector?: string
  group?: string
  resource?: string
  kind?: string
  name?: string
}

// Small on purpose: grids and the object panel import it, the panel itself is a lazy chunk.
export function openMultiLogs(p: MultiLogsParams) {
  const what = p.name ? `${p.resource}/${p.name}` : `sel:${p.selector ?? ''}`
  openPanel(`mlogs:${p.clusterId}:${p.namespace}/${what}`, 'multilogs', `logs ${p.name ?? p.selector ?? 'por rótulo'}`, { ...p })
}
