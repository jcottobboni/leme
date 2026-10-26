import { useState } from 'react'
import { YamlDiff } from '../../components/Monaco'

export interface ReviewState {
  title: string
  subtitle?: string
  previous: string
  next: string
  confirmLabel: string
  danger?: boolean
  onConfirm: () => Promise<void>
}

/** Full-panel review of a dry-run: manifest diff plus an explicit confirmation. */
export function Review({ r, onClose }: { r: ReviewState; onClose: () => void }) {
  const [busy, setBusy] = useState(false)
  const unchanged = r.previous === r.next
  return (
    <div className="review">
      <div className="toolbar">
        <b>{r.title}</b>
        {r.subtitle && <span className="muted">{r.subtitle}</span>}
        <span className="muted">Diff: atual × resultado do dry-run no servidor (Secrets mascarados)</span>
        <span className="spacer" />
        <button onClick={onClose} disabled={busy}>Cancelar</button>
        <button
          className={r.danger ? 'danger' : 'primary'}
          disabled={busy}
          data-testid="review-confirm"
          onClick={async () => {
            setBusy(true)
            try {
              await r.onConfirm()
              onClose()
            } finally {
              setBusy(false)
            }
          }}
        >
          {busy ? 'Aplicando…' : r.confirmLabel}
        </button>
      </div>
      {unchanged && <div className="banner">Nenhuma mudança no manifest.</div>}
      <YamlDiff original={r.previous} modified={r.next} />
    </div>
  )
}
