export function age(iso?: string): string {
  if (!iso) return ''
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h${m % 60 ? `${m % 60}m` : ''}`
  const d = Math.floor(h / 24)
  return d < 365 ? `${d}d` : `${Math.floor(d / 365)}y${d % 365}d`
}

export function cpu(milli?: number): string {
  if (milli === undefined) return ''
  return milli >= 1000 ? `${+(milli / 1000).toFixed(2)}` : `${milli}m`
}

export function bytes(n?: number): string {
  if (n === undefined) return ''
  const u = ['B', 'Ki', 'Mi', 'Gi', 'Ti']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 || i === 0 ? v.toFixed(0) : v.toFixed(1)}${u[i]}`
}

export function healthOfStatus(s: string): 'ok' | 'warn' | 'error' | 'neutral' {
  if (/^(Running|Ready|Active|Bound|Healthy|Complete|Succeeded|True|Admitted)$/.test(s)) return 'ok'
  if (/^(Completed|Scaled to 0|Available|Normal)$/.test(s)) return 'neutral'
  if (/(BackOff|Err|Fail|Unknown|NotReady|Lost|Degraded|Blocked|OOM|Evicted|Warning|Rejected)/.test(s)) return 'error'
  if (s === 'Cancelled') return 'neutral'
  if (!s) return 'neutral'
  return 'warn'
}
