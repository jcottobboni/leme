// Synthetic ClusterMap (the JSON of GET /api/clusters/{id}/map) for the map-webgl e2e: `n` workloads over a few
// namespaces, grouped in apps, with known names to search for. Deterministic.

export interface Wl { group: string; kind: string; name: string; health: string; ready?: string; status?: string; pods: { total: number; ok: number; warn: number; error: number } }

/** Workloads that always exist (namespace, app, kind, name, health). */
export const NEEDLE = { ns: 'mgl-shop', app: 'needle', group: 'apps', kind: 'Deployment', name: 'needle-api' }
export const PEER = { ns: 'mgl-shop', app: 'needle', group: 'apps', kind: 'Deployment', name: 'needle-db' }
export const OTHER = { ns: 'mgl-pay', app: 'ledger', group: 'apps', kind: 'StatefulSet', name: 'ledger-core' }

const KINDS: [string, string][] = [['apps', 'Deployment'], ['apps', 'StatefulSet'], ['apps', 'DaemonSet'], ['', 'Pod'], ['batch', 'CronJob']]

export function syntheticMap(n: number, nsCount = 16) {
  const names = Array.from({ length: nsCount }, (_, i) => (i === 0 ? NEEDLE.ns : i === 1 ? OTHER.ns : `mgl-ns-${String(i).padStart(2, '0')}`))
  const nss = names.map((name) => ({ name, status: 'Active', pods: { total: 0, ok: 0, warn: 0, error: 0 }, apps: [] as { name: string; workloads: Wl[] }[], services: 3, ingresses: 1, pvcs: 0 }))
  const add = (nsIdx: number, app: string, w: Wl) => {
    const ns = nss[nsIdx]
    let a = ns.apps.find((x) => x.name === app)
    if (!a) ns.apps.push((a = { name: app, workloads: [] }))
    a.workloads.push(w)
    ns.pods.total += w.pods.total
    ns.pods.ok += w.pods.ok
    ns.pods.warn += w.pods.warn
    ns.pods.error += w.pods.error
  }
  const wl = (group: string, kind: string, name: string, health = 'ok'): Wl => ({
    group, kind, name, health, ready: health === 'error' ? '0/1' : '1/1',
    pods: { total: 1, ok: health === 'ok' ? 1 : 0, warn: health === 'warn' ? 1 : 0, error: health === 'error' ? 1 : 0 },
  })
  add(0, NEEDLE.app, wl(NEEDLE.group, NEEDLE.kind, NEEDLE.name))
  add(0, PEER.app, wl(PEER.group, PEER.kind, PEER.name))
  add(1, OTHER.app, wl(OTHER.group, OTHER.kind, OTHER.name))
  let problems = 0
  for (let i = 3; i < n; i++) {
    const nsIdx = i % nsCount
    const [group, kind] = KINDS[i % KINDS.length]
    // Apps of ~12 workloads; one in 97 workloads is broken, one in 89 needs attention.
    const app = `app-${nsIdx}-${Math.floor(i / (nsCount * 12))}`
    const health = i % 97 === 0 ? 'error' : i % 89 === 0 ? 'warn' : 'ok'
    if (health !== 'ok') problems++
    add(nsIdx, app, wl(group, kind, `wl-${i}`, health))
  }
  for (const ns of nss) ns.apps.sort((a, b) => b.workloads.length - a.workloads.length || a.name.localeCompare(b.name))
  return { map: { scope: 'cluster', namespaces: nss, compact: n > 1500 }, problems }
}

/** A traffic snapshot (contract of features/map-webgl/trafficEdges.ts) between the known workloads. */
export function syntheticTraffic() {
  const ep = (x: { ns: string; group: string; kind: string; name: string }) => ({ kind: x.kind, group: x.group, namespace: x.ns, name: x.name })
  return {
    available: true,
    source: 'caretta',
    window: '5m',
    edges: [
      { from: ep(NEEDLE), to: ep(PEER), port: 5432, protocol: 'TCP', rate: 250_000, unit: 'bytes/s', source: 'caretta' },
      { from: ep(NEEDLE), to: ep(OTHER), port: 8080, protocol: 'TCP', rate: 1_200, unit: 'bytes/s', source: 'caretta' },
      // Group omitted: still resolves to the tile by kind/namespace/name.
      { from: { kind: PEER.kind, namespace: PEER.ns, name: PEER.name }, to: ep(OTHER), rate: 40, unit: 'bytes/s', source: 'caretta' },
      // Outside the map (external endpoint): dropped and counted.
      { from: ep(NEEDLE), to: { kind: 'External', name: '203.0.113.7' }, port: 443, rate: 900, unit: 'bytes/s', source: 'caretta' },
    ],
  }
}
