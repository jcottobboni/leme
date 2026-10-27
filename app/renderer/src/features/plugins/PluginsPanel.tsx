import { useState } from 'react'
import type { IDockviewPanelProps } from 'dockview-react'
import { FolderOpen, Puzzle, ShieldAlert, ShieldCheck, ShieldOff } from 'lucide-react'
import { openPanel, useApp, type OpenOptions } from '../../store'
import { ErrorBanner, Empty, useAsync } from '../common'
import { PanelHeader, refreshAction } from '../../components/PanelHeader'
import { commandLine, pluginsApi, usePlugins, type PluginsInfo } from './api'
import './plugins.css'

export function openPlugins(clusterId?: string, opts?: OpenOptions) {
  openPanel('plugins', 'plugins', 'Plugins', { clusterId }, opts)
}

type FolderBridge = { openFolder: () => Promise<{ ok: boolean; path?: string; error?: string }> }
const folderBridge = (window as unknown as { leme?: { plugins?: FolderBridge } }).leme?.plugins

const EXAMPLE = `apiVersion: leme.dev/v1
name: meu-time
actions:
- id: describe
  title: kubectl describe
  scopes: [pods, deployments]
  command: kubectl
  args: [describe, $RESOURCE_NAME, $NAME, -n, $NAMESPACE]
  mode: output
links:
- id: grafana
  title: Abrir no Grafana
  scopes: [deployments]
  url: https://grafana.exemplo/d/app?var-ns=$NAMESPACE&var-app=$NAME`

const MODE = { terminal: 'terminal', output: 'saída', background: 'segundo plano' }

/** Loaded plugins, their files and trust (M8). */
export function PluginsPanel(_: IDockviewPanelProps) {
  const toast = useApp((s) => s.toast)
  const { data, error, loading, reload, setData } = useAsync(() => pluginsApi.list(), [], 10000)
  const [busy, setBusy] = useState(false)
  const apply = (info: PluginsInfo) => {
    setData(info)
    usePlugins.getState().set(info)
  }
  const act = async (fn: () => Promise<PluginsInfo | unknown>, ok?: string) => {
    setBusy(true)
    try {
      const r = await fn()
      if (r && typeof r === 'object' && 'files' in (r as PluginsInfo)) apply(r as PluginsInfo)
      else apply(await pluginsApi.list())
      if (ok) toast('ok', ok)
    } catch (e) {
      toast('error', (e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const openFolder = async () => {
    if (!folderBridge) return toast('info', `Pasta de plugins: ${data?.dir}`)
    const r = await folderBridge.openFolder()
    if (!r.ok) toast('error', r.error ?? 'Não foi possível abrir a pasta')
  }
  const info = data
  return (
    <div className="grid-panel">
      <PanelHeader title="Plugins" icon={Puzzle} actions={[
        refreshAction(reload, loading),
        { key: 'reload', label: 'Recarregar arquivos', onClick: () => void act(() => pluginsApi.reload(), 'Plugins recarregados'), disabled: busy, testid: 'plugins-reload' },
        { key: 'folder', label: 'Abrir pasta', icon: FolderOpen, onClick: () => void openFolder() },
      ]} />
      <div className="plugins-panel" data-testid="plugins-panel">
        <ErrorBanner error={error} />
        {info && (
          <>
            <p className="muted small">
              Pasta: <span className="mono">{info.dir}</span>. Arquivos <b>.yaml</b> no formato do Leme ou do k9s; o Leme recarrega sozinho quando mudam.
            </p>
            <label className="row-check">
              <input type="checkbox" checked={info.k9s} disabled={busy} data-testid="plugins-k9s" onChange={(e) => void act(() => pluginsApi.setK9s(e.target.checked))} />
              Ler também os plugins do k9s (<span className="mono">plugins.yaml</span> e a pasta de plugins do k9s)
            </label>

            {!info.files.length && (
              <Empty title="Nenhum plugin ainda" code={EXAMPLE}>
                Crie um arquivo <span className="mono">.yaml</span> na pasta de plugins, como o exemplo abaixo, ou copie plugins da comunidade do k9s. Veja docs/plugins.md.
              </Empty>
            )}

            {info.files.length > 0 && <h3>Arquivos</h3>}
            {info.files.map((f) => (
              <div key={f.path} className="file" data-testid="plugin-file">
                <div className="path">{f.path}</div>
                <div className="muted small">
                  {f.origin === 'k9s' ? 'k9s' : f.format === 'k9s' ? 'formato k9s' : f.plugin ?? '—'}
                  {!f.error && ` · ${f.actions} ações · ${f.links} links · ${f.views} visões · ${f.rules} regras`}
                </div>
                {f.error && <div className="err" data-testid="plugin-file-error">{f.error}</div>}
                {!!f.warnings?.length && <ul className="warn-list">{f.warnings.map((w) => <li key={w}>{w}</li>)}</ul>}
              </div>
            ))}

            {info.actions.length > 0 && (
              <>
                <h3>Ações</h3>
                <table className="table">
                  <thead><tr><th>Ação</th><th>Escopo</th><th>Modo</th><th>Comando</th><th>Confiança</th><th /></tr></thead>
                  <tbody>
                    {info.actions.map((a) => (
                      <tr key={a.id} data-testid={`plugin-action-${a.id}`}>
                        <td>{a.mutates && <ShieldAlert size={13} />} {a.title} <span className="muted small">{a.id}</span></td>
                        <td>{a.scopes.join(', ')}</td>
                        <td>{MODE[a.mode]}</td>
                        <td className="cmd" title={commandLine(a)}>{commandLine(a)}</td>
                        <td>{a.trusted ? <span className="pill pill-ok"><ShieldCheck size={12} /> confiável</span> : <span className="pill pill-neutral">não revisada</span>}</td>
                        <td>{a.trusted && <button className="icon-btn" title="Revogar confiança" onClick={() => void act(() => pluginsApi.revoke(a.id), 'Confiança revogada')}><ShieldOff size={14} /></button>}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p className="muted small">Uma ação é aprovada ao ser executada pela primeira vez (menu de contexto da grade ou detalhe do objeto).</p>
              </>
            )}

            {info.links.length > 0 && (
              <>
                <h3>Links</h3>
                <table className="table">
                  <thead><tr><th>Link</th><th>Escopo</th><th>URL</th></tr></thead>
                  <tbody>{info.links.map((l) => <tr key={l.id}><td>{l.title}</td><td>{l.scopes.join(', ')}</td><td className="cmd">{l.url}</td></tr>)}</tbody>
                </table>
              </>
            )}

            {info.views.length > 0 && (
              <>
                <h3>Visões de recursos</h3>
                <table className="table">
                  <thead><tr><th>Tipo</th><th>Colunas</th><th>Plugin</th></tr></thead>
                  <tbody>{info.views.map((v) => <tr key={`${v.group}/${v.kind}`}><td>{v.kind} <span className="muted small">{v.group}</span></td><td>{v.columns.join(', ') || '—'}</td><td>{v.plugin}</td></tr>)}</tbody>
                </table>
              </>
            )}

            {info.rules.length > 0 && (
              <>
                <h3>Regras de diagnóstico (CEL)</h3>
                <table className="table">
                  <thead><tr><th>Regra</th><th>Severidade</th><th>Tipos</th><th>Plugin</th></tr></thead>
                  <tbody>{info.rules.map((r) => <tr key={`${r.plugin}/${r.id}`}><td>{r.title}</td><td>{r.severity}</td><td>{r.kinds.join(', ')}</td><td>{r.plugin}</td></tr>)}</tbody>
                </table>
              </>
            )}
          </>
        )}
      </div>
    </div>
  )
}
