// Settings popover of the cluster map (WebGL threshold, hide empty namespaces).
import { useEffect, useState } from 'react'
import { Info } from 'lucide-react'
import { useUi } from '../../store'

export function MapSettings({ threshold, renderer, total, onClose }: { threshold: number; renderer: 'svg' | 'webgl'; total: number; onClose: () => void }) {
  const [draft, setDraft] = useState(String(threshold))
  useEffect(() => setDraft(String(threshold)), [threshold])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    // Capture: other Escape handlers of the shell must not swallow it first.
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])
  const commit = () => {
    const n = Math.round(Number(draft))
    if (Number.isFinite(n) && n >= 1 && n <= 1_000_000) useUi.getState().update({ mapWebglThreshold: n })
    else setDraft(String(threshold))
  }
  return (
    <div className="mgl-settings" data-testid="map-settings-pop">
      <b>Mapa do cluster</b>
      <label>
        <span>Usar WebGL a partir de</span>
        <input type="number" min={1} step={100} data-testid="map-webgl-threshold" value={draft} onChange={(e) => setDraft(e.target.value)} onBlur={commit}
          onKeyDown={(e) => { if (e.key === 'Enter') commit() }} />
      </label>
      <label className="mgl-check">
        <input type="checkbox" data-testid="map-hide-empty" checked={useUi.getState().mapHideEmpty} onChange={(e) => useUi.getState().update({ mapHideEmpty: e.target.checked })} />
        <span>Ocultar namespaces sem workloads</span>
      </label>
      <div className="mgl-sub">
        <Info size={12} style={{ verticalAlign: -2 }} /> Agora: <span className="mgl-kind">{renderer === 'webgl' ? 'WebGL' : 'SVG'}</span> com {total} workloads. Abaixo do limiar o mapa usa SVG; a partir dele, WebGL (sem WebGL, continua em SVG).
      </div>
    </div>
  )
}
