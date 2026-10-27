// M7 RF05: unit tests of the shared k9s-style filter parser (no browser, no cluster).
import { test, expect } from '@playwright/test'
import { matchHit, matchRow, normalizeJP, parseFilter, resolveAlias, withoutTerm, gridText } from '../renderer/src/features/search/filter'
import type { Resource, Row } from '../renderer/src/api'

const row = (name: string, ns: string, labels: Record<string, string> = {}, cells: Record<string, unknown> = {}) =>
  ({ uid: name, name, namespace: ns, health: 'ok', labels, cells } as Row)

test('parser: termos, seletor, negação, regex, namespace e alias', () => {
  const f = parseFilter(':deploy demo -l app=web,tier!=db !canary /^we.b/ ns:prod texto "com espaço"')
  expect(f.alias).toBe('deploy')
  expect(f.aliasArg).toBe('demo')
  expect(f.namespaces).toEqual(['demo', 'prod'])
  expect(f.labelSelector).toBe('app=web,tier!=db')
  expect(f.exclude).toEqual(['canary'])
  expect(f.regex.map((r) => r.source)).toEqual(['^we.b'])
  expect(f.include).toEqual(['texto', 'com espaço'])
  expect(f.errors).toEqual([])
  expect(gridText(f)).toBe('-l app=web,tier!=db !canary /^we.b/ texto "com espaço"')

  expect(parseFilter('-lapp=web').labelSelector).toBe('app=web')
  expect(parseFilter('-l "env in (a, b)" -l x=y').labelSelector).toBe('env in (a, b),x=y')
  expect(parseFilter('-l').errors[0]).toContain('faltou o seletor')
  // A regex keeps its spaces; a negated regex excludes.
  const r = parseFilter('/a b/ !/^kube-/')
  expect(r.regex[0].source).toBe('a b')
  expect(r.notRegex[0].source).toBe('^kube-')
  // Invalid regex: an error, never a throw.
  const bad = parseFilter('/[/ web')
  expect(bad.errors[0]).toContain('regex inválida')
  expect(bad.include).toEqual(['web'])
  // ":" only counts at the start; plain text stays text.
  expect(parseFilter('web :x').alias).toBeUndefined()
  expect(parseFilter('').terms).toEqual([])
})

test('parser: remover um termo (chip) e casar linhas e hits', () => {
  const f = parseFilter('-l app=web !canary ns:demo')
  expect(withoutTerm(f, 0)).toBe('!canary ns:demo')
  expect(withoutTerm(f, 2)).toBe('-l app=web !canary')

  const rows = [row('web', 'demo', { app: 'web' }), row('web-canary', 'demo'), row('api', 'prod', {}, { image: 'nginx:1' })]
  const names = (q: string) => rows.filter((r) => matchRow(r, parseFilter(q))).map((r) => r.name)
  expect(names('!canary')).toEqual(['web', 'api'])
  expect(names('web !canary')).toEqual(['web'])
  expect(names('/^w.b$/')).toEqual(['web'])
  expect(names('ns:prod')).toEqual(['api'])
  expect(names('nginx')).toEqual(['api'])
  expect(names('app=web')).toEqual(['web'])
  expect(names('!/canary|api/')).toEqual(['web'])
  // The label selector is the engine's job: it does not filter here.
  expect(names('-l app=nothing')).toEqual(['web', 'web-canary', 'api'])

  expect(matchHit({ name: 'web-1', namespace: 'demo', kind: 'Pod' }, parseFilter('web !web-'))).toBe(false)
  expect(matchHit({ name: 'web', namespace: 'demo', kind: 'Deployment' }, parseFilter('web ns:demo'))).toBe(true)
})

test('aliases: k9s, nomes do recurso e shortNames de CRDs', () => {
  const res = (kind: string, resource: string, group = '', shortNames?: string[]) => ({ kind, resource, group, version: 'v1', namespaced: true, shortNames } as Resource)
  const all = [res('Deployment', 'deployments', 'apps', ['deploy']), res('Pod', 'pods', '', ['po']), res('Event', 'events'), res('Event', 'events', 'events.k8s.io'),
    res('Certificate', 'certificates', 'cert-manager.io', ['cert', 'certs']), res('Deployment', 'deployments', 'example.com')]
  expect(resolveAlias(all, 'deploy').map((r) => r.group)).toEqual(['apps'])
  expect(resolveAlias(all, 'dp').map((r) => r.group)).toEqual(['apps'])
  expect(resolveAlias(all, 'po').map((r) => r.kind)).toEqual(['Pod'])
  expect(resolveAlias(all, 'cert').map((r) => r.kind)).toEqual(['Certificate'])
  expect(resolveAlias(all, 'ev')).toHaveLength(1)
  expect(resolveAlias(all, 'certif').map((r) => r.kind)).toEqual(['Certificate']) // prefix fallback
  expect(resolveAlias(all, 'zzz')).toEqual([])
})

test('JSONPath: mesma forma da chave do engine', () => {
  expect(normalizeJP('.spec.replicas')).toBe('{.spec.replicas}')
  expect(normalizeJP('spec.replicas')).toBe('{.spec.replicas}')
  expect(normalizeJP(' {.a}{.b} ')).toBe('{.a}{.b}')
  expect(normalizeJP('')).toBe('')
})
