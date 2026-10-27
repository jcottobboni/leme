// Small runtime probes for the map renderers: WebGL support and a live frame-rate sample.
import { useEffect, useRef } from 'react'

let gl: boolean | undefined
/** True when this window can create a WebGL context (cached). Headless/old GPUs without one keep the SVG map. */
export function webglAvailable(): boolean {
  if (gl !== undefined) return gl
  try {
    const c = document.createElement('canvas')
    gl = !!(c.getContext('webgl2') ?? c.getContext('webgl'))
  } catch {
    gl = false
  }
  return gl
}

/**
 * Frames per second of the window, sampled with requestAnimationFrame over the last second and written to
 * `data-fps` of the returned element ref (no React render). The e2e and the perf test read it.
 */
export function useFpsProbe<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  useEffect(() => {
    let raf = 0
    let idle = 0
    const stamps: number[] = []
    let last = 0
    const tick = (now: number) => {
      // Hidden (inactive dock tab, no element yet): poll slowly instead of waking every frame.
      if (!ref.current?.offsetParent) {
        stamps.length = 0
        idle = window.setTimeout(() => { raf = requestAnimationFrame(tick) }, 500)
        return
      }
      stamps.push(now)
      while (stamps.length && now - stamps[0] > 1000) stamps.shift()
      if (now - last > 250 && ref.current) {
        last = now
        const span = stamps.length > 1 ? stamps[stamps.length - 1] - stamps[0] : 0
        ref.current.dataset.fps = span > 0 ? (((stamps.length - 1) * 1000) / span).toFixed(1) : '0'
      }
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => {
      cancelAnimationFrame(raf)
      window.clearTimeout(idle)
    }
  }, [])
  return ref
}
