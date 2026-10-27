// Follow mode can stream for hours: log buffers keep only the most recent ~5 MB.
export const BUFFER_MAX = 5 * 1024 * 1024

/**
 * Append-only buffer capped by size: the oldest items are dropped (and their size counted in `dropped`) past `max`.
 * Items are strings by default (size = length); other items need a `sizeOf`.
 */
export class LogRing<T = string> {
  private chunks: T[] = []
  private head = 0
  size = 0
  dropped = 0
  constructor(
    private max = BUFFER_MAX,
    private sizeOf: (item: T) => number = (item) => String(item).length,
  ) {}
  push(item: T) {
    this.chunks.push(item)
    this.size += this.sizeOf(item)
    while (this.size > this.max && this.head < this.chunks.length - 1) {
      const old = this.chunks[this.head]
      this.chunks[this.head++] = undefined as T
      const n = this.sizeOf(old)
      this.size -= n
      this.dropped += n
    }
    // Compact the array now and then instead of shifting on every drop.
    if (this.head > 1024 && this.head * 2 > this.chunks.length) {
      this.chunks = this.chunks.slice(this.head)
      this.head = 0
    }
  }
  clear() {
    this.chunks = []
    this.head = 0
    this.size = 0
    this.dropped = 0
  }
  parts(): T[] {
    return this.chunks.slice(this.head)
  }
}
