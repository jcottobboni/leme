// Layout v2 (M3.6): details drawer, grid selection and row actions, theme, overview. Kind cluster ONLY.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

function kubectl(...args: string[]) {
  // Always pin both kubeconfig and context so nothing else can be targeted.
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

test.beforeAll(async () => {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: KIND_KUBECONFIG, LEME_TOKEN: TOKEN },
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
  try {
    kubectl('-n', 'demo', 'delete', 'configmap', 'e2e-bulk-1', 'e2e-bulk-2', '--ignore-not-found')
  } catch {
    /* best effort */
  }
})

async function openKind(page: Page, kind: string) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId(`nav-kind-${kind}`).click()
  await page.locator('.ns-select:visible').first().selectOption('demo')
}

test('CA-V1: drawer de detalhes, navegação por teclado e abrir em aba', async ({ page }) => {
  await openKind(page, 'Pod')
  const rows = page.locator('[data-testid^="row-web-"]')
  await expect(rows).toHaveCount(3)
  const first = (await rows.first().getAttribute('data-testid'))!.slice(4)
  const second = (await rows.nth(1).getAttribute('data-testid'))!.slice(4)
  await rows.first().click()
  const drawer = page.getByTestId('drawer')
  await expect(drawer.locator('.obj-name')).toContainText(first)
  await page.keyboard.press('ArrowDown')
  await expect(drawer.locator('.obj-name')).toContainText(second)
  await page.keyboard.press('Escape')
  await expect(drawer).toHaveCount(0)
  await rows.first().click()
  await drawer.getByRole('button', { name: 'Abrir em aba' }).click()
  await expect(page.locator('.dv-tab .ctab', { hasText: first })).toBeVisible()
  await expect(page.getByTestId('drawer')).toHaveCount(0)
})

test('CA-V2: exclusão em lote pede confirmação e respeita somente leitura', async ({ page }) => {
  kubectl('-n', 'demo', 'create', 'configmap', 'e2e-bulk-1', '--from-literal=a=1')
  kubectl('-n', 'demo', 'create', 'configmap', 'e2e-bulk-2', '--from-literal=a=1')
  await openKind(page, 'ConfigMap')
  for (const n of ['e2e-bulk-1', 'e2e-bulk-2']) await page.getByTestId(`row-${n}`).locator('input[type=checkbox]').check()
  await expect(page.getByTestId('bulkbar')).toContainText('2 selecionados')
  // Read-only blocks the bulk action.
  await page.getByTestId('rw-toggle').click()
  await expect(page.getByTestId('bulk-delete')).toBeDisabled()
  await page.getByTestId('rw-toggle').click()
  await page.getByTestId('bulk-delete').click()
  await expect(page.locator('.modal')).toContainText('e2e-bulk-1')
  await page.locator('.modal').getByRole('button', { name: 'Excluir' }).click()
  await expect(page.locator('.toast-ok', { hasText: 'Excluído: 2 de 2' })).toBeVisible()
  await expect.poll(() => kubectl('-n', 'demo', 'get', 'configmap', '-o', 'name')).not.toContain('e2e-bulk-')
})

test('CA-V3: menu da linha conforme o tipo', async ({ page }) => {
  await openKind(page, 'Pod')
  await page.locator('[data-testid^="row-web-"]').first().click({ button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'Logs' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Shell' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Escalar…' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await page.getByTestId('nav-kind-Deployment').click()
  await page.getByTestId('row-web').getByRole('button', { name: 'Ações de web' }).click()
  await expect(page.getByRole('menuitem', { name: 'Escalar…' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Reiniciar…' })).toBeVisible()
  // A workload has the aggregated logs (M7), not the logs of one pod.
  await expect(page.getByRole('menuitem', { name: 'Logs', exact: true })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'Logs (todos os pods)' })).toBeVisible()
  await page.keyboard.press('Escape')
  // Containers column (squares) on pods.
  await page.getByTestId('nav-kind-Pod').click()
  await expect(page.locator('[data-testid^="row-web-"]').first().locator('.sq.running')).toHaveCount(1)
})

test('CA-V4: tema claro aplica e persiste', async ({ page }) => {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.setItem('leme.ui.v1', JSON.stringify({ theme: 'dark' })))
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.getByTestId('theme-toggle').click() // dark -> light
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  expect(bg).toBe('rgb(238, 240, 243)')
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await expect(page.locator('.dockview-theme-light')).toHaveCount(1)
})

test('CA-V5: visão geral com donuts e medidores', async ({ page }) => {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  await page.getByTestId('tool-overview').click()
  await expect(page.getByTestId('ov-pods').locator('svg.donut')).toBeVisible({ timeout: 30_000 })
  // A donut only when there is more than one state; a single state shows a bar and a sentence.
  await expect(page.getByTestId('ov-Deployment').locator('svg.donut, .ov-single').first()).toBeVisible()
  await expect(page.getByTestId('gauge-CPU')).toContainText('Requests')
  await expect(page.getByTestId('gauge-Memória')).toContainText('Alocável')
})

test('CA-V6: duplo clique abre em aba, lista vazia mostra estado vazio e a topologia não vaza CSS na grade', async ({ page }) => {
  await openKind(page, 'Deployment')
  await page.getByTestId('row-web').dblclick()
  await expect(page.locator('.dv-tab .ctab', { hasText: /^web$/ })).toBeVisible()
  await expect(page.getByTestId('drawer')).toHaveCount(0)
  expect(await page.evaluate(() => String(getSelection()).trim())).toBe('')
  // An empty list arrives without rows: it must render the empty state, not a skeleton forever.
  await page.getByTestId('nav-kind-DaemonSet').click()
  await expect(page.locator('.empty', { hasText: 'Nenhum recurso do tipo DaemonSets em demo' })).toBeVisible()
  // Topology styles must not leak into grid rows.
  await page.getByTestId('tool-topology').click()
  await expect(page.getByTestId('topo-region').first()).toBeVisible({ timeout: 20_000 })
  await page.getByTestId('nav-kind-Pod').click()
  const row = page.locator('[data-testid^="row-web-"]').first()
  await expect(row).toBeVisible()
  expect(await row.evaluate((e) => getComputedStyle(e).borderTopLeftRadius)).toBe('0px')
})
