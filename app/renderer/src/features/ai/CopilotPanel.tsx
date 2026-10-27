import { useEffect, useRef, useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { Check, Download, Eye, FileCode2, FileDiff, Send, Settings2, ShieldAlert, Sparkles, Square, Wrench, X } from 'lucide-react'
import { openMenu } from '../shell/menu'
import { clusterName, openPanel, useApp, type OpenOptions } from '../../store'
import { ClusterGate } from '../common'
import { Modal } from '../../components/Dialogs'
import { YamlDiff } from '../../components/Monaco'
import { AISettingsDialog } from './AISettingsDialog'
import { Markdown } from './Markdown'
import { aiApi, chat, type AISettings, type ChatEvent, type ChatRequest, type Proposal } from './api'
import './ai.css'

export interface CopilotRef {
  group: string
  kind: string
  namespace?: string
  name: string
}

export function openCopilot(clusterId: string, ref?: CopilotRef, opts?: OpenOptions) {
  openPanel(`copilot:${clusterId}`, 'copilot', 'Copilot', { clusterId, ref }, opts)
  // Re-opening an existing panel for another object: tell it which object is now in focus.
  if (ref) window.dispatchEvent(new CustomEvent('leme:copilot-ref', { detail: { clusterId, ref } }))
}

type Item =
  | { kind: 'user'; text: string }
  | { kind: 'assistant'; text: string }
  | { kind: 'tool'; name: string; detail: string }
  | { kind: 'proposal'; proposal: Proposal; state: 'pending' | 'applying' | 'applied' | 'discarded'; error?: string }
  | { kind: 'alert'; level: 'warn' | 'error'; text: string; consent?: boolean }

const TOOL_LABEL: Record<string, string> = {
  get_object: 'leu o objeto', list_objects: 'listou objetos', get_events: 'leu eventos', get_logs: 'leu logs',
  diagnose: 'rodou o diagnóstico', can_i: 'checou permissão', propose_change: 'propôs uma mudança',
}

const SUGGESTIONS_OBJECT = ['Por que este objeto está com problema?', 'Como corrigir o problema principal?', 'Explique a configuração deste objeto']
const SUGGESTIONS_CLUSTER = ['Resuma os problemas do cluster', 'O que devo olhar primeiro?', 'Algum risco de segurança evidente?']

export function CopilotPanel({ params }: IDockviewPanelProps<{ clusterId: string; ref?: CopilotRef }>) {
  return (
    <ClusterGate clusterId={params.clusterId}>
      <Copilot clusterId={params.clusterId} initialRef={params.ref} />
    </ClusterGate>
  )
}

function Copilot({ clusterId, initialRef }: { clusterId: string; initialRef?: CopilotRef }) {
  const [ref, setRef] = useState<CopilotRef | undefined>(initialRef)
  const [items, setItems] = useState<Item[]>([])
  const [history, setHistory] = useState<ChatRequest['history']>([])
  // The engine keeps this conversation's anonymization aliases, so names learned in earlier turns stay hidden.
  const [conversation] = useState(() => crypto.randomUUID())
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [allowLogs, setAllowLogs] = useState(false)
  const [logLines, setLogLines] = useState(100)
  const [meta, setMeta] = useState<{ provider?: string; model?: string; anonymized?: boolean; tokens?: number }>({})
  const [settings, setSettings] = useState<AISettings>()
  const [showSettings, setShowSettings] = useState(false)
  const [preview, setPreview] = useState<{ system: string; user: string; anonymized: boolean; bytes: number }>()
  const cancel = useRef<() => void>(undefined)
  const body = useRef<HTMLDivElement>(null)
  const readOnly = useApp((s) => s.connected[clusterId]?.readOnly ?? true)
  const lastQuestion = useRef('')

  const loadSettings = () => aiApi.settings().then(setSettings).catch(() => undefined)
  useEffect(() => {
    loadSettings()
    const onRef = (e: Event) => {
      const d = (e as CustomEvent).detail as { clusterId: string; ref: CopilotRef }
      if (d.clusterId === clusterId) setRef(d.ref)
    }
    window.addEventListener('leme:copilot-ref', onRef)
    return () => {
      window.removeEventListener('leme:copilot-ref', onRef)
      cancel.current?.()
    }
  }, [clusterId])
  useEffect(() => {
    body.current?.scrollTo({ top: body.current.scrollHeight })
  }, [items])

  // Local endpoints (Ollama, LM Studio, vLLM on localhost) need no key.
  const ready = settings?.ai.enabled && (settings.keys[settings.ai.provider] || settings.local)
  const request = (question: string): ChatRequest => ({ conversation, history, question, ref, allowLogs, logLines })

  const send = (question: string) => {
    const q = question.trim()
    if (!q || busy) return
    lastQuestion.current = q
    setInput('')
    setBusy(true)
    setItems((it) => [...it, { kind: 'user', text: q }])
    let answer = ''
    let started = false
    const onEvent = (e: ChatEvent) => {
      switch (e.type) {
        case 'meta':
          setMeta((m) => ({ ...m, provider: e.provider, model: e.model, anonymized: e.anonymized }))
          break
        case 'text':
          answer += e.text ?? ''
          setItems((it) => {
            if (started && it[it.length - 1]?.kind === 'assistant') return [...it.slice(0, -1), { kind: 'assistant', text: (it[it.length - 1] as { text: string }).text + (e.text ?? '') }]
            started = true
            return [...it, { kind: 'assistant', text: e.text ?? '' }]
          })
          break
        case 'tool':
          started = false
          setItems((it) => [...it, { kind: 'tool', name: e.name ?? '', detail: e.detail ?? '' }])
          break
        case 'proposal':
          started = false
          if (e.proposal) setItems((it) => [...it, { kind: 'proposal', proposal: e.proposal!, state: 'pending' }])
          break
        case 'usage':
          setMeta((m) => ({ ...m, tokens: (e.input ?? 0) + (e.output ?? 0) }))
          break
        case 'refusal':
          setItems((it) => [...it, { kind: 'alert', level: 'warn', text: `O modelo recusou a solicitação${e.text ? ` (categoria: ${e.text})` : ''}.` }])
          break
        case 'consent':
          setItems((it) => [...it, { kind: 'alert', level: 'warn', consent: true, text: '' }])
          break
        case 'error':
          setItems((it) => [...it, { kind: 'alert', level: 'error', text: e.text ?? 'erro desconhecido' }])
          break
        case 'done':
          setBusy(false)
          cancel.current = undefined
          if (answer) setHistory((h) => [...h, { role: 'user' as const, text: q }, { role: 'assistant' as const, text: answer }].slice(-20))
          break
      }
    }
    cancel.current = chat(clusterId, request(q), onEvent)
  }

  const consent = async () => {
    await aiApi.consent()
    await loadSettings()
    setItems((it) => it.filter((x) => !(x.kind === 'alert' && x.consent)).slice(0, -1))
    send(lastQuestion.current)
  }

  const apply = async (idx: number, p: Proposal) => {
    setItems((it) => it.map((x, i) => (i === idx && x.kind === 'proposal' ? { ...x, state: 'applying', error: undefined } : x)))
    try {
      await aiApi.apply(clusterId, p.token)
      setItems((it) => it.map((x, i) => (i === idx && x.kind === 'proposal' ? { ...x, state: 'applied' } : x)))
      useApp.getState().toast('ok', `Mudança aplicada em ${p.ref.kind}/${p.ref.name}`)
    } catch (e) {
      setItems((it) => it.map((x, i) => (i === idx && x.kind === 'proposal' ? { ...x, state: 'pending', error: (e as Error).message } : x)))
    }
  }

  const showPreview = async () => {
    const q = input.trim() || '(sua pergunta)'
    setPreview(await aiApi.preview(clusterId, request(q)))
  }

  return (
    <div className="ai-panel" data-testid="copilot">
      <div className="ai-head">
        <span className="ai-title"><Sparkles size={15} /> Copilot · {clusterName(clusterId)}</span>
        {ref ? (
          <span className="ai-ref" title="Objeto em foco: vai como contexto da conversa" data-testid="copilot-ref">
            <span>{ref.kind} {ref.namespace ? `${ref.namespace}/` : ''}{ref.name}</span>
            <button title="Conversar sobre o cluster inteiro" aria-label="Remover o objeto em foco" onClick={() => setRef(undefined)}><X size={12} /></button>
          </span>
        ) : <span className="ai-meta">contexto: cluster</span>}
        <span className="spacer" />
        {meta.model && <span className="ai-meta" data-testid="copilot-meta">{meta.model}{meta.anonymized ? ' · anonimizado' : ''}{meta.tokens ? ` · ${meta.tokens.toLocaleString()} tokens` : ''}</span>}
        <button title="Ver exatamente o que será enviado ao provedor" onClick={showPreview} data-testid="copilot-preview"><Eye size={14} /> Prévia do envio</button>
        <button title="Provedor, modelo e privacidade" onClick={() => setShowSettings(true)} data-testid="copilot-settings"><Settings2 size={14} /> Ajustes de IA</button>
      </div>
      <div className="ai-body" ref={body}>
        {!items.length && (
          <div className="ai-empty">
            <Sparkles size={28} />
            {ready ? (
              <>
                <div>Pergunte sobre {ref ? `${ref.kind} ${ref.name}` : 'o cluster'}. O Copilot lê, diagnostica e pode propor uma correção; aplicar sempre depende de você.</div>
                <div className="ai-suggestions">
                  {(ref ? SUGGESTIONS_OBJECT : SUGGESTIONS_CLUSTER).map((s) => <button key={s} onClick={() => send(s)}>{s}</button>)}
                </div>
              </>
            ) : (
              <>
                <div>O Copilot está desligado. Escolha um provedor (Claude, compatível com OpenAI ou Ollama local) e ative.</div>
                <button className="primary" onClick={() => setShowSettings(true)} data-testid="copilot-setup">Configurar IA</button>
              </>
            )}
          </div>
        )}
        {items.map((it, i) => {
          switch (it.kind) {
            case 'user':
              return <div key={i} className="ai-msg user">{it.text}</div>
            case 'assistant':
              return <div key={i} className="ai-msg assistant" data-testid="copilot-answer"><Markdown text={it.text} /></div>
            case 'tool':
              return <div key={i} className="ai-tool" title={it.detail}><Wrench size={12} /> {TOOL_LABEL[it.name] ?? it.name} <code>{it.detail}</code></div>
            case 'alert':
              return it.consent ? (
                <div key={i} className="ai-alert warn" data-testid="copilot-consent">
                  <b>Enviar dados do cluster para {settings?.ai.provider === 'anthropic' ? 'a Anthropic (Claude)' : settings?.ai.baseURL || 'o provedor'}?</b>
                  <div className="small">Vão o YAML higienizado do objeto (sem valores de Secret nem de variáveis de ambiente), eventos e o diagnóstico do Leme; logs só se você marcar. Nomes {settings?.anonymize ? 'são anonimizados' : <b>não</b>} antes do envio. Este aceite vale para este provedor.</div>
                  <div className="row">
                    <button className="primary" onClick={consent} data-testid="copilot-consent-ok">Concordo, enviar</button>
                    <button onClick={() => setItems((x) => x.filter((_, j) => j !== i))}>Cancelar</button>
                  </div>
                </div>
              ) : <div key={i} className={`ai-alert ${it.level}`}>{it.text}</div>
            case 'proposal':
              return <ProposalCard key={i} p={it.proposal} state={it.state} error={it.error} readOnly={readOnly}
                onApply={() => apply(i, it.proposal)} onDiscard={() => setItems((x) => x.map((y, j) => (j === i && y.kind === 'proposal' ? { ...y, state: 'discarded' } : y)))} />
          }
        })}
      </div>
      <div className="ai-input">
        <textarea data-testid="copilot-input" value={input} disabled={!ready} placeholder={ready ? 'Pergunte algo… (Enter envia, Shift+Enter quebra linha)' : 'Configure a IA para conversar'}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              send(input)
            }
          }} />
        <div className="ai-input-bar">
          <label className="check" title="Deixa o Copilot ler as últimas linhas de log (com credenciais óbvias mascaradas)">
            <input type="checkbox" checked={allowLogs} onChange={(e) => setAllowLogs(e.target.checked)} data-testid="copilot-logs" /> Permitir logs
          </label>
          {allowLogs && <label className="muted">linhas <input type="number" min={10} max={500} value={logLines} onChange={(e) => setLogLines(Math.min(500, Math.max(10, +e.target.value || 100)))} /></label>}
          <span className="spacer" />
          {busy ? (
            <button onClick={() => cancel.current?.()} data-testid="copilot-stop"><Square size={13} /> Parar</button>
          ) : (
            <button className="primary" disabled={!ready || !input.trim()} onClick={() => send(input)} data-testid="copilot-send"><Send size={13} /> Enviar</button>
          )}
        </div>
      </div>
      {showSettings && <AISettingsDialog onClose={() => (setShowSettings(false), loadSettings())} />}
      {preview && (
        <Modal title="O que será enviado ao provedor" onClose={() => setPreview(undefined)} width={860}>
          <div className="ai-preview" data-testid="copilot-preview-dialog">
            <div className="muted small">{preview.bytes.toLocaleString()} bytes · {preview.anonymized ? 'nomes anonimizados' : 'sem anonimização'} · valores de Secret e de variáveis de ambiente nunca são enviados</div>
            <pre>{preview.user}</pre>
            <details><summary className="muted small">Instruções do sistema</summary><pre>{preview.system}</pre></details>
          </div>
          <div className="modal-actions"><button onClick={() => setPreview(undefined)}>Fechar</button></div>
        </Modal>
      )}
    </div>
  )
}

function saveText(name: string, text: string) {
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type: 'application/yaml' }))
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(a.href), 0)
}

/** "Exportar proposta" (M7 RF07): the final YAML and the merge patch, for teams that apply through GitOps.
 * The content is what the engine sent with the proposal; nothing is fetched again and masked values stay masked. */
export function exportProposal(p: Proposal, what: 'yaml' | 'patch') {
  const r = p.ref
  const base = [r.kind.toLowerCase(), r.namespace, r.name].filter(Boolean).join('-').replace(/[^\w.-]+/g, '_')
  const target = `${r.kind.toLowerCase()}${r.group ? `.${r.group}` : ''} ${r.name}${r.namespace ? ` -n ${r.namespace}` : ''}`
  const head = [`# Proposta do Leme: ${p.summary || `${r.kind} ${r.name}`}`, `# Objeto: ${r.kind} ${r.namespace ? `${r.namespace}/` : ''}${r.name}`]
  if (what === 'yaml') {
    saveText(`${base}.yaml`, [...head, '# YAML final, sem status nem campos que o servidor controla (uid, resourceVersion…).', p.manifest ?? p.after].join('\n'))
  } else {
    saveText(`${base}.patch.yaml`, [...head, '# JSON merge patch (RFC 7386). Para aplicar sem o Leme:', `#   kubectl patch ${target} --type merge --patch-file ${base}.patch.yaml`, p.patch ?? ''].join('\n'))
  }
}

function ProposalCard({ p, state, error, readOnly, onApply, onDiscard }: { p: Proposal; state: string; error?: string; readOnly: boolean; onApply: () => void; onDiscard: () => void }) {
  const [ack, setAck] = useState(false)
  const risky = (p.risks?.length ?? 0) > 0
  const blocked = readOnly ? 'O cluster está em somente leitura: libere a escrita para aplicar.' : !p.rbacAllowed ? 'Você não tem permissão de update neste objeto.' : p.dryRunError ? 'O dry-run falhou.' : risky && !ack ? 'Marque que revisou os riscos.' : ''
  return (
    <div className="ai-proposal" data-testid="copilot-proposal">
      <div className="ai-proposal-head">
        <Wrench size={14} />
        <b>{p.ref.kind} {p.ref.namespace ? `${p.ref.namespace}/` : ''}{p.ref.name}</b>
        <span className="muted">{p.summary}</span>
        <span className="spacer" />
        {p.dryRun ? <span className="ai-pill ok">dry-run ok</span> : p.dryRunError ? <span className="ai-pill bad" title={p.dryRunError}>dry-run falhou</span> : <span className="ai-pill warn">sem dry-run (somente leitura)</span>}
        <span className={`ai-pill ${p.rbacAllowed ? 'ok' : 'bad'}`}>{p.rbacAllowed ? 'permissão ok' : 'sem permissão'}</span>
      </div>
      {risky && (
        <div className="ai-risks" data-testid="copilot-risks">
          <b><ShieldAlert size={14} /> Esta mudança amplia privilégios ou exposição</b>
          <ul>{p.risks!.map((r) => <li key={r}>{r}</li>)}</ul>
          {state !== 'applied' && state !== 'discarded' && (
            <label><input type="checkbox" checked={ack} onChange={(e) => setAck(e.target.checked)} data-testid="copilot-risks-ack" /> Revisei os riscos e quero aplicar mesmo assim</label>
          )}
        </div>
      )}
      <div className="ai-proposal-diff"><YamlDiff original={p.before} modified={p.after} /></div>
      <div className="ai-proposal-foot">
        {state === 'applied' ? <span className="s-ok"><Check size={14} /> Aplicada</span> : state === 'discarded' ? <span className="muted">Descartada</span> : (
          <>
            <button className="primary" disabled={!!blocked || state === 'applying'} title={blocked || 'Aplica com o mesmo resourceVersion da proposta, após novo dry-run'} onClick={onApply} data-testid="copilot-apply">
              {state === 'applying' ? 'Aplicando…' : 'Aplicar'}
            </button>
            <button onClick={onDiscard}>Descartar</button>
            {blocked && <span className="muted small">{blocked}</span>}
          </>
        )}
        <span className="spacer" />
        <button title="Baixar o YAML final ou o patch, para aplicar via GitOps" data-testid="copilot-export"
          onClick={(e) => openMenu(e, [
            { label: 'YAML final (.yaml)', icon: FileCode2, onClick: () => exportProposal(p, 'yaml') },
            { label: 'Patch merge (.patch.yaml)', icon: FileDiff, disabled: !p.patch, onClick: () => exportProposal(p, 'patch') },
          ])}><Download size={13} /> Exportar proposta</button>
        {error && <span className="s-warn small">{error}</span>}
      </div>
    </div>
  )
}
