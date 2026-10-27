// M7 RF05 / CA05: k9s-style filters in the grid search and the palette, and JSONPath columns that
// persist. Read-only against the isolated kind cluster (demo/web has app=web, demo/broken has
// app=broken, demo/app-secret is the test Secret); nothing is created or changed in the cluster.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-m7s-' + Math.random().toString(36).slice(2)
const CONFIG = fs.mkdtempSync(path.join(os.tmpdir(), 'leme-e2e-m7s-'))
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

test.beforeAll(async () => {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: CONFIG },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
})

async function openApp(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('nav-kind-Pod').waitFor()
}

const grid = (page: Page) => page.locator('.grid-panel').filter({ visible: true })

test('CA05: -l app=web filtra no engine, !web e /regex/ filtram a grade, regex inválida não quebra', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('nav-kind-Deployment').click()
  const g = grid(page)
  await expect(g.getByTestId('row-web')).toBeVisible()
  await expect(g.getByTestId('row-broken')).toBeVisible()
  const search = g.getByTestId('grid-search')

  // Label selector: applied server-side, so the list itself has one item ("1 item", not "1 de N").
  await search.fill('-l app=web')
  await expect(g.getByTestId('row-broken')).toHaveCount(0)
  await expect(g.getByTestId('row-web')).toBeVisible()
  await expect(g.locator('.count-pill')).toHaveText('1 item')
  await expect(g.getByTestId('fchip-label')).toContainText('app=web')

  // Negation: everything except web.
  await search.fill('!web')
  await expect(g.getByTestId('row-web')).toHaveCount(0)
  await expect(g.getByTestId('row-broken')).toBeVisible()
  await expect(g.getByTestId('fchip-not')).toContainText('web')

  // Regex and namespace, combined.
  await search.fill('ns:demo /^bro/')
  await expect(g.getByTestId('row-broken')).toBeVisible()
  await expect(g.getByTestId('row-web')).toHaveCount(0)
  await expect(g.getByTestId('row-coredns')).toHaveCount(0)

  // Selector + negation together; a chip removes its term.
  await search.fill('-l app!=broken !coredns ns:demo')
  await expect(g.getByTestId('row-web')).toBeVisible()
  await expect(g.getByTestId('row-broken')).toHaveCount(0)
  await g.getByRole('button', { name: 'Remover o filtro -l app!=broken' }).click()
  await expect(search).toHaveValue('!coredns ns:demo')
  await expect(g.getByTestId('row-broken')).toBeVisible()

  // Invalid regex: inline error, the grid keeps working.
  await search.fill('/[/ web')
  await expect(g.getByTestId('filter-error')).toContainText('regex inválida')
  await expect(g.getByTestId('row-web')).toBeVisible()

  // Invalid selector: the engine says so, no crash.
  await search.fill('-l "app in (web"')
  await expect(g.locator('.banner.error')).toContainText('seletor de label inválido')
  await search.fill('')
  await expect(g.getByTestId('row-broken')).toBeVisible()
})

test('CA05: coluna JSONPath por tipo, persiste ao recarregar; expressão inválida mostra o erro; Secret mascarado', async ({ page }) => {
  await openApp(page)
  await page.getByTestId('nav-kind-Deployment').click()
  let g = grid(page)
  await expect(g.getByTestId('row-web')).toBeVisible()
  await g.getByTestId('grid-columns').click()
  await page.getByRole('menuitem', { name: 'Adicionar coluna JSONPath…' }).click()
  const dlg = page.getByTestId('jp-dialog')
  await dlg.getByTestId('jp-expr').fill('.spec.strategy.type')
  await dlg.getByTestId('jp-label').fill('Estratégia')
  await dlg.getByTestId('jp-add').click()
  // Engine-side parse error (balanced, but not a valid index).
  await dlg.getByTestId('jp-expr').fill('.spec.template.spec.containers[abc]')
  await dlg.getByTestId('jp-label').fill('Ruim')
  await dlg.getByTestId('jp-add').click()
  await expect(dlg.getByTestId('jp-col-error')).toContainText('inválida')
  await page.keyboard.press('Escape')
  await expect(dlg).toHaveCount(0)

  await expect(g.locator('.th-label', { hasText: 'Estratégia' })).toBeVisible()
  await expect(g.getByTestId('row-web')).toContainText('RollingUpdate')
  await expect(g.getByTestId('jp-banner')).toContainText('inválida')
  // The error also shows in the columns menu.
  await g.getByTestId('grid-columns').click()
  await expect(page.getByRole('menuitem', { name: /Ruim: expressão JSONPath inválida/ })).toBeVisible()
  await page.keyboard.press('Escape')

  // Persists: reload the app and the column is still there.
  await page.goto('about:blank')
  await openApp(page)
  await page.getByTestId('nav-kind-Deployment').click()
  g = grid(page)
  await expect(g.locator('.th-label', { hasText: 'Estratégia' })).toBeVisible()
  await expect(g.getByTestId('row-web')).toContainText('RollingUpdate')

  // Secrets: values never reach the grid, even through a JSONPath column.
  const value = JSON.parse(kubectl('-n', 'demo', 'get', 'secret', 'app-secret', '-o', 'jsonpath={.data}')).password as string
  await page.getByTestId('nav-kind-Secret').click()
  g = grid(page)
  await expect(g.getByTestId('row-app-secret')).toBeVisible()
  await g.getByTestId('grid-columns').click()
  await page.getByRole('menuitem', { name: 'Adicionar coluna JSONPath…' }).click()
  await dlg.getByTestId('jp-expr').fill('.data.password')
  await dlg.getByTestId('jp-label').fill('Senha')
  await dlg.getByTestId('jp-add').click()
  await page.keyboard.press('Escape')
  await expect(g.locator('.th-label', { hasText: 'Senha' })).toBeVisible()
  await expect(g.getByTestId('row-app-secret')).toContainText('<hidden>')
  await expect(g.getByTestId('row-app-secret')).not.toContainText(value)
})

test('CA05: paleta com :deploy demo -l app=web abre a grade já filtrada', async ({ page }) => {
  await openApp(page)
  await page.keyboard.press('Control+K')
  const input = page.getByTestId('palette-input')
  await input.fill(':deploy demo -l app=web')
  await expect(page.getByTestId('palette-chips')).toContainText('app=web')
  const item = page.getByTestId('palette-item').first()
  await expect(item).toContainText('Deployment')
  await expect(item).toContainText('ns demo')
  await input.press('Enter')
  const g = grid(page)
  await expect(g.getByTestId('grid-search')).toHaveValue('-l app=web')
  await expect(g.getByTestId('row-web')).toBeVisible()
  await expect(g.getByTestId('row-broken')).toHaveCount(0)
  await expect(g.locator('.ns-select')).toHaveValue('demo')

  // Object search with negation and namespace: hits are filtered in the palette.
  await page.keyboard.press('Control+K')
  await input.fill('web !web-')
  await expect(page.getByTestId('palette-item').filter({ hasText: 'Deployment · demo' }).first()).toBeVisible()
  await expect(page.getByTestId('palette-item').filter({ hasText: /^web-/ })).toHaveCount(0)
  // A CRD-free alias that does not exist says so.
  await input.fill(':nãoexiste')
  await expect(page.getByText('Nenhum tipo de recurso para :nãoexiste.')).toBeVisible()
})
