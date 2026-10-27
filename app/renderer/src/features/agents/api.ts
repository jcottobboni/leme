import { call } from '../../api'

export interface AgentContext {
  id: string
  name: string
  prod: boolean
  write: boolean
  prodConfirmed: boolean
}

export interface AgentAccessInfo {
  contexts: AgentContext[]
  enginePath: string
}

export const getAgentAccess = () => call<AgentAccessInfo>('GET', '/api/agents/access')

export const setAgentAccess = (id: string, write: boolean, confirmName?: string) =>
  call<AgentContext>('PUT', `/api/agents/access/${encodeURIComponent(id)}`, { write, confirmName })
