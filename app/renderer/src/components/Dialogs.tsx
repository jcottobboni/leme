import { useState, type ReactNode } from 'react'

export function Modal({ title, children, onClose, width }: { title: string; children: ReactNode; onClose: () => void; width?: number }) {
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()} role="dialog" aria-label={title} style={width ? { width: `min(${width}px, 94vw)` } : undefined}>
        <div className="modal-title">{title}</div>
        {children}
      </div>
    </div>
  )
}

export function UnlockDialog({ contextName, onConfirm, onCancel }: { contextName: string; onConfirm: (typed: string) => void; onCancel: () => void }) {
  const [typed, setTyped] = useState('')
  return (
    <Modal title="Liberar escrita em cluster de PRODUÇÃO" onClose={onCancel}>
      <p>
        Este contexto parece ser de <b>produção</b>. Para permitir edição, exclusão, scale, restart e terminal, digite o nome exato do contexto:
      </p>
      <code className="block">{contextName}</code>
      <input autoFocus className="input" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="nome do contexto" />
      <div className="modal-actions">
        <button onClick={onCancel}>Cancelar</button>
        <button className="danger" disabled={typed !== contextName} onClick={() => onConfirm(typed)}>
          Liberar escrita
        </button>
      </div>
    </Modal>
  )
}

export function ConfirmDialog({ title, body, confirmLabel, danger, onConfirm, onCancel }: { title: string; body: ReactNode; confirmLabel: string; danger?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return (
    <Modal title={title} onClose={onCancel}>
      <div>{body}</div>
      <div className="modal-actions">
        <button onClick={onCancel}>Cancelar</button>
        <button className={danger ? 'danger' : 'primary'} onClick={onConfirm} autoFocus>
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}

export function PromptDialog({ title, label, initial, onConfirm, onCancel, type = 'text' }: { title: string; label: string; initial: string; type?: string; onConfirm: (v: string) => void; onCancel: () => void }) {
  const [v, setV] = useState(initial)
  return (
    <Modal title={title} onClose={onCancel}>
      <label className="field">
        {label}
        <input autoFocus className="input" type={type} value={v} onChange={(e) => setV(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && onConfirm(v)} />
      </label>
      <div className="modal-actions">
        <button onClick={onCancel}>Cancelar</button>
        <button className="primary" onClick={() => onConfirm(v)}>
          OK
        </button>
      </div>
    </Modal>
  )
}
