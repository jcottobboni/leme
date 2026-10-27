import { useCallback, useEffect, useRef } from 'react'

type DragEvent = { clientX: number; clientY: number; preventDefault: () => void; stopPropagation?: () => void }

/**
 * Low-level drag: calls `onMove(dx, dy, ev)` with the offset from the press point until the mouse is released, then
 * `onEnd()`. Adds `body.resizing` during the drag (no text selection, resize cursor). Returns a function that aborts
 * the drag (removes the listeners) without calling `onEnd`.
 */
export function beginDrag(e: DragEvent, onMove: (dx: number, dy: number, ev: MouseEvent) => void, onEnd?: () => void): () => void {
  e.preventDefault()
  e.stopPropagation?.()
  const x0 = e.clientX
  const y0 = e.clientY
  const move = (ev: MouseEvent) => onMove(ev.clientX - x0, ev.clientY - y0, ev)
  const stop = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    document.body.classList.remove('resizing')
  }
  const up = () => {
    stop()
    onEnd?.()
  }
  document.body.classList.add('resizing')
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
  return stop
}

export interface DragResizeOptions {
  /** Current size when the drag starts. */
  get: () => number
  /** New size while dragging (already clamped to min/max). */
  set: (size: number) => void
  /** Called once on release (e.g. persist the size). */
  onEnd?: () => void
  min?: number
  /** Upper bound; a function is evaluated when the drag starts (e.g. from the container width). */
  max?: number | (() => number)
  /** 'x' (default) or 'y'. */
  axis?: 'x' | 'y'
  /** The handle is on the left/top edge: dragging towards it grows the size (drawers on the right). */
  invert?: boolean
}

/**
 * Drag-to-resize handle: returns the `onMouseDown` handler for the splitter element.
 * Example: `const onMouseDown = useDragResize({ get: () => width, set: (w) => setWidth(w), min: 200, max: 480 })`.
 * Listeners are removed if the component unmounts mid-drag.
 */
export function useDragResize(opts: DragResizeOptions) {
  const o = useRef(opts)
  o.current = opts
  const abort = useRef<(() => void) | undefined>(undefined)
  useEffect(() => () => abort.current?.(), [])
  return useCallback((e: DragEvent) => {
    const { get, min = 0, max, axis = 'x', invert } = o.current
    const s0 = get()
    const hi = typeof max === 'function' ? max() : max ?? Infinity
    abort.current?.()
    abort.current = beginDrag(
      e,
      (dx, dy) => {
        const d = (axis === 'x' ? dx : dy) * (invert ? -1 : 1)
        o.current.set(Math.max(min, Math.min(hi, s0 + d)))
      },
      () => {
        abort.current = undefined
        o.current.onEnd?.()
      },
    )
  }, [])
}
