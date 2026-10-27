import { isValidElement, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import { Check, Ellipsis, RefreshCw, Square, SquareCheck, type LucideIcon } from 'lucide-react'
import { openMenu, type MenuItem } from '../features/shell/menu'

/** One option of a segmented action (e.g. layout direction). */
export interface SegOption {
  value: string
  label: string
  icon?: LucideIcon
  title?: string
}

/**
 * Header action. Plain buttons, toggles (`pressed`) and segmented choices (`options`) are all
 * described as data so the header can move the ones that do not fit into the "⋯" menu.
 */
export interface HeaderAction {
  key: string
  label: string
  icon?: LucideIcon
  onClick?: (e?: MouseEvent<HTMLElement>) => void
  title?: string
  testid?: string
  primary?: boolean
  danger?: boolean
  disabled?: boolean
  /** Spins the icon (loading). */
  spin?: boolean
  /** Toggle state; undefined = not a toggle. */
  pressed?: boolean
  /** Shows only the icon (the label goes to the tooltip and aria-label). */
  iconOnly?: boolean
  /** Never moves to the overflow menu. */
  keep?: boolean
  /** Segmented choice. */
  options?: SegOption[]
  value?: string
  onSelect?: (value: string) => void
}

/** The single "Atualizar" action every panel uses. */
export function refreshAction(onClick: () => void, loading?: boolean, extra?: Partial<HeaderAction>): HeaderAction {
  return { key: 'refresh', label: 'Atualizar', icon: RefreshCw, onClick: () => onClick(), spin: loading, keep: true, title: 'Atualizar (buscar de novo no cluster)', ...extra }
}

/** The same "Atualizar" as a plain button, for toolbars inside a panel (detail panes, tabs). */
export function RefreshBtn({ onClick, loading }: { onClick: () => void; loading?: boolean }) {
  return (
    <button onClick={() => onClick()} title="Atualizar (buscar de novo no cluster)">
      <RefreshCw size={14} className={loading ? 'spin-icon' : undefined} /> Atualizar
    </button>
  )
}

function ActionView({ a }: { a: HeaderAction }) {
  const Icon = a.icon
  if (a.options) {
    return (
      <span className="ph-seg" role="group" aria-label={a.label} title={a.title ?? a.label} data-testid={a.testid}>
        {a.options.map((o) => {
          const OI = o.icon
          return (
            <button key={o.value} className={a.value === o.value ? 'on' : ''} aria-pressed={a.value === o.value} title={o.title ?? o.label} disabled={a.disabled} onClick={() => a.onSelect?.(o.value)}>
              {OI && <OI size={13} />}<span>{o.label}</span>
            </button>
          )
        })}
      </span>
    )
  }
  const cls = [a.pressed !== undefined ? `ph-tog ${a.pressed ? 'on' : ''}` : '', a.primary ? 'primary' : '', a.danger ? 'danger' : '', a.iconOnly ? 'icon-btn' : ''].filter(Boolean).join(' ')
  return (
    <button className={cls || undefined} onClick={a.onClick} disabled={a.disabled} title={a.title ?? (a.iconOnly ? a.label : undefined)} aria-label={a.iconOnly ? a.label : undefined}
      aria-pressed={a.pressed} data-testid={a.testid}>
      {Icon && <Icon size={14} className={a.spin ? 'spin-icon' : undefined} />}
      {!a.iconOnly && <span>{a.label}</span>}
    </button>
  )
}

function menuItems(list: HeaderAction[]): MenuItem[] {
  const out: MenuItem[] = []
  for (const a of list) {
    if (a.options) {
      if (out.length) out.push({ separator: true })
      for (const o of a.options) out.push({ label: `${a.label}: ${o.label}`, icon: a.value === o.value ? Check : o.icon, disabled: a.disabled, onClick: () => a.onSelect?.(o.value) })
      continue
    }
    out.push({ label: a.label, icon: a.pressed === undefined ? a.icon : a.pressed ? SquareCheck : Square, danger: a.danger, disabled: a.disabled, onClick: () => a.onClick?.() })
  }
  return out
}

/**
 * Standard panel header: icon + title, scope (namespace, cluster), filters and info, and the actions
 * aligned to the right. It never wraps: when the actions do not fit, the last ones (except `keep`)
 * go to an overflow "⋯" menu, measured with a ResizeObserver.
 */
export function PanelHeader({ icon, title, scope, children, actions = [], testid, className }: {
  icon?: LucideIcon | ReactNode
  title: string
  scope?: ReactNode
  children?: ReactNode
  actions?: HeaderAction[]
  testid?: string
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const main = useRef<HTMLDivElement>(null)
  const movable = actions.filter((a) => !a.keep)
  const [shown, setShown] = useState(Number.POSITIVE_INFINITY)
  const visible = Math.min(shown, movable.length)
  const keys = actions.map((a) => a.key).join('|')

  // Any resize (or a new set of actions) starts again from "everything visible".
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    let w = el.clientWidth
    const ro = new ResizeObserver(() => {
      if (el.clientWidth === w) return
      w = el.clientWidth
      setShown(Number.POSITIVE_INFINITY)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  useLayoutEffect(() => setShown(Number.POSITIVE_INFINITY), [keys])
  // Then hides one action per pass (before paint) while the left side overflows.
  useLayoutEffect(() => {
    const m = main.current
    if (m && visible > 0 && m.scrollWidth > m.clientWidth + 1) setShown(visible - 1)
  })

  const hidden = new Set(movable.slice(visible).map((a) => a.key))
  const overflow = actions.filter((a) => hidden.has(a.key))
  const Icon = icon && !isValidElement(icon) && (typeof icon === 'function' || typeof icon === 'object') ? (icon as LucideIcon) : undefined
  return (
    <div ref={ref} className={`toolbar ph ${className ?? ''}`} data-testid={testid}>
      <div ref={main} className="ph-main">
        <span className="grid-title ph-title">{Icon ? <Icon size={16} /> : (icon as ReactNode)}{title}</span>
        {scope}
        {children}
      </div>
      {actions.length > 0 && (
        <div className="ph-actions">
          {actions.filter((a) => !hidden.has(a.key)).map((a) => <ActionView key={a.key} a={a} />)}
          {overflow.length > 0 && (
            <button className="icon-btn ph-more" title="Mais ações" aria-label="Mais ações" aria-haspopup="menu" data-testid="ph-more"
              onClick={(e) => {
                const r = e.currentTarget.getBoundingClientRect()
                openMenu({ clientX: r.right - 230, clientY: r.bottom + 4, preventDefault: () => e.preventDefault(), stopPropagation: () => e.stopPropagation() }, menuItems(overflow))
              }}>
              <Ellipsis size={16} />
            </button>
          )}
        </div>
      )}
    </div>
  )
}
