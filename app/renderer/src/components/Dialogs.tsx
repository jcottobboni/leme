import { useEffect, useId, useRef, useState, type ReactNode } from 'react'

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]), [contenteditable="true"]'

// Open modals, innermost last: only the top one reacts to Escape and Tab.
const stack: string[] = []

export interface ModalProps {
  title: string
  children: ReactNode
  onClose: () => void
  width?: number
  /** While true, Escape and the backdrop do not close the dialog (an operation is running). */
  busy?: boolean
  /**
   * Destructive or typed confirmations: a click on the backdrop does not close them (Escape and Cancel still do).
   * Defaults to false.
   */
  destructive?: boolean
  testId?: string
}

/**
 * Accessible modal: role=dialog + aria-modal, labelled by its title, initial focus inside (the `autoFocus` element or
 * the first focusable one), focus trapped while open and restored on close, Escape closes (unless `busy`).
 * The backdrop closes only on a full click on it (press and release outside), never on destructive dialogs.
 */
export function Modal({ title, children, onClose, width, busy, destructive, testId }: ModalProps) {
  const id = useId()
  const box = useRef<HTMLDivElement>(null)
  const downOnBackdrop = useRef(false)
  const close = useRef(onClose)
  close.current = onClose
  const busyRef = useRef(busy)
  busyRef.current = busy

  useEffect(() => {
    stack.push(id)
    const prev = document.activeElement as HTMLElement | null
    const el = box.current!
    // `autoFocus` children are focused by React before this runs; otherwise focus the first control.
    if (!el.contains(document.activeElement)) {
      const first = el.querySelector<HTMLElement>(FOCUSABLE)
      ;(first ?? el).focus()
    }
    const onKey = (e: KeyboardEvent) => {
      if (stack[stack.length - 1] !== id) return
      // Monaco uses Escape itself (find widget, suggestions) when the editor has focus.
      if (e.key === 'Escape' && !e.isComposing && !(e.target as Element | null)?.closest?.('.monaco-editor')) {
        // Capture phase + stop: shell and panel Escape handlers must not also act under an open dialog.
        e.stopPropagation()
        e.preventDefault()
        if (!busyRef.current) close.current()
        return
      }
      if (e.key !== 'Tab') return
      const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((n) => n.offsetParent !== null || n === document.activeElement)
      if (!items.length) {
        e.preventDefault()
        el.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement as HTMLElement | null
      if (e.shiftKey && (active === first || !el.contains(active))) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && (active === last || !el.contains(active))) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => {
      window.removeEventListener('keydown', onKey, true)
      const i = stack.lastIndexOf(id)
      if (i >= 0) stack.splice(i, 1)
      if (prev && prev.isConnected) prev.focus({ preventScroll: true })
    }
  }, [id])

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        downOnBackdrop.current = e.target === e.currentTarget
      }}
      onClick={(e) => {
        const full = downOnBackdrop.current && e.target === e.currentTarget
        downOnBackdrop.current = false
        if (full && !destructive && !busy) onClose()
      }}
    >
      <div
        ref={box}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={`${id}-title`}
        aria-busy={busy || undefined}
        tabIndex={-1}
        data-testid={testId}
        style={width ? { width: `min(${width}px, 94vw)` } : undefined}
      >
        <div className="modal-title" id={`${id}-title`}>{title}</div>
        {children}
      </div>
    </div>
  )
}

/** Exact-match check of a "type the name to confirm" field. */
export const typedMatches = (expected: string, typed: string) => !!expected && typed === expected

/**
 * "Type the name to confirm" field: prompt, the expected text (selectable, to copy) and the input.
 * Controlled; enable the confirm button with `typedMatches(expected, value)`.
 */
export function TypedConfirm({ expected, value, onChange, children, placeholder = 'nome', testId, autoFocus = true, onEnter }: {
  expected: string
  value: string
  onChange: (v: string) => void
  /** Prompt shown above the expected text. Default: "Digite o nome exato para confirmar:". */
  children?: ReactNode
  placeholder?: string
  /** data-testid of the input. */
  testId?: string
  autoFocus?: boolean
  /** Called on Enter when the text matches. */
  onEnter?: () => void
}) {
  return (
    <div className="typed-confirm">
      <p>{children ?? 'Digite o nome exato para confirmar:'}</p>
      <code className="block typed-confirm-target">{expected}</code>
      <input
        autoFocus={autoFocus}
        className="input"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && typedMatches(expected, value) && onEnter?.()}
        placeholder={placeholder}
        aria-label={placeholder}
        spellCheck={false}
        autoComplete="off"
        data-testid={testId}
      />
    </div>
  )
}

/** A whole destructive confirmation that requires typing `expected` (see TypedConfirm). */
export function TypedConfirmDialog({ title, body, expected, confirmLabel, placeholder, busy, testId, onConfirm, onCancel }: {
  title: string
  body: ReactNode
  expected: string
  confirmLabel: string
  placeholder?: string
  busy?: boolean
  testId?: string
  onConfirm: (typed: string) => void
  onCancel: () => void
}) {
  const [typed, setTyped] = useState('')
  const ok = typedMatches(expected, typed) && !busy
  return (
    <Modal title={title} onClose={onCancel} busy={busy} destructive>
      <TypedConfirm expected={expected} value={typed} onChange={setTyped} placeholder={placeholder} testId={testId} onEnter={() => ok && onConfirm(typed)}>
        {body}
      </TypedConfirm>
      <div className="modal-actions">
        <button onClick={onCancel} disabled={busy}>Cancelar</button>
        <button className="danger" disabled={!ok} onClick={() => onConfirm(typed)}>
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}

export function UnlockDialog({ contextName, onConfirm, onCancel }: { contextName: string; onConfirm: (typed: string) => void; onCancel: () => void }) {
  return (
    <TypedConfirmDialog
      title="Liberar escrita em cluster de PRODUÇÃO"
      body={<>Este contexto parece ser de <b>produção</b>. Para permitir edição, exclusão, scale, restart e terminal, digite o nome exato do contexto:</>}
      expected={contextName}
      placeholder="nome do contexto"
      confirmLabel="Liberar escrita"
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  )
}

export function ConfirmDialog({ title, body, confirmLabel, danger, busy, onConfirm, onCancel }: { title: string; body: ReactNode; confirmLabel: string; danger?: boolean; busy?: boolean; onConfirm: () => void; onCancel: () => void }) {
  return (
    <Modal title={title} onClose={onCancel} destructive={danger} busy={busy}>
      <div>{body}</div>
      <div className="modal-actions">
        <button onClick={onCancel} disabled={busy}>Cancelar</button>
        <button className={danger ? 'danger' : 'primary'} onClick={onConfirm} disabled={busy} autoFocus>
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
