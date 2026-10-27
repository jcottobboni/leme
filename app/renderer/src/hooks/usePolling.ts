import { useEffect, useRef } from 'react'
import { usePanelVisible } from './visibility'

export interface PollingOptions {
  /** Also run once on mount (and whenever `ms` changes). Default false: the caller loads first on its own. */
  immediate?: boolean
  /** Poll even while hidden (rare: e.g. something that must keep a session alive). Default false. */
  whileHidden?: boolean
}

/**
 * Calls `fn` every `ms` while the component is on screen (its dock panel is the visible tab and its window is not
 * hidden). Hidden panels do not poll; when one becomes visible again and a tick was missed, `fn` runs once right
 * away and the interval restarts. `ms <= 0` disables polling. `fn` may change between renders (kept in a ref).
 */
export function usePolling(fn: () => unknown, ms: number, opts: PollingOptions = {}): { markRun: () => void } {
  const visible = usePanelVisible()
  const active = ms > 0 && (visible || !!opts.whileHidden)
  const cb = useRef(fn)
  cb.current = fn
  const last = useRef(0)
  const immediate = !!opts.immediate

  useEffect(() => {
    if (!immediate || ms <= 0) return
    last.current = Date.now()
    cb.current()
  }, [immediate, ms])

  useEffect(() => {
    if (!active) return
    const tick = () => {
      last.current = Date.now()
      cb.current()
    }
    // Back on screen after missing at least one tick: refresh now instead of waiting a whole interval.
    if (last.current && Date.now() - last.current >= ms) tick()
    else if (!last.current) last.current = Date.now()
    const t = setInterval(tick, ms)
    return () => clearInterval(t)
  }, [active, ms])

  // Lets the caller report a run it made on its own (e.g. a reload on deps change), so returning to the panel
  // right after it does not refresh again.
  const markRun = useRef(() => {
    last.current = Date.now()
  }).current
  return { markRun }
}
