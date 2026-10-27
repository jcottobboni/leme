import { call, qs } from '../../api'

export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info'

export interface DiagRef {
  group: string
  kind: string
  namespace?: string
  name: string
}

export interface Diagnosis {
  id: string
  severity: Severity
  object: DiagRef
  title: string
  explanation: string
  evidence: string[]
  suggestion: string
  fixable: boolean
}

export const analyzeApi = {
  namespace: (clusterId: string, namespace: string) =>
    call<{ findings: Diagnosis[] }>('GET', `/api/clusters/${encodeURIComponent(clusterId)}/diagnose?${qs({ namespace })}`).then((r) => r.findings),
  object: (clusterId: string, ref: DiagRef) =>
    call<{ findings: Diagnosis[] }>('GET', `/api/clusters/${encodeURIComponent(clusterId)}/diagnose?${qs({ ...ref })}`).then((r) => r.findings),
}

export const SEVERITIES: Severity[] = ['critical', 'high', 'medium', 'low', 'info']
export const SEV_LABEL: Record<Severity, string> = { critical: 'Crítico', high: 'Alto', medium: 'Médio', low: 'Baixo', info: 'Info' }
