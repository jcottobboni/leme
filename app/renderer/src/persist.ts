// UI state that must survive restarts (preferences, cluster profiles, tab layout).
// In the desktop app the page origin changes on every launch (random engine port), so localStorage
// alone would start empty each time: the Electron shell keeps a copy on disk and this module
// mirrors it into localStorage before any store reads it. In the browser (`leme serve`) the origin
// is stable and localStorage is enough.

interface UiStateBridge {
  load(): Record<string, string>
  set(key: string, value: string | null): void
}

const bridge = (window as unknown as { leme?: { uiState?: UiStateBridge } }).leme?.uiState

function safely(fn: () => void) {
  try {
    fn()
  } catch {
    /* storage unavailable: keep defaults */
  }
}

if (bridge) safely(() => {
  for (const [k, v] of Object.entries(bridge.load())) localStorage.setItem(k, v)
})

/** Writes a persisted key (null removes it) to localStorage and, in the desktop app, to disk. */
export function persist(key: string, value: string | null) {
  safely(() => (value === null ? localStorage.removeItem(key) : localStorage.setItem(key, value)))
  bridge?.set(key, value)
}
