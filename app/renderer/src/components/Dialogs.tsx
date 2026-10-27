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

export function UnlockDialog({ contextName, shared, shell, onConfirm, onCancel }: { contextName: string; shared?: boolean; shell?: boolean; onConfirm: (typed: string) => void; onCancel: () => void }) {
  const [typed, setTyped] = useState('')
  return (
    <Modal title={shell ? 'Liberar shell em cluster de PRODUÇÃO' : 'Liberar escrita em cluster de PRODUÇÃO'} onClose={onCancel}>
      <p>
        {shared ? (
          <>Este contexto usa o <b>mesmo servidor</b> de um contexto de produção: liberar aqui vale para aquele cluster inteiro. </>
        ) : (
          <>Este contexto parece ser de <b>produção</b>. </>
        )}
        {shell ? 'Para abrir terminais nos pods (a escrita continua travada)' : 'Para permitir edição, exclusão, scale, restart e terminal'}, digite o nome exato do contexto:
      </p>
      <code className="block">{contextName}</code>
      <input autoFocus className="input" value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="nome do contexto" />
      <div className="modal-actions">
        <button onClick={onCancel}>Cancelar</button>
        <button className="danger" disabled={typed !== contextName} onClick={() => onConfirm(typed)}>
          {shell ? 'Liberar shell' : 'Liberar escrita'}
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

export function PromptDialog({ title, label, initial, onConfirm, onCancel, type = 'text', confirmLabel = 'OK' }: { title: string; label: string; initial: string; type?: string; confirmLabel?: string; onConfirm: (v: string) => void; onCancel: () => void }) {
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
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
