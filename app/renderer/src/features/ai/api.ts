import { call, openSocket } from '../../api'

export type Provider = 'anthropic' | 'openai' | 'ollama'

export interface AIConfig {
  enabled?: boolean
  provider: Provider
  model?: string
  baseURL?: string
  effort?: string
  anonymize?: boolean | null
}

export interface AISettings {
  ai: AIConfig
  keys: Record<Provider, boolean>
  defaultModels: Record<Provider, string>
  anonymize: boolean
  local: boolean
  consented: boolean
}

export interface ChangeRef {
  group: string
  kind: string
  namespace?: string
  name: string
}

export interface Proposal {
  token: string
  ref: ChangeRef
  summary: string
  before: string
  after: string
  dryRun: boolean
  dryRunError?: string
  rbacAllowed: boolean
  expires: string
}

export interface ChatEvent {
  type: 'meta' | 'text' | 'tool' | 'proposal' | 'usage' | 'refusal' | 'consent' | 'error' | 'done'
  text?: string
  name?: string
  detail?: string
  proposal?: Proposal
  input?: number
  output?: number
  provider?: string
  model?: string
  anonymized?: boolean
}

export interface ChatRequest {
  history: { role: 'user' | 'assistant'; text: string }[]
  question: string
  ref?: ChangeRef
  allowLogs: boolean
  logLines: number
}

export interface AuditEntry {
  time: string
  source: string
  action: string
  context?: string
  provider?: string
  model?: string
  object?: string
  result: string
  detail?: string
  bytes?: number
}

interface KeyBridge {
  set(provider: string, baseURL: string, key: string): Promise<{ ok: boolean; stored?: 'keychain' | 'session' }>
  status(): Promise<{ keychain?: boolean }>
}

/** The desktop shell stores keys in the OS keychain; in the browser they go straight to the engine (memory only). */
export const keyBridge = (window as unknown as { leme?: { aiKeys?: KeyBridge } }).leme?.aiKeys

export const aiApi = {
  settings: () => call<AISettings>('GET', '/api/ai/settings'),
  save: (a: AIConfig) => call<AISettings>('PUT', '/api/ai/settings', a),
  consent: () => call<{ ok: boolean }>('POST', '/api/ai/keys', { consent: true }),
  async setKey(provider: Provider, baseURL: string, key: string): Promise<'keychain' | 'session'> {
    if (keyBridge) {
      const r = await keyBridge.set(provider, baseURL, key)
      if (!r.ok) throw new Error('não foi possível guardar a chave')
      return r.stored ?? 'session'
    }
    await call('POST', '/api/ai/keys', { provider, baseURL, key })
    return 'session'
  },
  test: () => call<{ text: string; model: string }>('POST', '/api/ai/test'),
  preview: (clusterId: string, req: ChatRequest) => call<{ system: string; user: string; anonymized: boolean; bytes: number; provider: string; endpoint: string }>('POST', `/api/clusters/${clusterId}/ai/preview`, req),
  apply: (clusterId: string, token: string) => call<{ applied: boolean }>('POST', `/api/clusters/${clusterId}/changes/${token}/apply`),
  audit: () => call<{ entries: AuditEntry[] }>('GET', '/api/audit'),
}

/** Opens a chat turn: sends the request and streams events until "done". Returns a cancel function. */
export function chat(clusterId: string, req: ChatRequest, onEvent: (e: ChatEvent) => void): () => void {
  const ws = openSocket(`/api/clusters/${clusterId}/ai/chat`, {})
  let finished = false
  ws.onopen = () => ws.send(JSON.stringify(req))
  ws.onmessage = (m) => {
    const e = JSON.parse(m.data) as ChatEvent
    if (e.type === 'done') finished = true
    onEvent(e)
  }
  ws.onclose = () => {
    if (!finished) onEvent({ type: 'done' })
  }
  return () => {
    finished = true
    ws.close()
    onEvent({ type: 'done' })
  }
}
