import { useEffect, useState } from 'react'
import { Check, KeyRound, PlugZap } from 'lucide-react'
import { Modal } from '../../components/Dialogs'
import { aiApi, keyBridge, type AIConfig, type AISettings, type AuditEntry, type Provider } from './api'
import './ai.css'

const PROVIDERS: [Provider, string, string][] = [
  ['anthropic', 'Claude (Anthropic)', 'API oficial da Anthropic'],
  ['openai', 'Compatível com OpenAI', 'OpenAI, Azure, OpenRouter, Groq, LM Studio, vLLM'],
  ['ollama', 'Ollama (local)', 'Modelo na sua máquina: nada sai do computador'],
]
const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max']

/** Same rule as the engine: local models need no consent and default to no anonymization. */
export function isLocalEndpoint(c: AIConfig) {
  if (!c.baseURL) return c.provider === 'ollama'
  try {
    return ['localhost', '127.0.0.1', '[::1]'].includes(new URL(c.baseURL).hostname)
  } catch {
    return false
  }
}

/** Copilot settings: provider, model, endpoint, write-only API key, anonymization, audit log. */
export function AISettingsDialog({ onClose }: { onClose: (changed: boolean) => void }) {
  const [s, setS] = useState<AISettings>()
  const [cfg, setCfg] = useState<AIConfig>({ provider: 'anthropic' })
  const [key, setKey] = useState('')
  const [keyMsg, setKeyMsg] = useState('')
  const [test, setTest] = useState<{ ok: boolean; text: string }>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [audit, setAudit] = useState<AuditEntry[]>()
  const [keychain, setKeychain] = useState<boolean>()

  useEffect(() => {
    aiApi.settings().then((r) => (setS(r), setCfg(r.ai))).catch((e) => setError(String(e.message ?? e)))
    keyBridge?.status().then((r) => setKeychain(r.keychain))
  }, [])

  const set = (p: Partial<AIConfig>) => setCfg((c) => ({ ...c, ...p }))
  const save = async (next = cfg) => {
    setBusy(true)
    setError('')
    try {
      const r = await aiApi.save(next)
      setS(r)
      setCfg(r.ai)
      return true
    } catch (e) {
      setError((e as Error).message)
      return false
    } finally {
      setBusy(false)
    }
  }
  const saveKey = async () => {
    setBusy(true)
    try {
      if (!(await save())) return
      const where = await aiApi.setKey(cfg.provider, cfg.baseURL ?? '', key)
      setKey('')
      setKeyMsg(where === 'keychain' ? 'Chave guardada no cofre do sistema.' : 'Chave guardada só nesta sessão (sem cofre do sistema disponível).')
      setS(await aiApi.settings())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const runTest = async () => {
    setTest(undefined)
    if (!(await save())) return
    setBusy(true)
    try {
      const r = await aiApi.test()
      setTest({ ok: true, text: `${r.model}: ${r.text}` })
    } catch (e) {
      setTest({ ok: false, text: (e as Error).message })
    } finally {
      setBusy(false)
    }
  }

  const hasKey = s?.keys?.[cfg.provider]
  const needsURL = cfg.provider !== 'anthropic'
  return (
    <Modal title="Configurações de IA" onClose={() => onClose(true)} width={640}>
      {!s ? <div className="muted">{error || 'Carregando…'}</div> : (
      <div className="ai-settings" data-testid="ai-settings">
        <label className="check ai-enable">
          <input type="checkbox" data-testid="ai-enabled" checked={!!cfg.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          Ativar o Copilot (desligado por padrão; nada é enviado sem isso)
        </label>
        <div className="field">Provedor
          <div className="ai-providers">
            {PROVIDERS.map(([id, label, hint]) => (
              <button key={id} className={cfg.provider === id ? 'on' : ''} data-testid={`ai-provider-${id}`} onClick={() => set({ provider: id, model: '', baseURL: '' })}>
                <b>{label}</b>
                <span className="muted small">{hint}</span>
              </button>
            ))}
          </div>
        </div>
        <div className="row">
          <label className="field grow">Modelo
            <input className="input" data-testid="ai-model" value={cfg.model ?? ''} placeholder={s?.defaultModels?.[cfg.provider]} onChange={(e) => set({ model: e.target.value })} />
          </label>
          {cfg.provider === 'anthropic' && (
            <label className="field">Esforço
              <select value={cfg.effort || 'medium'} onChange={(e) => set({ effort: e.target.value })}>
                {EFFORTS.map((x) => <option key={x}>{x}</option>)}
              </select>
            </label>
          )}
        </div>
        {(needsURL || cfg.baseURL) && (
          <label className="field">Endereço da API {cfg.provider === 'ollama' && <span className="muted small">(padrão http://localhost:11434/v1)</span>}
            <input className="input" data-testid="ai-baseurl" value={cfg.baseURL ?? ''} placeholder={cfg.provider === 'openai' ? 'https://api.openai.com/v1' : cfg.provider === 'ollama' ? 'http://localhost:11434/v1' : ''}
              onChange={(e) => set({ baseURL: e.target.value })} />
          </label>
        )}
        {cfg.provider !== 'ollama' && (
          <div className="field">Chave de API
            <div className="row">
              <input className="input grow" type="password" autoComplete="off" data-testid="ai-key" value={key} placeholder={hasKey ? '•••••••• configurada' : 'cole a chave aqui'} onChange={(e) => setKey(e.target.value)} />
              <button data-testid="ai-key-save" disabled={busy || !key.trim()} onClick={saveKey}><KeyRound size={14} /> Salvar chave</button>
            </div>
            <span className="muted small">
              {hasKey ? <><Check size={12} /> configurada · </> : null}
              {keyBridge ? (keychain === false ? 'Sem cofre do sistema: a chave valerá só nesta sessão.' : 'Guardada criptografada pelo cofre do sistema; nunca volta para a tela.') : 'Modo navegador: a chave fica só na memória do engine.'}
              {' '}A chave vale só para este endereço.
            </span>
            {keyMsg && <span className="s-ok small">{keyMsg}</span>}
          </div>
        )}
        <label className="check">
          <input type="checkbox" data-testid="ai-anonymize" checked={cfg.anonymize ?? !isLocalEndpoint(cfg)} onChange={(e) => set({ anonymize: e.target.checked })} />
          Anonimizar nomes, hosts, IPs e e-mails antes de enviar (restaurados na resposta)
        </label>
        <p className="muted small ai-note">
          O que é enviado: YAML higienizado do objeto (sem valores de Secret nem de variáveis de ambiente), eventos e o diagnóstico do Leme. Logs só quando você permitir na conversa.
          A IA só lê e propõe; aplicar uma mudança exige sua aprovação e escrita liberada no cluster.
        </p>
        {test && <div className={test.ok ? 'banner ai-ok' : 'banner error'} data-testid="ai-test-result">{test.text}</div>}
        {error && <div className="banner error">{error}</div>}
        <details className="ai-audit" onToggle={(e) => (e.currentTarget.open && !audit ? aiApi.audit().then((r) => setAudit(r.entries)) : undefined)}>
          <summary>Registro de auditoria (IA e agentes)</summary>
          {audit && !audit.length && <div className="muted small">Nada registrado ainda.</div>}
          {audit && audit.length > 0 && (
            <table className="table small">
              <thead><tr><th>Quando</th><th>Origem</th><th>Ação</th><th>Objeto</th><th>Resultado</th></tr></thead>
              <tbody>{audit.map((a, i) => (
                <tr key={i} title={a.detail}><td>{new Date(a.time).toLocaleString()}</td><td>{a.source}{a.model ? ` · ${a.model}` : ''}</td><td>{a.action}</td><td className="ellipsis">{a.object ?? a.context}</td><td>{a.result}</td></tr>
              ))}</tbody>
            </table>
          )}
        </details>
      </div>
      )}
      <div className="modal-actions">
        <button onClick={runTest} disabled={busy || !s} data-testid="ai-test"><PlugZap size={14} /> Testar conexão</button>
        <span className="spacer" />
        <button onClick={() => onClose(false)}>Fechar</button>
        <button className="primary" disabled={busy || !s} data-testid="ai-save" onClick={async () => (await save()) && onClose(true)}>Salvar</button>
      </div>
    </Modal>
  )
}
