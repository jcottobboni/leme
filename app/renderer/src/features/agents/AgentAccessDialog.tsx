import { useEffect, useState } from 'react'
import type { ClusterInfo } from '../../api'
import { Modal } from '../../components/Dialogs'
import { getAgentAccess, setAgentAccess, type AgentContext } from './api'
import './agents.css'

/** Per-context write access for MCP agents (M4, group G2). Reads are always allowed. */
export function AgentAccessDialog({ c, onClose }: { c: ClusterInfo; onClose: () => void }) {
  const [ctx, setCtx] = useState<AgentContext>()
  const [enginePath, setEnginePath] = useState('')
  const [write, setWrite] = useState(false)
  const [typed, setTyped] = useState('')
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    getAgentAccess()
      .then((r) => {
        const found = r.contexts.find((x) => x.id === c.id)
        setCtx(found)
        setWrite(found?.write ?? false)
        setEnginePath(r.enginePath)
      })
      .catch((e) => setError(String(e.message ?? e)))
  }, [c.id])

  const prod = ctx?.prod ?? c.prod
  const needsName = write && prod && !(ctx?.write && ctx.prodConfirmed)
  const command = `claude mcp add leme -- "${enginePath}" mcp`

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      await setAgentAccess(c.id, write, needsName ? typed : undefined)
      onClose()
    } catch (e) {
      setError(String((e as Error).message ?? e))
      setSaving(false)
    }
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      /* clipboard blocked: the command stays selectable */
    }
  }

  return (
    <Modal title={`Acesso de agentes (MCP) — ${c.name}`} onClose={onClose} width={620}>
      <div className="ag-dialog" data-testid="agent-access">
        <p>
          O <b>MCP</b> (Model Context Protocol) permite que agentes de IA, como o Claude Code, consultem seus clusters pelo Leme. A <b>leitura é sempre permitida</b> (segredos e valores sensíveis são mascarados). A escrita só existe se você liberar aqui.
        </p>
        <label className="ag-toggle">
          <input type="checkbox" data-testid="agent-write" checked={write} onChange={(e) => setWrite(e.target.checked)} />
          Permitir escrita por agentes neste contexto
        </label>
        {needsName && (
          <div className="ag-confirm">
            <span>
              Este contexto parece ser de <b>produção</b>. Para liberar, digite o nome exato do contexto:
            </span>
            <code className="block">{c.name}</code>
            <input data-testid="agent-confirm" className="input" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="nome do contexto" />
          </div>
        )}
        <ul className="ag-rules muted">
          <li>Toda mudança é em duas etapas: o agente propõe (com diff e dry-run) e só depois aplica, com token de uso único.</li>
          <li>Agentes nunca podem excluir objetos, nem alterar Secrets ou RBAC, nem abrir terminal.</li>
          <li>Tudo fica registrado no log de auditoria. Desligar aqui vale na hora, mesmo com o agente em execução.</li>
        </ul>
        <div>
          <div className="muted">Para conectar o Claude Code:</div>
          <div className="ag-command">
            <code data-testid="agent-command">{command}</code>
            <button onClick={copy} disabled={!enginePath}>
              {copied ? 'Copiado' : 'Copiar'}
            </button>
          </div>
        </div>
        {error && <div className="ag-error">{error}</div>}
        <div className="modal-actions">
          <button onClick={onClose}>Cancelar</button>
          <button className="primary" data-testid="agent-save" disabled={saving || !ctx || (needsName && typed !== c.name)} onClick={save}>
            Salvar
          </button>
        </div>
      </div>
    </Modal>
  )
}
