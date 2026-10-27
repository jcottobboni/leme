import { useEffect, useState } from 'react'
import { File, FilePlus, Folder, FolderPlus, Lock, Trash2 } from 'lucide-react'
import { Modal } from '../../components/Dialogs'
import { useApp } from '../../store'
import { getKubeconfigs, pickBridge, setKubeconfigs, type KubeconfigSettings } from './api'
import './kubeconfig.css'

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`

/** Kubeconfig files and folders the app reads, on top of $KUBECONFIG (or ~/.kube/config). */
export function KubeconfigDialog({ onClose }: { onClose: () => void }) {
  const [data, setData] = useState<KubeconfigSettings>()
  const [typed, setTyped] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    getKubeconfigs()
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)))
  }, [])

  // Every change is saved at once and the context list reloads, so there is no Save button.
  const save = async (paths: string[]) => {
    setBusy(true)
    setError('')
    try {
      setData(await setKubeconfigs(paths))
      await useApp.getState().loadClusters()
      return true
    } catch (e) {
      setError((e as Error).message)
      return false
    } finally {
      setBusy(false)
    }
  }
  const current = data?.sources.map((s) => s.path) ?? []
  const add = (paths: string[]) => paths.length > 0 && save([...current, ...paths])
  const pick = async (kind: 'file' | 'dir') => {
    if (pickBridge) await add(await pickBridge.pick(kind))
  }
  const addTyped = async () => {
    if (typed.trim() && (await save([...current, typed.trim()]))) setTyped('')
  }

  return (
    <Modal title="Arquivos kubeconfig" onClose={onClose} width={640}>
      <div className="kc-dialog" data-testid="kubeconfig-dialog">
        <p>
          Adicione arquivos ou pastas com kubeconfigs. Numa pasta, entram todos os arquivos <code>config</code>, <code>*.yaml</code>, <code>*.yml</code> e <code>*.kubeconfig</code>, inclusive em subpastas; backups (<code>*.bak*</code>) ficam de fora. Arquivos novos aparecem ao recarregar os contextos.
        </p>
        <p className="muted small">
          Adicione só arquivos e pastas de confiança: um kubeconfig pode executar comandos na sua máquina (plugins <code>exec</code>) quando você conecta no contexto.
        </p>
        {data && data.base.length > 0 && (
          <div>
            <div className="muted">Sempre lidos (variável KUBECONFIG ou padrão):</div>
            <ul className="kc-list">
              {data.base.map((p) => (
                <li key={p} className="kc-item" title="Definido fora do app">
                  <Lock size={14} />
                  <span className="kc-path">{p}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
        <div>
          <div className="muted">Adicionados no app:</div>
          {data && data.sources.length === 0 && <div className="muted">Nenhum ainda.</div>}
          <ul className="kc-list" data-testid="kubeconfig-sources">
            {data?.sources.map((s) => (
              <li key={s.path} className="kc-item">
                {s.kind === 'dir' ? <Folder size={14} /> : <File size={14} />}
                <span className="kc-path" title={s.files.join('\n')}>{s.path}</span>
                <span className={`kc-meta ${s.kind === 'missing' ? 'kc-missing' : 'muted'}`}>
                  {s.kind === 'missing' ? 'não encontrado' : s.kind === 'dir' ? plural(s.files.length, 'arquivo', 'arquivos') : ''}
                </span>
                <button className="icon-btn" title="Remover" disabled={busy} onClick={() => save(current.filter((p) => p !== s.path))}>
                  <Trash2 size={14} />
                </button>
              </li>
            ))}
          </ul>
        </div>
        <div className="kc-add">
          {pickBridge && (
            <>
              <button disabled={busy || !data} onClick={() => pick('dir')}>
                <FolderPlus size={14} /> Adicionar pasta
              </button>
              <button disabled={busy || !data} onClick={() => pick('file')}>
                <FilePlus size={14} /> Adicionar arquivo
              </button>
            </>
          )}
          <input
            className="input"
            data-testid="kubeconfig-path"
            placeholder="ou digite um caminho, ex.: ~/.kube/clients"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && addTyped()}
          />
          <button data-testid="kubeconfig-add" disabled={busy || !data || !typed.trim()} onClick={addTyped}>
            Adicionar
          </button>
        </div>
        {error && <div className="kc-error">{error}</div>}
        <div className="modal-actions">
          <button className="primary" onClick={onClose}>
            Fechar
          </button>
        </div>
      </div>
    </Modal>
  )
}
