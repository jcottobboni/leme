import type { KeyboardEvent, MouseEvent } from 'react'

/**
 * Props that make a non-button element (div, span, tr) work from the keyboard: focusable and
 * activated with Enter or Space. `role` defaults to "button"; table rows pass `null` to keep theirs.
 */
export function pressable(fn: (e?: MouseEvent | KeyboardEvent) => void, role: string | null = 'button') {
  return {
    ...(role ? { role } : {}),
    tabIndex: 0,
    onClick: (e: MouseEvent) => fn(e),
    onKeyDown: (e: KeyboardEvent) => {
      if (e.target !== e.currentTarget) return
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault()
        fn(e)
      }
    },
  }
}
