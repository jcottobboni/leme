// Small geometry and text helpers shared by the SVG stage (MapPanel) and the WebGL scene (map-webgl/scene.ts).

/** Visible world rectangle. */
export interface VP { x0: number; y0: number; x1: number; y1: number }

/** Whether a circle of radius `r` at (x, y) intersects the visible rectangle. */
export const seen = (vp: VP, x: number, y: number, r: number) => x + r >= vp.x0 && x - r <= vp.x1 && y + r >= vp.y0 && y - r <= vp.y1

/** Truncates to `n` characters with an ellipsis (nothing when there is no room for one character and the ellipsis). */
export const trunc = (s: string, n: number) => (s.length <= n ? s : n <= 1 ? '' : s.slice(0, n - 1) + '…')

/** SVG path of a pointy-top hexagon of radius `r` centered at (cx, cy). */
export const hexPath = (r: number, cx = 0, cy = 0) => Array.from({ length: 6 }, (_, i) => {
  const a = (Math.PI / 180) * (60 * i - 90)
  return `${i ? 'L' : 'M'}${(cx + r * Math.cos(a)).toFixed(2)} ${(cy + r * Math.sin(a)).toFixed(2)}`
}).join('') + 'Z'
