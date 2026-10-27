import { useState } from 'react'
import { create } from 'zustand'
import { ExternalLink, Puzzle, ShieldAlert } from 'lucide-react'
import { api, type Resource } from '../../api'
import { findResource, openPanel, useApp } from '../../store'
import { ConfirmDialog, Modal } from '../../components/Dialogs'
import type { MenuItem } from '../shell/menu'
import { actionsFor, commandLine, ensurePlugins, linksFor, linkURL, pluginsApi, usePlugins, type PluginAction, type PluginInput, type RunResult } from './api'
import './plugins.css'

type Dialog =
  | { kind: 'trust'; clusterId: string; action: PluginAction; input: PluginInput }
  | { kind: 'confirm'; clusterId: string; action: PluginAction; input: PluginInput }
  | { kind: 'container'; clusterId: string; action: PluginAction; input: PluginInput; containers: string[] }
  | { kind: 'output'; action: PluginAction; input: PluginInput; result: RunResult }

const useDialog = create<{ d?: Dialog }>(() => ({}))
const close = () => useDialog.setState({ d: undefined })

const MODE_LABEL = { terminal: 'terminal interativo', output: 'mostra a saída', background: 'em segundo plano' }

/** Runs a plugin action: trust first, then container choice (k9s containers scope), confirmation and the run. */
export async function runAction(clusterId: string, action: PluginAction, input: PluginInput, step: 'trust' | 'container' | 'confirm' | 'run' = 'trust') {
  const { toast } = useApp.getState()
  if (step === 'trust' && !action.trusted) return useDialog.setState({ d: { kind: 'trust', clusterId, action, input } })
  if ((step === 'trust' || step === 'container') && action.scopes.includes('containers') && !input.container && input.group === '' && input.resource === 'pods') {
    try {
      const res = findResource(clusterId, '', 'pods')
      const v = await api.object(clusterId, { group: '', version: res?.version ?? 'v1', resource: 'pods', namespace: input.namespace, name: input.name })
      const names: string[] = (v.object.spec?.containers ?? []).map((c: { name: string }) => c.name)
      if (names.length > 1) return useDialog.setState({ d: { kind: 'container', clusterId, action, input, containers: names } })
      input = { ...input, container: names[0] }
    } catch (e) {
      return toast('error', `Plugin: ${(e as Error).message}`)
    }
  }
  if (step !== 'run' && (action.confirm || action.mutates)) return useDialog.setState({ d: { kind: 'confirm', clusterId, action, input } })
  if (action.mode === 'terminal') {
    openPanel(`plugin:${clusterId}:${action.id}:${Date.now()}`, 'pluginTerm', `${action.title} · ${input.name}`, { clusterId, action: action.id, title: action.title, input }, { area: 'bottom' })
    return
  }
  if (action.mode === 'background') toast('info', `${action.title}: executando em segundo plano…`)
  try {
    const result = await pluginsApi.run(clusterId, action.id, input)
    const failed = result.exitCode !== 0 || !!result.error
    if (action.mode === 'output' || failed) useDialog.setState({ d: { kind: 'output', action, input, result } })
    else toast('ok', `${action.title}: concluído`)
  } catch (e) {
    toast('error', `${action.title}: ${(e as Error).message}`)
    void ensurePlugins(0)
  }
}

export function openLink(clusterId: string, link: Parameters<typeof linkURL>[0], input: PluginInput) {
  const c = useApp.getState().clusters.find((x) => x.id === clusterId)
  const url = linkURL(link, input, { context: c?.name ?? '', cluster: c?.cluster ?? '', user: c?.user ?? '' })
  if (!url) return useApp.getState().toast('error', `${link.title}: o link não é http(s)`)
  window.open(url, '_blank', 'noopener,noreferrer')
}

/** Menu entries of the plugins that apply to a resource. */
export function pluginMenuItems(clusterId: string, res: Resource | undefined, input: () => PluginInput): MenuItem[] {
  if (!res) return []
  void ensurePlugins()
  const info = usePlugins.getState().info
  const acts = actionsFor(info, res)
  const links = linksFor(info, res)
  if (!acts.length && !links.length) return []
  const readOnly = useApp.getState().connected[clusterId]?.readOnly ?? true
  const items: MenuItem[] = [{ separator: true }]
  for (const a of acts) {
    items.push({
      label: a.title + (a.trusted ? '' : ' (revisar)'),
      icon: a.mutates ? ShieldAlert : Puzzle,
      hint: a.shortCut || (a.format === 'k9s' ? 'k9s' : undefined),
      danger: a.mutates,
      disabled: a.mutates && readOnly,
      onClick: () => void runAction(clusterId, a, input()),
    })
  }
  for (const l of links) items.push({ label: l.title, icon: ExternalLink, onClick: () => openLink(clusterId, l, input()) })
  return items
}

function ActionSummary({ a }: { a: PluginAction }) {
  return (
    <div className="plugin-summary">
      <div className="muted small">
        {a.format === 'k9s' ? 'Plugin do k9s' : `Plugin ${a.plugin}`} · {MODE_LABEL[a.mode]} · <span className="mono">{a.source}</span>
      </div>
      <pre className="plugin-cmd" data-testid="plugin-cmd">{commandLine(a)}</pre>
    </div>
  )
}

/** Trust, confirmation, container choice and output of plugin actions. Mounted once in the app. */
export function PluginDialogHost() {
  const d = useDialog((s) => s.d)
  const [busy, setBusy] = useState(false)
  if (!d) return null
  if (d.kind === 'trust') {
    const a = d.action
    return (
      <Modal title={`Confiar em "${a.title}"?`} onClose={close} busy={busy} destructive={a.mutates} testId="plugin-trust">
        <p>Esta ação roda um programa no seu computador. Confira o comando exato; se o arquivo mudar, o Leme pergunta de novo.</p>
        <ActionSummary a={a} />
        <ul className="small plugin-notes">
          <li>O comando recebe um kubeconfig temporário, só com o contexto <b>{useApp.getState().clusters.find((c) => c.id === d.clusterId)?.name}</b>, que passa pelo Leme: com o cluster travado ele só consegue ler.</li>
          {a.mutates && <li><b>Marcada como alteração</b>: só roda com a escrita destravada, e pede confirmação a cada uso.</li>}
          <li>Um programa pode ler seus arquivos e credenciais por conta própria: confie só em plugins de origem conhecida.</li>
        </ul>
        <div className="modal-actions">
          <button onClick={close} disabled={busy}>Cancelar</button>
          <button className={a.mutates ? 'danger' : 'primary'} disabled={busy} data-testid="plugin-trust-ok" autoFocus
            onClick={async () => {
              setBusy(true)
              try {
                await pluginsApi.trust(a.id, a.hash)
                usePlugins.getState().set(await pluginsApi.list())
                close()
                void runAction(d.clusterId, { ...a, trusted: true }, d.input, 'container')
              } catch (e) {
                useApp.getState().toast('error', (e as Error).message)
                close()
                void ensurePlugins(0)
              } finally {
                setBusy(false)
              }
            }}>
            Confiar e executar
          </button>
        </div>
      </Modal>
    )
  }
  if (d.kind === 'container') {
    return (
      <Modal title="Qual contêiner?" onClose={close}>
        <div className="plugin-containers">
          {d.containers.map((c) => (
            <button key={c} onClick={() => (close(), void runAction(d.clusterId, d.action, { ...d.input, container: c }, 'confirm'))}>{c}</button>
          ))}
        </div>
      </Modal>
    )
  }
  if (d.kind === 'confirm') {
    const a = d.action
    return (
      <ConfirmDialog title={a.title} danger={a.mutates} confirmLabel="Executar"
        body={<><p>Executar em <b>{d.input.namespace ? `${d.input.namespace}/` : ''}{d.input.name}</b>{a.mutates ? ' (altera o cluster)' : ''}?</p><ActionSummary a={a} /></>}
        onCancel={close} onConfirm={() => (close(), void runAction(d.clusterId, a, d.input, 'run'))} />
    )
  }
  const r = d.result
  return (
    <Modal title={d.action.title} onClose={close} width={760} testId="plugin-output">
      <div className="muted small">
        {d.input.namespace ? `${d.input.namespace}/` : ''}{d.input.name} · <span data-testid="plugin-exit">saída {r.exitCode}</span> · {r.durationMs} ms{r.truncated ? ' · saída cortada em 1 MiB' : ''}
      </div>
      {r.error && <div className="banner error">{r.error}</div>}
      <pre className="plugin-output" data-testid="plugin-output-text">{r.output || '(sem saída)'}</pre>
      <div className="modal-actions">
        <button onClick={() => navigator.clipboard.writeText(r.output)}>Copiar</button>
        <button className="primary" onClick={close} autoFocus>Fechar</button>
      </div>
    </Modal>
  )
}
