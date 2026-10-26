import { useEffect, useRef } from 'react'
import { Terminal, type ITerminalOptions } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'

export interface XtermHandle {
  term: Terminal
  fit: FitAddon
  search: SearchAddon
}

const theme = { background: '#0b0d10', foreground: '#dde2e8', cursor: '#4aa3ff', selectionBackground: '#264f78' }

/** Mounts an xterm.js instance and keeps it fitted to its container. */
export function Xterm({ options, onReady }: { options?: ITerminalOptions; onReady: (h: XtermHandle) => () => void }) {
  const el = useRef<HTMLDivElement>(null)
  const ready = useRef(onReady)
  ready.current = onReady
  useEffect(() => {
    const term = new Terminal({ fontFamily: "'JetBrains Mono Variable', 'JetBrains Mono', monospace", fontSize: 13, theme, scrollback: 50000, ...options })
    const fit = new FitAddon()
    const search = new SearchAddon()
    term.loadAddon(fit)
    term.loadAddon(search)
    term.open(el.current!)
    const ro = new ResizeObserver(() => {
      try {
        fit.fit()
      } catch {
        /* not visible yet */
      }
    })
    ro.observe(el.current!)
    requestAnimationFrame(() => fit.fit())
    const cleanup = ready.current({ term, fit, search })
    return () => {
      cleanup()
      ro.disconnect()
      term.dispose()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  return <div className="xterm-host" ref={el} />
}
