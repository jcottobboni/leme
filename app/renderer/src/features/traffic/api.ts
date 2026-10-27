// Client of GET /api/clusters/{id}/traffic (engine/internal/api/traffic_routes.go).
import { call, qs } from '../../api'
import type { TrafficEdge, TrafficEndpoint, TrafficSnapshot } from '../map-webgl/trafficEdges'

export const TRAFFIC_WINDOWS = ['1m', '5m', '15m', '30m', '1h'] as const
export type TrafficWindow = (typeof TRAFFIC_WINDOWS)[number]

export const trafficApi = {
  snapshot: (clusterId: string, namespace?: string, window: TrafficWindow = '5m') =>
    call<TrafficSnapshot>('GET', `/api/clusters/${clusterId}/traffic?` + qs({ namespace, window })).then((s) => ({ ...s, edges: s.edges ?? [] })),
}

const SOURCE_LABEL: Record<string, string> = { caretta: 'Caretta (eBPF)', hubble: 'Hubble (Cilium)' }
export const sourceLabel = (s?: string) => (s ? SOURCE_LABEL[s] ?? s : 'nenhuma')

/** Human volume: bytes/s in binary units, flows/s with two decimals. */
export function fmtRate(rate: number, unit: TrafficEdge['unit']) {
  if (unit === 'flows/s') return `${rate >= 100 ? Math.round(rate) : rate.toFixed(rate >= 10 ? 1 : 2)} flows/s`
  const units = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s']
  let v = rate
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export const isWorkload = (e: TrafficEndpoint) => e.kind !== 'External'

export function endpointLabel(e: TrafficEndpoint) {
  if (e.kind === 'External') return e.name
  return e.namespace ? `${e.namespace}/${e.name}` : e.name
}
