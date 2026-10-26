import type { TimelineEvent } from '../../api'

export interface Row {
  key: string // path joined with '|'
  label: string
  depth: number
  hasChildren: boolean
  expanded: boolean
  events: TimelineEvent[] // own events, or the whole subtree when collapsed
  aggregated: boolean
  warnings: number
}

interface TreeNode {
  key: string
  label: string
  depth: number
  own: TimelineEvent[]
  children: Map<string, TreeNode>
}

export interface Filter {
  type: '' | 'Warning' | 'Normal'
  text: string
  from: number
  to: number
}

export function matches(e: TimelineEvent, f: Filter) {
  if (f.type && e.type !== f.type) return false
  if (e.last < f.from || e.first > f.to) return false
  if (f.text) {
    const t = f.text.toLowerCase()
    if (!`${e.reason} ${e.message} ${e.kind}/${e.name} ${e.namespace}`.toLowerCase().includes(t)) return false
  }
  return true
}

/** Builds the visible rows: namespace → workload → object, collapsed rows aggregating their subtree. */
export function buildRows(events: TimelineEvent[], f: Filter, expanded: Set<string>, collapsedByDefault: (depth: number) => boolean): Row[] {
  const root: TreeNode = { key: '', label: '', depth: -1, own: [], children: new Map() }
  for (const e of events) {
    if (!matches(e, f)) continue
    let node = root
    e.path.forEach((seg, i) => {
      const key = node.key ? `${node.key}|${seg}` : seg
      let child = node.children.get(seg)
      if (!child) {
        child = { key, label: seg, depth: i, own: [], children: new Map() }
        node.children.set(seg, child)
      }
      node = child
    })
    node.own.push(e)
  }
  const rows: Row[] = []
  const subtree = (n: TreeNode): TimelineEvent[] => {
    const out = [...n.own]
    for (const c of n.children.values()) out.push(...subtree(c))
    return out
  }
  const walk = (n: TreeNode) => {
    const sorted = [...n.children.values()].sort((a, b) => a.label.localeCompare(b.label))
    for (const c of sorted) {
      const hasChildren = c.children.size > 0
      // `expanded` holds the rows the user toggled away from their default state.
      const isOpen = hasChildren && expanded.has(c.key) === collapsedByDefault(c.depth)
      const evs = isOpen || !hasChildren ? c.own : subtree(c)
      rows.push({ key: c.key, label: c.label, depth: c.depth, hasChildren, expanded: isOpen, events: evs, aggregated: hasChildren && !isOpen, warnings: evs.filter((e) => e.type === 'Warning').length })
      if (isOpen) walk(c)
    }
  }
  walk(root)
  return rows
}

/** Histogram of event occurrences (count spread over first..last) in `buckets` slots. */
export function density(events: TimelineEvent[], f: Filter, buckets: number) {
  const warn = new Array(buckets).fill(0)
  const normal = new Array(buckets).fill(0)
  const span = f.to - f.from
  if (span <= 0) return { warn, normal }
  for (const e of events) {
    if (!matches(e, f)) continue
    const a = Math.max(0, Math.floor(((e.first - f.from) / span) * buckets))
    const b = Math.min(buckets - 1, Math.floor(((e.last - f.from) / span) * buckets))
    const per = e.count / (b - a + 1)
    const arr = e.type === 'Warning' ? warn : normal
    for (let i = a; i <= b; i++) arr[i] += per
  }
  return { warn, normal }
}
