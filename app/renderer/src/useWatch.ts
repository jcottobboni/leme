import { useEffect, useRef, useState } from 'react'
import { openSocket, type Column, type Row } from './api'

interface WatchState {
  columns: Column[]
  rows: Row[]
  loading: boolean
  error?: string
}

/** Subscribes to a live resource list. Rows are kept in a Map and re-emitted at most once per frame. */
export function useWatch(clusterId: string, group: string, resource: string, namespace: string, enabled = true): WatchState {
  const [state, setState] = useState<WatchState>({ columns: [], rows: [], loading: true })
  const rowsRef = useRef(new Map<string, Row>())

  useEffect(() => {
    if (!enabled) return
    let ws: WebSocket | undefined
    let closed = false
    let retry = 0
    let raf = 0
    let columns: Column[] = []
    rowsRef.current = new Map()
    setState({ columns: [], rows: [], loading: true })

    const emit = () => {
      if (raf) return
      raf = requestAnimationFrame(() => {
        raf = 0
        setState((s) => ({ ...s, columns, rows: Array.from(rowsRef.current.values()), loading: false }))
      })
    }

    const open = () => {
      ws = openSocket(`/api/clusters/${clusterId}/watch`, { group, resource, namespace })
      ws.onmessage = (ev) => {
        const m = JSON.parse(ev.data)
        if (m.type === 'snapshot') {
          retry = 0
          // Empty lists arrive without `rows` (omitempty on the engine side).
          columns = m.columns ?? []
          rowsRef.current = new Map(((m.rows ?? []) as Row[]).map((r) => [r.uid, r]))
          setState({ columns, rows: Array.from(rowsRef.current.values()), loading: false })
        } else if (m.type === 'delta') {
          for (const r of (m.rows ?? []) as Row[]) rowsRef.current.set(r.uid, r)
          for (const uid of (m.deletes ?? []) as string[]) rowsRef.current.delete(uid)
          emit()
        } else if (m.type === 'error') {
          setState((s) => ({ ...s, loading: false, error: m.error }))
        }
      }
      ws.onclose = () => {
        if (closed) return
        retry = Math.min(retry + 1, 6)
        setTimeout(() => !closed && open(), 500 * 2 ** retry)
      }
    }
    open()
    return () => {
      closed = true
      cancelAnimationFrame(raf)
      ws?.close()
    }
  }, [clusterId, group, resource, namespace, enabled])

  return state
}
