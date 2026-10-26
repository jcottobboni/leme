import { useState } from 'react'
import { Check, RotateCcw } from 'lucide-react'
import type { ClusterInfo } from '../../api'
import { ENV_LABEL, identityOf, SWATCHES, useProfiles, type ClusterEnv, type ClusterProfile } from '../../store'
import { Modal } from '../../components/Dialogs'
import { AvatarView } from './Sidebar'

const ENVS: [ClusterEnv | '', string][] = [['', 'Automático'], ['prod', 'Produção'], ['hml', 'Homologação'], ['dev', 'Desenvolvimento'], ['test', 'Teste'], ['none', 'Nenhum']]
const ICONS = ['☁️', '🧪', '🚀', '🛡️', '🏛️', '⚙️', '🔥', '🐳', '🌐', '💾', '📦', '🧭', '🇧🇷']

/** Alias, short label, color, icon and environment of a cluster, with a live preview. */
export function ClusterProfileDialog({ c, onClose }: { c: ClusterInfo; onClose: () => void }) {
  const saved = useProfiles((s) => s.profiles[c.id])
  const [p, setP] = useState<ClusterProfile>(saved ?? {})
  const set = (patch: Partial<ClusterProfile>) => setP((x) => ({ ...x, ...patch }))
  const auto = identityOf(c.id, c.name, undefined, c.prod)
  const id = identityOf(c.id, c.name, p, c.prod)
  const save = () => {
    useProfiles.getState().setProfile(c.id, { ...p, alias: p.alias?.trim(), short: p.short?.trim().toUpperCase() })
    onClose()
  }
  return (
    <Modal title="Personalizar cluster" onClose={onClose} width={620}>
      <div className="cp-dialog" data-testid="cluster-profile">
        <div className="cp-preview">
          <div className="cp-hot">
            <AvatarView id={id} size={40} />
            <span className="hot-label">{id.name}</span>
          </div>
          <div className="cp-preview-main">
            <div className="cp-preview-name">{id.name}</div>
            <div className="muted small ellipsis" title={c.name}>{c.name}</div>
            <div className="cp-tab" style={{ '--cc': id.color } as React.CSSProperties}>
              <span className="cp-tab-dot" /> Pods <span className="muted">· {id.short}</span>
            </div>
          </div>
        </div>

        <div className="row">
          <label className="field grow">Apelido
            <input autoFocus className="input" data-testid="cp-alias" value={p.alias ?? ''} placeholder={auto.name} onChange={(e) => set({ alias: e.target.value })} maxLength={40} />
          </label>
          <label className="field">Sigla
            <input className="input cp-short" data-testid="cp-short" value={p.short ?? ''} placeholder={auto.short} maxLength={4}
              onChange={(e) => set({ short: e.target.value.replace(/\s/g, '').toUpperCase() })} />
          </label>
        </div>

        <div className="field">Ambiente
          <div className="seg">
            {ENVS.map(([k, l]) => (
              <button key={k || 'auto'} className={(p.env ?? '') === k ? 'on' : ''} onClick={() => set({ env: (k || undefined) as ClusterEnv | undefined })}>
                {l}{k === '' && auto.env !== 'none' ? ` (${ENV_LABEL[auto.env]})` : ''}
              </button>
            ))}
          </div>
        </div>

        <div className="field">Cor
          <div className="cp-swatches">
            {SWATCHES.map((col) => (
              <button key={col} className={`cp-swatch ${id.color === col ? 'on' : ''}`} style={{ background: col }} title={col} aria-label={`Cor ${col}`} onClick={() => set({ color: col })}>
                {id.color === col && <Check size={13} />}
              </button>
            ))}
            <label className="cp-swatch cp-custom" title="Outra cor">
              <input type="color" data-testid="cp-color" value={id.color} onChange={(e) => set({ color: e.target.value })} />
            </label>
          </div>
        </div>

        <div className="field">Ícone
          <div className="cp-icons">
            <button className={!p.icon ? 'on' : ''} onClick={() => set({ icon: undefined })} title="Usar a sigla">{id.short || 'AB'}</button>
            {ICONS.map((ic) => <button key={ic} className={p.icon === ic ? 'on' : ''} onClick={() => set({ icon: ic })}>{ic}</button>)}
            <input className="input cp-emoji" placeholder="outro" maxLength={4} value={p.icon && !ICONS.includes(p.icon) ? p.icon : ''} onChange={(e) => set({ icon: e.target.value || undefined })} />
          </div>
        </div>
      </div>
      <div className="modal-actions">
        <button onClick={() => setP({})} title="Voltar à cor, sigla e nome automáticos"><RotateCcw size={13} /> Restaurar padrão</button>
        <span className="spacer" />
        <button onClick={onClose}>Cancelar</button>
        <button className="primary" data-testid="cp-save" onClick={save}>Salvar</button>
      </div>
    </Modal>
  )
}
