import { useEffect, useRef } from 'react'
import { openSocket } from '../api'

export type SocketQuery = Record<string, string | number | boolean | undefined>

/** Passed to the handlers: control of the current connection. */
export interface SocketControl {
  /** Next reconnect starts again from the shortest delay (call it when the stream proved healthy, e.g. on a snapshot). */
  resetBackoff: () => void
  /** The socket in use right now. */
  socket: () => WebSocket | undefined
}

export interface SocketOptions<M = unknown> {
  /** One decoded message (JSON by default; raw `ev.data` with `json: false`). Malformed JSON is ignored. */
  onMessage: (msg: M, ctl: SocketControl) => void
  onOpen?: (ctl: SocketControl) => void
  /** Every close, including the ones followed by a reconnect. */
  onClose?: (ev: CloseEvent) => void
  /** Default true. */
  json?: boolean
  /** Reconnect with exponential backoff after a close (default true). */
  reconnect?: boolean
  /** First delay in ms (default 500); doubles per failed attempt up to `base * 2 ** maxRetry`. */
  base?: number
  /** Cap of the backoff exponent (default 6, i.e. 32 s with base 500). */
  maxRetry?: number
}

/**
 * Opens an authenticated engine WebSocket (`openSocket`) and keeps it open: reconnects with exponential backoff,
 * reopens when `path` or `query` change, closes on unmount. `path` null/false/'' means disabled. Handlers can change
 * between renders (kept in a ref); only `path` and the values of `query` restart the connection.
 */
export function useSocket<M = unknown>(path: string | null | false | undefined, query: SocketQuery, opts: SocketOptions<M>) {
  const h = useRef(opts)
  h.current = opts
  const qkey = JSON.stringify(query)

  useEffect(() => {
    if (!path) return
    const q = JSON.parse(qkey) as SocketQuery
    let ws: WebSocket | undefined
    let closed = false
    let retry = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const ctl: SocketControl = {
      resetBackoff: () => {
        retry = 0
      },
      socket: () => ws,
    }
    const open = () => {
      const s = openSocket(path, q)
      ws = s
      s.onopen = () => h.current.onOpen?.(ctl)
      s.onmessage = (ev) => {
        let msg: unknown = ev.data
        if (h.current.json !== false) {
          try {
            msg = JSON.parse(ev.data)
          } catch {
            return
          }
        }
        h.current.onMessage(msg as M, ctl)
      }
      s.onclose = (ev) => {
        if (closed) return
        h.current.onClose?.(ev)
        if (h.current.reconnect === false) return
        retry = Math.min(retry + 1, h.current.maxRetry ?? 6)
        timer = setTimeout(() => !closed && open(), (h.current.base ?? 500) * 2 ** retry)
      }
    }
    open()
    return () => {
      closed = true
      clearTimeout(timer)
      ws?.close()
    }
  }, [path, qkey])
}
