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

/** Absolute local date and time, for the tooltip of a relative age ("há 3h"). */
export function absDate(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

/** "há 3h" (or "agora" under a few seconds). */
export function ago(iso?: string): string {
  if (!iso) return ''
  const a = age(iso)
  return /^\d+s$/.test(a) && parseInt(a) < 5 ? 'agora' : `há ${a}`
}

/** CPU in millicores: "250m" below one core, "1.5 cores" above (never a bare number). */
export function cpu(milli?: number): string {
  if (milli === undefined) return ''
  if (milli < 1000) return `${milli}m`
  const c = +(milli / 1000).toFixed(2)
  return `${c} ${c === 1 ? 'core' : 'cores'}`
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
  if (/^(Running|Ready|Active|Bound|Healthy|Complete|Succeeded|True|Admitted|Disponível|Saudável|Pronto|Ativo)$/.test(s)) return 'ok'
  if (/^(Completed|Scaled to 0|Available|Normal|Pausado|Sem réplicas|Concluído)$/.test(s)) return 'neutral'
  if (/(BackOff|Err|Fail|Unknown|NotReady|Lost|Degraded|Blocked|OOM|Evicted|Warning|Rejected|Travado|Degradado|Falh|Erro)/.test(s)) return 'error'
  if (s === 'Cancelled') return 'neutral'
  if (!s) return 'neutral'
  return 'warn'
}
