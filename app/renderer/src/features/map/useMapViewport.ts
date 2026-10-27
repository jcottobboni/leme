import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react'
import type { GlStageHandle } from '../map-webgl/GlStage'
import type { Layout } from './layout'
import type { VP } from './geometry'

export interface View { x: number; y: number; k: number }

export const MAX_K = 14

/**
 * Pan/zoom state of the map stage. The live view is in `viewRef` and is applied straight to the DOM (SVG transform,
 * WebGL stage, zoom label, backdrop, minimap rectangle); React state (`view`) follows at most every ~80 ms, which is
 * what drives culling and level of detail.
 */
export function useMapViewport({ layout, ready, minimap, glRef }: { layout: Layout | null; ready: boolean; minimap: boolean; glRef: RefObject<GlStageHandle | null> }) {
  const stageRef = useRef<HTMLDivElement>(null)
  const gRef = useRef<SVGGElement>(null)
  const zoomRef = useRef<HTMLDivElement>(null)
  const miniRef = useRef<SVGRectElement>(null)
  const viewRef = useRef<View>({ x: 0, y: 0, k: 1 })
  const [view, setView] = useState<View>(viewRef.current)
  const [size, setSize] = useState({ w: 0, h: 0 })
  const syncTimer = useRef<number>(0)
  const anim = useRef<number>(0)

  const apply = useCallback(() => {
    const v = viewRef.current
    gRef.current?.setAttribute('transform', `translate(${v.x} ${v.y}) scale(${v.k})`)
    glRef.current?.setView(v)
    if (zoomRef.current) zoomRef.current.textContent = `${Math.round(v.k * 100)}%`
    const st = stageRef.current
    if (st) {
      // The dotted backdrop pans with the map (a cheap depth cue).
      st.style.backgroundPosition = `${v.x}px ${v.y}px`
      const r = miniRef.current
      if (r) {
        r.setAttribute('x', String(-v.x / v.k)); r.setAttribute('y', String(-v.y / v.k))
        r.setAttribute('width', String(st.clientWidth / v.k)); r.setAttribute('height', String(st.clientHeight / v.k))
      }
    }
    if (!syncTimer.current) {
      syncTimer.current = window.setTimeout(() => {
        syncTimer.current = 0
        setView({ ...viewRef.current })
      }, 80)
    }
  }, [glRef])

  /** Stops a running animation (before a pan or a jump). */
  const stop = useCallback(() => cancelAnimationFrame(anim.current), [])

  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setSize({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [ready])
  useEffect(() => () => { window.clearTimeout(syncTimer.current); cancelAnimationFrame(anim.current) }, [])
  useEffect(() => { if (minimap) apply() }, [minimap, layout, size, apply])

  const animateTo = useCallback((to: View, ms = 380) => {
    cancelAnimationFrame(anim.current)
    const from = { ...viewRef.current }
    if (!ms) {
      viewRef.current = to
      apply()
      return
    }
    const t0 = performance.now()
    const step = (now: number) => {
      const p = Math.min(1, (now - t0) / ms), e = 1 - Math.pow(1 - p, 3)
      // Interpolate the zoom in log space so it feels even.
      const k = Math.exp(Math.log(from.k) + (Math.log(to.k) - Math.log(from.k)) * e)
      viewRef.current = { k, x: from.x + (to.x - from.x) * e, y: from.y + (to.y - from.y) * e }
      apply()
      if (p < 1) anim.current = requestAnimationFrame(step)
    }
    anim.current = requestAnimationFrame(step)
  }, [apply])

  const centerOn = useCallback((wx: number, wy: number, k: number): View => ({ k, x: size.w / 2 - wx * k, y: size.h / 2 - wy * k }), [size])
  const fit = useCallback((ms = 380) => {
    if (!layout || !layout.islands.length || !size.w) return
    const b = layout.bounds
    const bw = b.x1 - b.x0, bh = b.y1 - b.y0
    const k = Math.min(1.2, Math.min(size.w / bw, size.h / bh) * 0.92)
    animateTo(centerOn((b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2, k), ms)
  }, [layout, size, animateTo, centerOn])
  const minK = useMemo(() => {
    if (!layout || !layout.islands.length || !size.w) return 0.01
    const b = layout.bounds
    return Math.min(size.w / (b.x1 - b.x0), size.h / (b.y1 - b.y0)) * 0.4
  }, [layout, size])
  const zoomAt = useCallback((factor: number, px: number, py: number, ms = 0) => {
    const v = viewRef.current
    const k = Math.min(MAX_K, Math.max(minK, v.k * factor))
    const to = { k, x: px - (px - v.x) * (k / v.k), y: py - (py - v.y) * (k / v.k) }
    if (ms) animateTo(to, ms)
    else {
      cancelAnimationFrame(anim.current)
      viewRef.current = to
      apply()
    }
  }, [minK, apply, animateTo])
  const zoomToBox = useCallback((x: number, y: number, r: number, maxK = MAX_K) => {
    const k = Math.min(maxK, (Math.min(size.w, size.h) * 0.42) / r)
    animateTo(centerOn(x, y, Math.max(minK, k)))
  }, [size, animateTo, centerOn, minK])
  /** Moves the view at once (minimap jump, drag pan). */
  const jumpTo = useCallback((to: View) => {
    cancelAnimationFrame(anim.current)
    viewRef.current = to
    apply()
  }, [apply])

  // Wheel zoom needs a non-passive listener.
  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      zoomAt(Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0016)), e.clientX - r.left, e.clientY - r.top)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [zoomAt, ready])

  // Visible world rectangle (with a margin so a short pan does not reveal blanks before the next sync).
  const vp = useMemo<VP>(() => {
    const m = 0.5
    return { x0: (-view.x - size.w * m) / view.k, y0: (-view.y - size.h * m) / view.k, x1: (-view.x + size.w * (1 + m)) / view.k, y1: (-view.y + size.h * (1 + m)) / view.k }
  }, [view, size])

  return { stageRef, gRef, zoomRef, miniRef, viewRef, view, size, vp, minK, apply, stop, animateTo, centerOn, fit, zoomAt, zoomToBox, jumpTo }
}
