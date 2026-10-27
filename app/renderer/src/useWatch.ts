import { useEffect, useRef, useState } from 'react'
import type { Column, Row } from './api'
import { useSocket } from './hooks/useSocket'

interface WatchState {
  columns: Column[]
  rows: Row[]
  loading: boolean
  error?: string
}

type WatchMsg =
  | { type: 'snapshot'; columns?: Column[]; rows?: Row[] }
  | { type: 'delta'; rows?: Row[]; deletes?: string[] }
  | { type: 'error'; error: string }

/** Subscribes to a live resource list. Rows are kept in a Map and re-emitted at most once per frame. */
export function useWatch(clusterId: string, group: string, resource: string, namespace: string, enabled = true): WatchState {
  const [state, setState] = useState<WatchState>({ columns: [], rows: [], loading: true })
  const rowsRef = useRef(new Map<string, Row>())
  const columnsRef = useRef<Column[]>([])
  const raf = useRef(0)

  // New subscription: start empty (runs before useSocket opens the new connection).
  useEffect(() => {
    if (!enabled) return
    rowsRef.current = new Map()
    columnsRef.current = []
    setState({ columns: [], rows: [], loading: true })
    return () => {
      cancelAnimationFrame(raf.current)
      raf.current = 0
    }
  }, [clusterId, group, resource, namespace, enabled])

  const emit = () => {
    if (raf.current) return
    raf.current = requestAnimationFrame(() => {
      raf.current = 0
      setState((s) => ({ ...s, columns: columnsRef.current, rows: Array.from(rowsRef.current.values()), loading: false }))
    })
  }

  useSocket<WatchMsg>(enabled && `/api/clusters/${clusterId}/watch`, { group, resource, namespace }, {
    onMessage: (m, ctl) => {
      if (m.type === 'snapshot') {
        ctl.resetBackoff()
        // Empty lists arrive without `rows` (omitempty on the engine side).
        columnsRef.current = m.columns ?? []
        rowsRef.current = new Map((m.rows ?? []).map((r) => [r.uid, r]))
        setState({ columns: columnsRef.current, rows: Array.from(rowsRef.current.values()), loading: false })
      } else if (m.type === 'delta') {
        for (const r of m.rows ?? []) rowsRef.current.set(r.uid, r)
        for (const uid of m.deletes ?? []) rowsRef.current.delete(uid)
        emit()
      } else if (m.type === 'error') {
        setState((s) => ({ ...s, loading: false, error: m.error }))
      }
    },
  })

  return state
}
