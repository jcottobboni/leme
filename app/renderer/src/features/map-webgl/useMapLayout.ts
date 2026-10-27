// Map layout: computed synchronously for small maps and in a Web Worker at/above the WebGL threshold.
import { useEffect, useMemo, useRef, useState } from 'react'
import type { ClusterMap } from '../../api'
import { layoutMap, type Layout, type LayoutOpts } from '../map/layout'

export interface LayoutRequest { seq: number; map: ClusterMap; selected: string[]; hideSystem: boolean; onlyProblems: boolean }
export interface LayoutReply { seq: number; layout?: Layout; error?: string }

let worker: Worker | null | undefined
function getWorker(): Worker | null {
  if (worker !== undefined) return worker
  try {
    worker = new Worker(new URL('./layout.worker.ts', import.meta.url), { type: 'module' })
  } catch {
    worker = null // no workers (CSP, old runtime): the layout runs on the main thread
  }
  return worker
}

let nextSeq = 0

/**
 * Layout of `map` for the filters. With `background` the work moves to the worker; the previous layout
 * stays on screen meanwhile and `pending` is true. Any worker failure falls back to the main thread.
 */
export function useMapLayout(map: ClusterMap | undefined, o: LayoutOpts, background: boolean): { layout: Layout | null; pending: boolean } {
  const sync = useMemo(() => (map && !background ? layoutMap(map, o) : null), [map, background, o])
  const [bg, setBg] = useState<{ layout: Layout | null; pending: boolean }>({ layout: null, pending: false })
  const mine = useRef(0)
  useEffect(() => {
    if (!map || !background) return
    const seq = ++nextSeq
    mine.current = seq
    const local = () => {
      if (mine.current === seq) setBg({ layout: layoutMap(map, o), pending: false })
    }
    const w = getWorker()
    if (!w) return local()
    setBg({ layout: null, pending: true }) // until it answers, the last layout shown stays (see below)
    const onMsg = (e: MessageEvent<LayoutReply>) => {
      if (e.data.seq !== seq) return
      if (e.data.layout) {
        if (mine.current === seq) setBg({ layout: e.data.layout, pending: false })
      } else local()
    }
    const onErr = () => {
      worker = null // broken worker (load error): do not try it again
      local()
    }
    w.addEventListener('message', onMsg)
    w.addEventListener('error', onErr)
    try {
      w.postMessage({ seq, map, selected: [...o.selected], hideSystem: o.hideSystem, onlyProblems: o.onlyProblems } satisfies LayoutRequest)
    } catch {
      local()
    }
    return () => {
      w.removeEventListener('message', onMsg)
      w.removeEventListener('error', onErr)
    }
  }, [map, background, o])
  // While the worker runs, keep showing the last layout (from either path) instead of unmounting the stage.
  const last = useRef<Layout | null>(null)
  const out = background ? { layout: bg.layout ?? last.current, pending: bg.pending || (!!map && !bg.layout) } : { layout: sync, pending: false }
  if (out.layout) last.current = out.layout
  return out
}
