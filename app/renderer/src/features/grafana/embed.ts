// Opens a port-forwarded web UI (Grafana, Prometheus) in an isolated app window.
// Desktop: main process IPC "embed:open" (sandbox, no preload, in-memory session, navigation pinned to
// the forward's origin; only URLs of active forwards are accepted). Browser mode (`leme serve`): new tab.

interface EmbedBridge {
  open: (url: string, title: string) => Promise<{ ok: boolean; error?: string }>
}

const bridge = (window as unknown as { leme?: { embed?: EmbedBridge } }).leme?.embed

/** True inside the desktop app (the UI opens in an app window, not in the browser). */
export const embedInApp = !!bridge

export async function openEmbed(url: string, title: string): Promise<{ ok: boolean; error?: string }> {
  if (!/^http:\/\/127\.0\.0\.1:\d+(\/|$)/.test(url)) return { ok: false, error: 'only local port-forward URLs can be opened' }
  if (bridge) return bridge.open(url, title)
  window.open(url, '_blank', 'noopener,noreferrer')
  return { ok: true }
}
