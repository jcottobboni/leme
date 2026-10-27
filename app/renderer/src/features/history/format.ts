import type { HistoryOp } from '../../api'

/** Short summary of the changed paths: "spec.template.spec.containers[0].image, +2". */
export function summary(ops: HistoryOp[] = []) {
  const paths = ops.map((o) => o.path.replace(/^\//, '').replace(/\/(\d+)(?=\/|$)/g, '[$1]').replace(/\//g, '.').replace(/~1/g, '/').replace(/~0/g, '~'))
  if (!paths.length) return ''
  return paths.slice(0, 2).join(', ') + (paths.length > 2 ? `, +${paths.length - 2}` : '')
}
