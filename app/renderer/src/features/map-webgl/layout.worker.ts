// Runs layoutMap off the main thread for big maps (M5 map-webgl). Same function, same result as the sync path.
import { layoutMap } from '../map/layout'
import type { LayoutReply, LayoutRequest } from './useMapLayout'

const post = (r: LayoutReply) => (self as unknown as Worker).postMessage(r)

self.onmessage = (e: MessageEvent<LayoutRequest>) => {
  const { seq, map, selected, hideSystem, onlyProblems } = e.data
  try {
    post({ seq, layout: layoutMap(map, { selected: new Set(selected), hideSystem, onlyProblems }) })
  } catch (err) {
    post({ seq, error: (err as Error).message })
  }
}
