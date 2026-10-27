import { call } from '../../api'

export interface GrafanaTarget {
  kind: 'grafana' | 'prometheus'
  namespace: string
  service: string
  port: number
  scheme: 'http' | 'https'
  score: number
  /** Reference only: the engine never reads the Secret's values here. */
  adminSecret?: { namespace: string; name: string }
  /** Active port-forward of this target, when one is running. */
  forward?: { id: string; url: string }
}

const base = (clusterId: string) => `/api/clusters/${encodeURIComponent(clusterId)}/grafana`

export const grafanaApi = {
  targets: (clusterId: string) => call<{ targets: GrafanaTarget[] }>('GET', `${base(clusterId)}/targets`).then((r) => r.targets ?? []),
  open: (clusterId: string, t: Pick<GrafanaTarget, 'namespace' | 'service' | 'port'>) =>
    call<{ url: string; forwardId: string; reused: boolean }>('POST', `${base(clusterId)}/open`, { namespace: t.namespace, service: t.service, port: t.port }),
}

export const KIND_LABEL: Record<GrafanaTarget['kind'], string> = { grafana: 'Grafana', prometheus: 'Prometheus' }
