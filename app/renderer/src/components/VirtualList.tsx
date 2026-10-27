import { useRef, type CSSProperties, type ReactNode } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'

export interface VirtualListProps<T> {
  items: readonly T[]
  /** Row renderer. Only the rows in view (plus `overscan`) are mounted. */
  renderRow: (item: T, index: number) => ReactNode
  /** Stable key of a row (default: index). */
  getKey?: (item: T, index: number) => string | number
  /** Estimated row height in px (default 32). Rows are measured after render, so variable heights work. */
  rowHeight?: number | ((index: number) => number)
  /** Rows mounted beyond the viewport on each side (default 12). */
  overscan?: number
  /** Sticky content above the rows, inside the scroll container (e.g. column headers). */
  header?: ReactNode
  /** Shown instead of the rows when `items` is empty. */
  empty?: ReactNode
  className?: string
  style?: CSSProperties
  /** data-testid of the scroll container. */
  testId?: string
  /** Class of each row wrapper (absolute positioned, full width). */
  rowClassName?: string
}

/**
 * Virtualized vertical list (@tanstack/react-virtual). The component is the scroll container: give it a height
 * (flex: 1 in a column layout, or a fixed height). Rows are absolutely positioned wrappers; put the row look inside
 * `renderRow`. For <table> markup use `useVirtualRows` instead.
 */
export function VirtualList<T>({ items, renderRow, getKey, rowHeight = 32, overscan = 12, header, empty, className, style, testId, rowClassName }: VirtualListProps<T>) {
  const parent = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({
    count: items.length,
    getScrollElement: () => parent.current,
    estimateSize: typeof rowHeight === 'function' ? rowHeight : () => rowHeight,
    getItemKey: getKey ? (i) => getKey(items[i], i) : undefined,
    overscan,
  })
  return (
    <div ref={parent} className={`vlist${className ? ` ${className}` : ''}`} style={style} data-testid={testId}>
      {header && <div className="vlist-head">{header}</div>}
      {!items.length && empty}
      {!!items.length && (
        <div style={{ height: virt.getTotalSize(), position: 'relative', width: '100%' }}>
          {virt.getVirtualItems().map((vi) => (
            <div
              key={vi.key}
              data-index={vi.index}
              ref={virt.measureElement}
              className={rowClassName}
              style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vi.start}px)` }}
            >
              {renderRow(items[vi.index], vi.index)}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * Virtual rows for <table> markup: render `rows` inside <tbody> between two spacer rows of `padTop` / `padBottom`
 * px (e.g. `<tr style={{ height: padTop }} />`). `scrollRef` goes on the scrolling element that wraps the table.
 */
export function useVirtualRows(count: number, opts: { rowHeight?: number; overscan?: number } = {}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({ count, getScrollElement: () => scrollRef.current, estimateSize: () => opts.rowHeight ?? 32, overscan: opts.overscan ?? 12 })
  const rows = virt.getVirtualItems()
  const padTop = rows.length ? rows[0].start : 0
  const padBottom = rows.length ? virt.getTotalSize() - rows[rows.length - 1].end : 0
  return { scrollRef, rows, padTop, padBottom, measure: virt.measureElement, virtualizer: virt }
}
