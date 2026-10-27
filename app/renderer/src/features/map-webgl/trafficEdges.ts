// Shared contract between the traffic feature (producer) and the map/topology renderers (consumers).
// Owned by map-webgl (M5). Frozen: the traffic agent registers a provider here and never edits the
// map files; the map reads edges through useTrafficEdges and never imports features/traffic.
import { useEffect, useRef, useState } from 'react'

/** One side of an observed connection, at workload level (the same granularity as the map tiles). */
export interface TrafficEndpoint {
  /** Top-level controller kind (Deployment, StatefulSet, DaemonSet, DeploymentConfig, Job, CronJob, ReplicaSet, Pod), "Node" or "External". */
  kind: string
  group?: string
  /** Absent for Node and External. */
  namespace?: string
  /** Workload name; for External, the IP or host name. */
  name: string
}

export type TrafficSource = 'caretta' | 'hubble'

/** A directed, aggregated connection from client to server over the observation window. */
export interface TrafficEdge {
  from: TrafficEndpoint
  to: TrafficEndpoint
  port?: number
  protocol?: string
  /** Volume per second over the window, in `unit`. */
  rate: number
  unit: 'bytes/s' | 'flows/s'
  source: TrafficSource
}

export interface TrafficSnapshot {
  /** False when no source is present: `hint` explains how to get one. */
  available: boolean
  source?: TrafficSource
  edges: TrafficEdge[]
  /** PromQL range used, e.g. "5m". */
  window?: string
  hint?: string
  errors?: Record<string, string>
}

export type TrafficProvider = (clusterId: string, namespace?: string) => Promise<TrafficSnapshot>

export const NO_TRAFFIC: TrafficSnapshot = { available: false, edges: [] }

let provider: TrafficProvider | undefined

/** Called once by features/traffic at module load. */
export function setTrafficProvider(p: TrafficProvider) {
  provider = p
}

/** Stable key of an endpoint for lookups (map tiles: group/kind/namespace/name). */
export function endpointKey(e: TrafficEndpoint) {
  return `${e.group ?? ''}/${e.kind}/${e.namespace ?? ''}/${e.name}`
}

/** Node id of the endpoint in the topology graph (engine/internal/graph: kind/namespace/name). */
export function topologyId(e: TrafficEndpoint) {
  return `${e.kind}/${e.namespace ?? ''}/${e.name}`
}

/** Polls the registered provider while enabled. Without a provider or when disabled: NO_TRAFFIC. */
export function useTrafficEdges(clusterId: string, namespace: string | undefined, enabled: boolean, every = 30_000) {
  const [data, setData] = useState<TrafficSnapshot>(NO_TRAFFIC)
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(false)
  const seq = useRef(0)
  useEffect(() => {
    const my = ++seq.current
    if (!enabled || !provider) {
      setData(NO_TRAFFIC)
      setError(undefined)
      setLoading(false)
      return
    }
    const p = provider
    const run = async () => {
      setLoading(true)
      try {
        const d = await p(clusterId, namespace || undefined)
        if (my === seq.current) {
          setData(d)
          setError(undefined)
        }
      } catch (e) {
        if (my === seq.current) setError((e as Error).message)
      } finally {
        if (my === seq.current) setLoading(false)
      }
    }
    run()
    const t = setInterval(run, every)
    return () => clearInterval(t)
  }, [clusterId, namespace, enabled, every])
  return { data, error, loading }
}
