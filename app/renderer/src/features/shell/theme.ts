import { useEffect } from 'react'
import { create } from 'zustand'
import { useApp, useUi } from '../../store'

export type ThemePref = 'system' | 'dark' | 'light'
export type Theme = 'dark' | 'light'

/** Resolved theme (system preference applied). Monaco and the dock follow it. */
export const useTheme = create<{ theme: Theme }>(() => ({ theme: resolve(useUi.getState().theme) }))

function resolve(pref: ThemePref): Theme {
  if (pref === 'system') return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  return pref
}

/** Applies the theme to a window (the main one or a popout). */
export function applyThemeTo(win: Window) {
  try {
    win.document.documentElement.dataset.theme = useTheme.getState().theme
  } catch {
    /* popout already closed */
  }
}

/** Applies the theme to the main window and to every popout window. */
export function applyThemeEverywhere() {
  applyThemeTo(window)
  for (const g of useApp.getState().dock?.groups ?? []) {
    if (g.api.location.type === 'popout') applyThemeTo(g.api.getWindow())
  }
}

export function useApplyTheme() {
  const pref = useUi((s) => s.theme)
  useEffect(() => {
    const update = () => {
      useTheme.setState({ theme: resolve(pref) })
      applyThemeEverywhere()
    }
    update()
    if (pref !== 'system') return
    const mq = window.matchMedia('(prefers-color-scheme: light)')
    mq.addEventListener('change', update)
    return () => mq.removeEventListener('change', update)
  }, [pref])
}

export function cycleTheme() {
  const order: ThemePref[] = ['system', 'dark', 'light']
  const cur = useUi.getState().theme
  useUi.getState().update({ theme: order[(order.indexOf(cur) + 1) % order.length] })
}
