// React host of the WebGL map stage. MapView keeps the view, toolbar, tooltip and pointer handling; this
// component owns the PixiJS scene and exposes setView (every frame) and hitAt (spatial grid) through `handle`.
import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from 'react'
import type { Layout, Match } from '../map/layout'
import { kindIcon } from '../shell/nav'
import type { EdgeL } from './edges'
import type { Hit, MapScene, SceneView } from './scene'
import './mgl.css'

export interface GlStageHandle {
  setView(v: SceneView): void
  hitAt(x: number, y: number): Hit | null
}

interface Props {
  handle: Ref<GlStageHandle>
  layout: Layout
  match: Match | null
  edges: EdgeL[]
  hover: Hit | null
  /** The settled view (MapView syncs it ~80 ms after pan/zoom): culling and LOD are recomputed on change. */
  view: SceneView
  /** The live view, read once when the scene starts. */
  current: () => SceneView
  size: { w: number; h: number }
  theme: string
  /** WebGL could not start (no context, driver error): MapView switches to the SVG stage. */
  onFail: (reason: string) => void
  /** First frame with the layout is on screen. */
  onReady: () => void
}

export function GlStage({ handle, layout, match, edges, hover, view, current, size, theme, onFail, onReady }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const iconsRef = useRef<HTMLDivElement>(null)
  const [scene, setScene] = useState<MapScene | null>(null)
  const cb = useRef({ onFail, onReady, current, size })
  cb.current = { onFail, onReady, current, size }

  useImperativeHandle(handle, () => ({
    setView: (v) => scene?.setView(v),
    hitAt: (x, y) => scene?.hitAt(x, y) ?? null,
  }), [scene])

  useEffect(() => {
    const el = host.current
    if (!el) return
    let alive = true
    let made: MapScene | null = null
    import('./scene')
      .then(({ MapScene }) => MapScene.create(el, cb.current.size.w, cb.current.size.h))
      .then((s) => {
        made = s
        if (!alive) return s.destroy()
        s.setView(cb.current.current())
        setScene(s)
      })
      .catch((e: unknown) => { if (alive) cb.current.onFail((e as Error)?.message || 'WebGL indisponível') })
    return () => {
      alive = false
      made?.destroy()
    }
  }, [])

  useEffect(() => {
    if (!scene) return
    scene.setLayout(layout)
    let first = true
    scene.onFrame = () => {
      if (!first) return
      first = false
      cb.current.onReady()
    }
  }, [scene, layout])
  useEffect(() => { scene?.setMatch(match) }, [scene, match])
  useEffect(() => { scene?.setEdges(edges) }, [scene, edges])
  useEffect(() => { scene?.setHover(hover) }, [scene, hover])
  useEffect(() => { scene?.resize(size.w, size.h) }, [scene, size])
  // One culling/LOD pass after any of the inputs above changed.
  useEffect(() => { scene?.sync() }, [scene, layout, match, edges, hover, view, size])
  // Theme tokens are applied to <html> in an effect; read them on the next frame.
  useEffect(() => {
    if (!scene) return
    const r = requestAnimationFrame(() => scene.setTheme())
    return () => cancelAnimationFrame(r)
  }, [scene, theme])

  // Kind icons: the same Lucide glyphs as the SVG stage, rendered hidden once and rasterized by the scene.
  const kinds = useMemo(() => [...new Set(layout.tiles.map((t) => t.w.kind))].sort(), [layout])
  useEffect(() => {
    const box = iconsRef.current
    if (!scene || !box) return
    const out: Record<string, string> = {}
    box.querySelectorAll<HTMLElement>('[data-kind]').forEach((n) => {
      const svg = n.querySelector('svg')
      if (!svg) return
      const c = svg.cloneNode(true) as SVGElement
      c.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
      c.setAttribute('stroke', '#ffffff')
      out[n.dataset.kind!] = c.outerHTML.replace(/currentColor/g, '#ffffff')
    })
    void scene.setIcons(out)
  }, [scene, kinds])

  return (
    <>
      <div ref={host} className="mgl-host" data-testid="map-gl" />
      <div ref={iconsRef} className="mgl-icons" aria-hidden>
        {kinds.map((k) => {
          const Icon = kindIcon(k)
          return <span key={k} data-kind={k}><Icon size={64} strokeWidth={1.7} /></span>
        })}
      </div>
    </>
  )
}
