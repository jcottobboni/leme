import { behaviorKind } from './nav'

/**
 * What the interface offers for each built-in kind. Keyed by behaviorKind(group, kind), so a CRD that
 * reuses a built-in name (Longhorn's Node, Knative's Route) gets none of it.
 */
export interface KindBehavior {
  /** Prometheus charts (the Métricas tab). */
  metrics?: boolean
  /** Security rules and image scan (the Segurança tab). */
  security?: boolean
  /** Owns pods through a template: replicas card and pod list in the overview. */
  workload?: boolean
  scalable?: boolean
  restartable?: boolean
  /** Rollout history (the Revisões tab). */
  revisions?: boolean
  /** Logs and shell of the pod itself. */
  pod?: boolean
  portForward?: boolean
  /** Extra overview card. */
  details?: 'pod' | 'route' | 'service' | 'configmap' | 'secret'
  /** OpenShift actions. */
  rolloutLatest?: boolean
  startBuild?: boolean
  buildLogs?: boolean
}

export const KIND_BEHAVIOR: Record<string, KindBehavior> = {
  Pod: { metrics: true, security: true, pod: true, portForward: true, details: 'pod' },
  Deployment: { metrics: true, security: true, workload: true, scalable: true, restartable: true, revisions: true },
  StatefulSet: { metrics: true, security: true, workload: true, scalable: true, restartable: true },
  DaemonSet: { metrics: true, security: true, workload: true, restartable: true },
  ReplicaSet: { security: true, workload: true, scalable: true },
  ReplicationController: { security: true, workload: true, scalable: true },
  Job: { security: true, workload: true },
  CronJob: { security: true },
  DeploymentConfig: { metrics: true, security: true, workload: true, scalable: true, rolloutLatest: true },
  BuildConfig: { startBuild: true },
  Build: { buildLogs: true },
  Node: { metrics: true },
  Service: { portForward: true, details: 'service' },
  Route: { details: 'route' },
  ConfigMap: { details: 'configmap' },
  Secret: { details: 'secret' },
}

const NONE: KindBehavior = {}

export function behaviorOf(group: string, kind?: string): KindBehavior {
  return (kind && KIND_BEHAVIOR[behaviorKind(group, kind)]) || NONE
}
