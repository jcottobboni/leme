import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import type { DockviewPanelApi } from 'dockview-react'

// Whether the dock panel that renders a component is on screen. `undefined` outside a dock panel.
const PanelVisibleContext = createContext<boolean | undefined>(undefined)

/** Tracks `document.hidden` of a window (the main one by default; a popout has its own document). */
export function useDocumentVisible(win: Window = window): boolean {
  const [visible, setVisible] = useState(() => !win.document.hidden)
  useEffect(() => {
    const doc = win.document
    const on = () => setVisible(!doc.hidden)
    on()
    doc.addEventListener('visibilitychange', on)
    return () => doc.removeEventListener('visibilitychange', on)
  }, [win])
  return visible
}

function panelWindow(api: DockviewPanelApi): Window {
  try {
    return api.group?.api.getWindow() ?? window
  } catch {
    return window
  }
}

/**
 * Feeds the visibility of a dock panel to its subtree: the panel is the active tab of its group and its window
 * (main or popout) is not hidden. Wrapped around every dock component in App.tsx.
 */
export function PanelVisibilityProvider({ api, children }: { api: DockviewPanelApi; children: ReactNode }) {
  const [shown, setShown] = useState(api.isVisible)
  const [win, setWin] = useState<Window>(() => panelWindow(api))
  useEffect(() => {
    setShown(api.isVisible)
    setWin(panelWindow(api))
    const a = api.onDidVisibilityChange((e) => setShown(e.isVisible))
    // Moving to/from a popout window changes the document whose visibility counts.
    const b = api.onDidLocationChange(() => setWin(panelWindow(api)))
    return () => {
      a.dispose()
      b.dispose()
    }
  }, [api])
  const docVisible = useDocumentVisible(win)
  return <PanelVisibleContext.Provider value={shown && docVisible}>{children}</PanelVisibleContext.Provider>
}

/** True while the component is on screen: its dock panel is visible (if any) and its window is not hidden. */
export function usePanelVisible(): boolean {
  const panel = useContext(PanelVisibleContext)
  const doc = useDocumentVisible()
  return panel ?? doc
}
