// Cluster identity (M3.7): alias, short label, color, icon, environment, labeled hotbar. Kind cluster ONLY;
// the fake production context points to a closed port and is never connected.
import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const FAKE_CONTEXT = 'fake-app-prd/api-ocp-prd-example:6443/someone'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

test.beforeAll(async () => {
  engine = spawn(path.join(__dirname, '..', '..', 'engine', 'bin', 'leme'), ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], {
    env: { ...process.env, KUBECONFIG: `${KIND_KUBECONFIG}:${path.join(__dirname, 'fake-prod.kubeconfig')}`, LEME_TOKEN: TOKEN, LEME_CONFIG_DIR: path.join(os.tmpdir(), `leme-e2e-identity-${process.pid}`) },
  })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
})

test.afterAll(() => engine?.kill('SIGTERM'))

async function fresh(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
}

test('CA-I1: nome legível, sigla e ambiente automáticos para contextos OpenShift', async ({ page }) => {
  await fresh(page)
  const card = page.getByTestId(`cluster-card-${FAKE_CONTEXT}`)
  await expect(card.locator('.cc-name')).toHaveText('fake-app-prd')
  await expect(card.locator('.cc-server')).toContainText('ocp-prd-example')
  await expect(card.locator('.avatar-text')).toHaveText('FAP')
  await expect(card.locator('.avatar')).toHaveAttribute('data-env', 'prod')
  await expect(card.locator('.badge-prod')).toBeVisible()
})

test('CA-I2: personalizar apelido, sigla, cor e ícone reflete na hotbar, navigator e barra de status', async ({ page }) => {
  await fresh(page)
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  const hot = page.getByTestId(`hotbar-${KIND_CONTEXT}`)
  await expect(hot).toBeVisible()
  await hot.click({ button: 'right' })
  await page.getByRole('menuitem', { name: /Personalizar/ }).click()
  const dlg = page.getByTestId('cluster-profile')
  await dlg.getByTestId('cp-alias').fill('Kind Lab')
  await dlg.getByTestId('cp-short').fill('kl')
  await dlg.getByRole('button', { name: 'Cor #f97316' }).click()
  await expect(dlg.locator('.cp-hot .avatar-text')).toHaveText('KL')
  await page.getByTestId('cp-save').click()
  await expect(hot.locator('.avatar-text')).toHaveText('KL')
  await expect(hot.locator('.hot-label')).toHaveText('Kind Lab')
  expect(await hot.locator('.avatar').evaluate((e) => getComputedStyle(e).getPropertyValue('--cc').trim())).toBe('#f97316')
  await expect(page.locator('.navigator .nav-cluster .cluster-name')).toHaveText('Kind Lab')
  await expect(page.locator('.statusbar .sb-cluster')).toContainText('Kind Lab')
  // Icon replaces the short label; persists after reload.
  await page.getByTestId('cluster-edit').click()
  await page.getByTestId('cluster-profile').getByRole('button', { name: '🧪' }).click()
  await page.getByTestId('cp-save').click()
  await expect(hot.locator('.avatar-text')).toHaveText('🧪')
  await page.reload()
  await expect(page.getByTestId(`cluster-card-${KIND_CONTEXT}`).locator('.cc-name')).toHaveText('Kind Lab')
  // Restore defaults.
  await page.getByTestId(`cluster-card-${KIND_CONTEXT}`).getByTitle('Personalizar').click()
  await page.getByRole('button', { name: 'Restaurar padrão' }).click()
  await page.getByTestId('cp-save').click()
  await expect(page.getByTestId(`cluster-card-${KIND_CONTEXT}`).locator('.cc-name')).toHaveText(KIND_CONTEXT)
})

test('CA-I3: cartão ao passar o mouse, nomes na hotbar e reordenar arrastando', async ({ page }) => {
  await fresh(page)
  // Pin the fake context from its card menu, then connect kind.
  await page.getByTestId(`cluster-card-${FAKE_CONTEXT}`).click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Fixar na hotbar' }).click()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
  const items = page.locator('.hot-list .hot-item')
  await expect(items).toHaveCount(2)
  await expect(items.nth(0)).toHaveAttribute('data-testid', `hotbar-${FAKE_CONTEXT}`)
  await page.getByTestId(`hotbar-${FAKE_CONTEXT}`).hover()
  await expect(page.getByTestId('hot-card')).toContainText(FAKE_CONTEXT)
  await expect(page.getByTestId('hot-card')).toContainText('ocp-prd-example')
  // Drag kind before the fake context: kind becomes the first pinned item.
  await page.getByTestId(`hotbar-${KIND_CONTEXT}`).dragTo(page.getByTestId(`hotbar-${FAKE_CONTEXT}`))
  await expect(items.nth(0)).toHaveAttribute('data-testid', `hotbar-${KIND_CONTEXT}`)
  // Labels toggle.
  await expect(page.locator('.hot-label').first()).toBeVisible()
  await page.getByTestId('hotbar-labels').click()
  await expect(page.locator('.hot-list .hot-label')).toHaveCount(0)
})
