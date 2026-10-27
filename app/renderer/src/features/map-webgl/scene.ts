// WebGL stage of the cluster map (PixiJS v8, loaded on demand). Draws the same layout as the SVG stage, with
// the same level-of-detail rules: islands -> dots -> hexagons + icon -> + name and pod bar -> pod squares.
//
// Pan/zoom only moves the world render group (a GPU transform). Culling, LOD, labels and bars are recomputed on
// `sync` (the view settles every ~80 ms, like the SVG stage). Hit testing uses a spatial grid instead of the DOM.
import type { Application, BitmapText, Container, Graphics, Sprite, Texture } from 'pixi.js'
import type { Health, MapWorkload } from '../../api'
import { BLOB_AMP, blobPolygon, dotRadius, islandSeed, appSeed, lodOf, TILE_R, type IslandL, type Layout, type Match, type TileL } from '../map/layout'
import { arrowOf, type EdgeL } from './edges'

type Pixi = typeof import('pixi.js')

export interface SceneView { x: number; y: number; k: number }
export type Hit = { kind: 'tile' | 'app' | 'isl'; id: number }

const HEALTHS: Health[] = ['ok', 'warn', 'error', 'neutral']
const FONT = 'Inter Variable, Inter, system-ui, sans-serif'
const BASE_FONT = 48 // bitmap font size; labels are scaled to their world size
const GRID = 120 // hit-test grid cell (world units)
const TEX_RES = 4

interface Colors {
  ok: number; warn: number; error: number; neutral: number; panel: number; raised: number; accent: number
  text: number; muted: number; bg: number; track: number; light: boolean
}

/** Resolves CSS colors (tokens, color-mix, …) to 0xRRGGBB through a 1x1 canvas. */
function cssColors(el: HTMLElement): Colors {
  const cs = getComputedStyle(el)
  const c = document.createElement('canvas')
  c.width = c.height = 1
  const ctx = c.getContext('2d', { willReadFrequently: true })
  const rd = (name: string, fb: number) => {
    const v = cs.getPropertyValue(name).trim()
    if (!v || !ctx) return fb
    ctx.clearRect(0, 0, 1, 1)
    ctx.fillStyle = '#000'
    ctx.fillStyle = v
    ctx.fillRect(0, 0, 1, 1)
    const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data
    return (r << 16) | (g << 8) | b
  }
  const light = (el.closest('[data-theme]') as HTMLElement | null)?.dataset.theme === 'light' || document.documentElement.dataset.theme === 'light'
  return {
    ok: rd('--ok', 0x3fb950), warn: rd('--warn', 0xd29922), error: rd('--error', 0xf85149), neutral: rd('--neutral', 0x8b949e),
    panel: rd('--panel', 0x161b22), raised: rd('--raised', 0x1c2128), accent: rd('--accent', 0x58a6ff), text: rd('--text-strong', 0xf0f6fc),
    muted: rd('--muted', 0x8b949e), bg: rd('--bg', 0x0d1117), track: rd('--track', 0x30363d), light,
  }
}

function hsl(h: number, s: number, l: number) {
  s /= 100
  l /= 100
  const f = (n: number) => {
    const k = (n + h / 30) % 12
    return Math.round(255 * (l - s * Math.min(l, 1 - l) * Math.max(-1, Math.min(k - 3, 9 - k, 1))))
  }
  return (f(0) << 16) | (f(8) << 8) | f(4)
}

const HEX_PTS = Array.from({ length: 6 }, (_, i) => {
  const a = (Math.PI / 180) * (60 * i - 90)
  return [TILE_R * Math.cos(a), TILE_R * Math.sin(a)]
}).flat()

const trunc = (s: string, n: number) => (s.length <= n ? s : n <= 1 ? '' : s.slice(0, n - 1) + '…')
const seen = (vp: { x0: number; y0: number; x1: number; y1: number }, x: number, y: number, r: number) => x + r >= vp.x0 && x - r <= vp.x1 && y + r >= vp.y0 && y - r <= vp.y1

/** A grow-only pool of display objects, reset on every sync. */
class Pool<T extends Container> {
  private items: T[] = []
  private used = 0
  constructor(private parent: Container, private make: () => T) {}
  reset() { this.used = 0 }
  take(): T {
    let it = this.items[this.used]
    if (!it) {
      it = this.make()
      this.items.push(it)
      this.parent.addChild(it)
    }
    this.used++
    it.visible = true
    return it
  }
  /** Hides what was not taken since reset. */
  end() { for (let i = this.used; i < this.items.length; i++) this.items[i].visible = false }
}

export class MapScene {
  private colors!: Colors
  private hex = new Map<Health, Texture>()
  private dot!: Texture
  private ring!: Texture
  private icons = new Map<string, Texture>()
  private world: Container
  private blobs: Container
  private regions: Container
  private labels: Container
  private tileLayer: Container
  private iconLayer: Container
  private ringLayer: Container
  private bars: Graphics
  private edgeG: Graphics
  private islLabelPool: Pool<BitmapText>
  private appLabelPool: Pool<BitmapText>
  private namePool: Pool<BitmapText>
  private subPool: Pool<BitmapText>
  private iconPool: Pool<Sprite>
  private ringPool: Pool<Sprite>

  private layout: Layout | null = null
  private match: Match | null = null
  private edges: EdgeL[] = []
  private hover: Hit | null = null
  private islG: Graphics[] = []
  private appG = new Map<number, Graphics>()
  private tiles: Sprite[] = []
  private shown: number[] = []
  private grid = new Map<number, number[]>()
  private tileIsl: Int32Array = new Int32Array(0)
  private view: SceneView = { x: 0, y: 0, k: 1 }
  private size = { w: 1, h: 1 }
  private raf = 0
  private dead = false
  /** Called after each rendered frame (the e2e reads data-ready from it). */
  onFrame?: () => void

  private constructor(private P: Pixi, private app: Application, private host: HTMLElement) {
    const C = P.Container
    this.world = new C({ isRenderGroup: true })
    app.stage.addChild(this.world)
    this.blobs = this.world.addChild(new C())
    this.regions = this.world.addChild(new C())
    this.labels = this.world.addChild(new C())
    this.tileLayer = this.world.addChild(new C())
    this.iconLayer = this.world.addChild(new C())
    this.ringLayer = this.world.addChild(new C())
    this.bars = this.world.addChild(new P.Graphics())
    const textLayer = this.world.addChild(new C())
    this.edgeG = this.world.addChild(new P.Graphics())
    const txt = (weight: '600' | '700') => () => new P.BitmapText({ text: '', anchor: 0.5, style: { fontFamily: FONT, fontSize: BASE_FONT, fill: 0xffffff, fontWeight: weight } })
    this.islLabelPool = new Pool(this.labels, txt('700'))
    this.appLabelPool = new Pool(this.labels, txt('600'))
    this.namePool = new Pool(textLayer, txt('600'))
    this.subPool = new Pool(textLayer, txt('600'))
    this.iconPool = new Pool(this.iconLayer, () => { const s = new P.Sprite(); s.anchor.set(0.5); return s })
    this.ringPool = new Pool(this.ringLayer, () => { const s = new P.Sprite(); s.anchor.set(0.5); return s })
    this.makeTextures()
  }

  /** Loads PixiJS and opens a WebGL renderer on `host`. Throws when WebGL cannot start (the caller falls back to SVG). */
  static async create(host: HTMLElement, w: number, h: number): Promise<MapScene> {
    const P = await import('pixi.js')
    // The app CSP has no 'unsafe-eval': install Pixi's eval-free shader/uniform paths.
    await import('pixi.js/unsafe-eval')
    const app = new P.Application()
    await app.init({
      preference: 'webgl', width: Math.max(1, w), height: Math.max(1, h), backgroundAlpha: 0, antialias: true,
      resolution: Math.min(2, window.devicePixelRatio || 1), autoDensity: true, autoStart: false, sharedTicker: false,
      failIfMajorPerformanceCaveat: false,
    })
    if (app.renderer.name !== 'webgl') {
      app.destroy(true)
      throw new Error('WebGL indisponível')
    }
    app.ticker?.stop()
    app.canvas.classList.add('mgl-canvas')
    host.appendChild(app.canvas)
    const s = new MapScene(P, app, host)
    s.size = { w: Math.max(1, w), h: Math.max(1, h) }
    return s
  }

  private gen(g: Graphics): Texture {
    const t = this.app.renderer.generateTexture({ target: g, resolution: TEX_RES, antialias: true })
    g.destroy()
    return t
  }

  private makeTextures() {
    const P = this.P
    for (const t of this.hex.values()) t.destroy(true)
    this.dot?.destroy(true)
    this.ring?.destroy(true)
    this.colors = cssColors(this.host)
    const c = this.colors
    for (const h of HEALTHS) {
      const g = new P.Graphics().poly(HEX_PTS).fill(c.panel).stroke({ width: 3, color: c[h], alpha: h === 'ok' ? 0.75 : 1, alignment: 0.5 })
      this.hex.set(h, this.gen(g))
    }
    this.dot = this.gen(new P.Graphics().circle(0, 0, 32).fill(0xffffff))
    this.ring = this.gen(new P.Graphics().poly(HEX_PTS).stroke({ width: 4, color: 0xffffff, alignment: 0.5 }).poly(HEX_PTS.map((v) => v * 1.12)).stroke({ width: 3, color: 0xffffff, alpha: 0.35, alignment: 0.5 }))
  }

  /** Kind icons as SVG markup (white strokes), rasterized once per kind. */
  async setIcons(markup: Record<string, string>) {
    for (const [kind, svg] of Object.entries(markup)) {
      if (this.icons.has(kind) || !svg) continue
      try {
        const img = new Image()
        img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg)
        await img.decode()
        if (this.dead) return
        this.icons.set(kind, this.P.Texture.from(img))
      } catch {
        /* no icon for this kind: the hexagon alone is drawn */
      }
    }
    this.sync()
  }

  setTheme() {
    if (this.dead) return
    if (JSON.stringify(cssColors(this.host)) === JSON.stringify(this.colors)) return
    this.makeTextures()
    if (this.layout) this.buildLayout()
    this.sync()
  }

  resize(w: number, h: number) {
    this.size = { w: Math.max(1, w), h: Math.max(1, h) }
    this.app.renderer.resize(this.size.w, this.size.h)
    this.invalidate()
  }

  setLayout(l: Layout) {
    this.layout = l
    this.buildLayout()
  }

  private buildLayout() {
    const l = this.layout!
    const P = this.P
    const c = this.colors
    for (const g of this.islG) g.destroy()
    for (const g of this.appG.values()) g.destroy()
    this.islG = []
    this.appG.clear()
    this.tileLayer.removeChildren().forEach((s) => s.destroy())
    for (const isl of l.islands) {
      const tint = c.light ? hsl(isl.hue, 66, 46) : hsl(isl.hue, 62, 55)
      const g = new P.Graphics().poly(blobPolygon(isl.x, isl.y, isl.r, islandSeed(isl.ns.name))).fill({ color: tint, alpha: 0.17 }).stroke({ width: 1.6, color: tint, alpha: 0.7, pixelLine: true })
      g.visible = false
      this.blobs.addChild(g)
      this.islG.push(g)
      for (const a of isl.apps) {
        if (!a.boxed) continue
        const ag = new P.Graphics().poly(blobPolygon(a.x, a.y, a.r, appSeed(isl.ns.name, a.name), 0.06, 20)).fill({ color: tint, alpha: 0.12 }).stroke({ width: 1, color: tint, alpha: 0.55, pixelLine: true })
        ag.visible = false
        this.regions.addChild(ag)
        this.appG.set(a.id, ag)
      }
    }
    // Tiles: one sprite each (textures are shared, so they batch).
    this.tiles = l.tiles.map((t) => {
      const s = new P.Sprite(this.hex.get(t.w.health) ?? this.hex.get('neutral')!)
      s.anchor.set(0.5)
      s.position.set(t.x, t.y)
      s.visible = false
      this.tileLayer.addChild(s)
      return s
    })
    this.shown = []
    // Spatial grid for hit tests, and tile -> island.
    this.grid.clear()
    const islByNs = new Map(l.islands.map((i) => [i.ns.name, i.id]))
    this.tileIsl = new Int32Array(l.tiles.length)
    for (const t of l.tiles) {
      const key = this.cell(Math.floor(t.x / GRID), Math.floor(t.y / GRID))
      const arr = this.grid.get(key)
      if (arr) arr.push(t.id)
      else this.grid.set(key, [t.id])
      this.tileIsl[t.id] = islByNs.get(t.ns) ?? -1
    }
  }

  private cell(cx: number, cy: number) { return (cx + 32768) * 65536 + (cy + 32768) }

  setMatch(m: Match | null) { this.match = m }
  setEdges(e: EdgeL[]) { this.edges = e }
  setHover(h: Hit | null) {
    if (h?.kind === this.hover?.kind && h?.id === this.hover?.id) return
    this.hover = h
  }

  /** Fast path for every pan/zoom frame: only the world transform changes. */
  setView(v: SceneView) {
    this.view = v
    this.world.position.set(v.x, v.y)
    this.world.scale.set(v.k)
    this.invalidate()
  }

  invalidate() {
    if (this.raf || this.dead) return
    this.raf = requestAnimationFrame(() => {
      this.raf = 0
      if (this.dead) return
      this.app.render()
      this.onFrame?.()
    })
  }

  /** Culling + LOD for the current view (margin of half a screen, like the SVG stage). Call when the view settles. */
  sync() {
    if (this.dead) return
    const v = this.view
    const l = this.layout
    if (!l) return
    const { w, h } = this.size
    const k = v.k
    const m = 0.5
    const vp = { x0: (-v.x - w * m) / k, y0: (-v.y - h * m) / k, x1: (-v.x + w * (1 + m)) / k, y1: (-v.y + h * (1 + m)) / k }
    const lod = lodOf(k)
    const dr = dotRadius(k)
    const match = this.match
    const c = this.colors
    for (const i of this.shown) this.tiles[i].visible = false
    this.shown = []
    for (const g of this.appG.values()) g.visible = false
    this.bars.clear()
    this.edgeG.clear()
    this.islLabelPool.reset(); this.appLabelPool.reset(); this.namePool.reset(); this.subPool.reset(); this.iconPool.reset(); this.ringPool.reset()

    for (const isl of l.islands) {
      const g = this.islG[isl.id]
      const vis = seen(vp, isl.x, isl.y, isl.r)
      g.visible = vis
      if (!vis) continue
      const dimIsl = !!match && !match.islands.has(isl.id) && !match.islandsWithTiles.has(isl.id)
      g.alpha = dimIsl ? 0.35 : 1
      this.islandHeader(isl, k, dimIsl)
      if (!(isl.r * k > 25)) continue
      for (const a of isl.apps) {
        if (!seen(vp, a.x, a.y, a.r)) continue
        const ag = this.appG.get(a.id)
        if (ag && a.r * k >= 18) {
          ag.visible = true
          ag.alpha = dimIsl ? 0.35 : 1
        }
        if (a.boxed && a.r * k >= 70 && a.name) {
          const lf = Math.min(Math.max(12, 11 / k), a.r * 0.14)
          this.label(this.appLabelPool, trunc(a.name.toUpperCase(), Math.floor((a.r * 1.5) / (lf * 0.7))), a.x, a.y - a.r + lf * 1.25 + 2 - lf * 0.35, lf, c.light ? hsl(isl.hue, 70, 24) : hsl(isl.hue, 85, 85), 0.85)
        }
        if (lod === 0) continue
        for (const t of a.tiles) {
          if (!seen(vp, t.x, t.y, 48)) continue
          this.drawTile(t, lod, dr, !!match && !match.tiles.has(t.id), !!match && match.tiles.has(t.id))
        }
      }
    }
    this.drawEdges(k)
    this.islLabelPool.end(); this.appLabelPool.end(); this.namePool.end(); this.subPool.end(); this.iconPool.end(); this.ringPool.end()
    this.invalidate()
  }

  private label(pool: Pool<BitmapText>, text: string, x: number, y: number, size: number, color: number, alpha = 1) {
    const t = pool.take()
    if (t.text !== text) t.text = text
    t.position.set(x, y)
    t.scale.set(size / BASE_FONT)
    t.tint = color
    t.alpha = alpha
    return t
  }

  private islandHeader(isl: IslandL, k: number, dim: boolean) {
    const lf = Math.min(Math.max(isl.font, 13 / k), isl.r * 0.2)
    const maxChars = Math.floor((isl.r * 1.5) / (lf * 0.7))
    if (maxChars < 3) return
    const ly = isl.y - isl.r * 0.72 + lf * 0.5
    const c = this.colors
    this.label(this.islLabelPool, trunc(isl.ns.name.toUpperCase(), maxChars), isl.x, ly - lf * 0.35, lf, c.light ? hsl(isl.hue, 70, 24) : hsl(isl.hue, 85, 85), dim ? 0.4 : 1)
    const bw = Math.min(isl.r * 1.0, Math.max(lf * 7, 60)), bh = lf * 0.34
    this.healthBar(isl.x - bw / 2, ly + lf * 0.45, bw, bh, isl.ns.pods)
  }

  private healthBar(x: number, y: number, w: number, h: number, p: MapWorkload['pods']) {
    if (!p.total) return
    const c = this.colors
    const g = this.bars
    g.roundRect(x, y, w, h, h / 2).fill({ color: c.track, alpha: 0.6 })
    const other = Math.max(0, p.total - p.ok - p.warn - p.error)
    let cx = x
    for (const [n, col] of [[p.ok, c.ok], [p.warn, c.warn], [p.error, c.error], [other, c.neutral]] as const) {
      if (!n) continue
      const sw = (n / p.total) * w
      g.rect(cx, y, sw, h).fill(col)
      cx += sw
    }
  }

  private drawTile(t: TileL, lod: number, dr: number, dim: boolean, match: boolean) {
    const s = this.tiles[t.id]
    const c = this.colors
    s.visible = true
    this.shown.push(t.id)
    const w = t.w
    if (lod === 1) {
      s.texture = this.dot
      s.tint = c[w.health] ?? c.neutral
      s.scale.set(dr / 32)
      s.alpha = dim ? 0.2 : 1
      return
    }
    s.texture = this.hex.get(w.health) ?? this.hex.get('neutral')!
    s.scale.set(1)
    s.tint = 0xffffff
    s.alpha = dim ? 0.18 : 1
    const hot = this.hover?.kind === 'tile' && this.hover.id === t.id
    if (match || hot) {
      const r = this.ringPool.take()
      r.texture = this.ring
      r.tint = match ? c.accent : c.text
      r.alpha = match ? 1 : 0.45
      r.position.set(t.x, t.y)
    }
    const icon = this.icons.get(w.kind)
    if (lod < 4 && icon) {
      const ic = this.iconPool.take()
      ic.texture = icon
      ic.position.set(t.x, t.y)
      ic.width = 22
      ic.height = 22
      ic.tint = c.text
      ic.alpha = dim ? 0.18 : 1
    }
    if (lod >= 4) this.podSquares(t, dim)
    if (lod >= 3) {
      if (!dim) this.healthBar(t.x - 22, t.y + TILE_R + 5, 44, 7, w.pods)
      this.label(this.namePool, trunc(w.name, 11), t.x, t.y + TILE_R + 22.5, 10, c.text, dim ? 0.18 : 1)
      if (lod >= 4) this.label(this.subPool, `${w.kind} · ${w.ready || `${w.pods.total} pods`}`, t.x, t.y + TILE_R + 35.5, 9.5, c.muted, dim ? 0.18 : 1)
    }
  }

  private podSquares(t: TileL, dim: boolean) {
    const p = t.w.pods
    if (!p.total || dim) return
    const c = this.colors
    const cap = 400
    const sc = p.total > cap ? cap / p.total : 1
    const other = Math.max(0, p.total - p.ok - p.warn - p.error)
    const groups: [number, number][] = [[c.error, p.error], [c.warn, p.warn], [c.ok, p.ok], [c.neutral, other]]
    const counts = groups.map(([col, n]) => [col, n ? Math.max(1, Math.round(n * sc)) : 0] as [number, number])
    const n = counts.reduce((a, [, v]) => a + v, 0)
    const cols = Math.ceil(Math.sqrt(n)), A = 38, cell = A / cols, s = cell * 0.82
    let i = 0
    for (const [col, cnt] of counts) {
      for (let j = 0; j < cnt; j++, i++) {
        const x = t.x - A / 2 + (i % cols) * cell + (cell - s) / 2, y = t.y - A / 2 - 3 + Math.floor(i / cols) * cell + (cell - s) / 2
        this.bars.rect(x, y, s, s)
      }
      if (cnt) this.bars.fill(col)
    }
  }

  private drawEdges(k: number) {
    if (!this.edges.length) return
    const g = this.edgeG
    const c = this.colors.accent
    for (const e of this.edges) {
      const a = arrowOf(e, k)
      g.moveTo(e.x0, e.y0).lineTo(a.ex, a.ey).stroke({ width: e.w / k, color: c, alpha: 0.75, cap: 'round' })
      g.poly([...a.tip]).fill({ color: c, alpha: 0.9 })
    }
  }

  /** What is under the stage point (sx, sy), with the same precedence as the SVG: tile, then app region, then island. */
  hitAt(sx: number, sy: number): Hit | null {
    const l = this.layout
    if (!l) return null
    const v = this.view
    const k = v.k
    const x = (sx - v.x) / k, y = (sy - v.y) / k
    const lod = lodOf(k)
    let isl: IslandL | undefined
    for (const i of l.islands) {
      if (Math.hypot(x - i.x, y - i.y) <= i.r * (1 - BLOB_AMP / 2)) { isl = i; break }
    }
    if (lod > 0) {
      const r = lod === 1 ? Math.max(dotRadius(k), 4 / k) : TILE_R
      const cx = Math.floor(x / GRID), cy = Math.floor(y / GRID)
      let best = -1, bd = Infinity
      for (let i = cx - 1; i <= cx + 1; i++) {
        for (let j = cy - 1; j <= cy + 1; j++) {
          for (const id of this.grid.get(this.cell(i, j)) ?? []) {
            const t = l.tiles[id]
            const d = Math.hypot(x - t.x, y - t.y)
            if (d <= r && d < bd) {
              const ti = l.islands[this.tileIsl[id]]
              if (ti && ti.r * k > 25) { best = id; bd = d }
            }
          }
        }
      }
      if (best >= 0) return { kind: 'tile', id: best }
    }
    if (!isl) return null
    if (isl.r * k > 25) {
      for (const a of isl.apps) {
        if (a.boxed && a.r * k >= 18 && Math.hypot(x - a.x, y - a.y) <= a.r) return { kind: 'app', id: a.id }
      }
    }
    return { kind: 'isl', id: isl.id }
  }

  destroy() {
    this.dead = true
    cancelAnimationFrame(this.raf)
    // Only our own textures: bitmap fonts are cached by Pixi across scenes and must survive.
    this.app.destroy({ removeView: true }, { children: true })
    for (const t of this.icons.values()) t.destroy(true)
    for (const t of this.hex.values()) t.destroy(true)
    this.dot?.destroy(true)
    this.ring?.destroy(true)
  }
}
