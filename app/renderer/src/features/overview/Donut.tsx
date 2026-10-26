export interface Slice {
  label: string
  value: number
  color: string
}

/** Donut chart (plain SVG). The center shows the total or a custom label. */
export function Donut({ slices, size = 84, thickness = 10, center, sub }: { slices: Slice[]; size?: number; thickness?: number; center?: string; sub?: string }) {
  const r = (size - thickness) / 2
  const c = 2 * Math.PI * r
  const total = slices.reduce((a, s) => a + s.value, 0)
  let offset = 0
  return (
    <svg className="donut" width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label={slices.map((s) => `${s.label}: ${s.value}`).join(', ')}>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--track)" strokeWidth={thickness} />
      {total > 0 &&
        slices.filter((s) => s.value > 0).map((s) => {
          const len = (s.value / total) * c
          const el = (
            <circle key={s.label} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={s.color} strokeWidth={thickness}
              strokeDasharray={`${Math.max(0, len - (slices.length > 1 ? 1.5 : 0))} ${c}`} strokeDashoffset={-offset}
              transform={`rotate(-90 ${size / 2} ${size / 2})`}>
              <title>{`${s.label}: ${s.value}`}</title>
            </circle>
          )
          offset += len
          return el
        })}
      <text x="50%" y={sub ? '47%' : '50%'} textAnchor="middle" dominantBaseline="central" fontSize={size / 4.6}>{center ?? total}</text>
      {sub && <text className="sub" x="50%" y="67%" textAnchor="middle" dominantBaseline="central" fontSize={size / 9}>{sub}</text>}
    </svg>
  )
}

/** Usage gauge: ring of usage over allocatable, colored by pressure. */
export function Gauge({ value, max, size = 96, label }: { value?: number; max: number; size?: number; label: string }) {
  const p = value !== undefined && max > 0 ? Math.min(1, value / max) : 0
  const color = p >= 0.9 ? 'var(--error)' : p >= 0.7 ? 'var(--warn)' : 'var(--ok)'
  return (
    <Donut size={size} thickness={11} center={value === undefined ? '—' : `${Math.round(p * 100)}%`} sub={label}
      slices={[{ label: 'uso', value: p, color }, { label: 'livre', value: 1 - p, color: 'transparent' }]} />
  )
}
