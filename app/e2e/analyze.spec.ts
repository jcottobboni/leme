// Deterministic analyzer (M4 G1): Diagnóstico tab and Problemas panel. Kind cluster ONLY.
import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
let engine: ChildProcess
let base = ''

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
})

async function connect(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await page.getByTestId(`cluster-row-${KIND_CONTEXT}`).first().click()
}

async function openDeployment(page: Page, name: string) {
  await connect(page)
  await page.getByTestId('nav-kind-Deployment').click()
  await page.locator('.ns-select:visible').first().selectOption('demo')
  await page.getByTestId(`row-${name}`).click()
  await page.getByTestId('tab-diagnosis').click()
}

test('Diagnóstico de um Deployment com imagem inexistente mostra ImagePullBackOff com evidência', async ({ page }) => {
  await openDeployment(page, 'broken')
  const diag = page.getByTestId('diagnosis')
  const f = diag.getByTestId('finding-image-pull').first()
  await expect(f).toBeVisible({ timeout: 20000 })
  await expect(f).toContainText('Perguntar à IA')
  await f.locator('summary').click()
  await expect(f.getByTestId('finding-evidence')).toContainText('does-not-exist')
})

test('Diagnóstico de um Deployment saudável não mostra problemas', async ({ page }) => {
  await openDeployment(page, 'web')
  await expect(page.getByTestId('diagnosis')).toContainText('Nenhum problema encontrado')
})

test('painel Problemas lista o Deployment quebrado do namespace demo', async ({ page }) => {
  await connect(page)
  await page.getByTestId('tool-problems').click()
  const panel = page.getByTestId('problems')
  await panel.locator('.ns-select').selectOption('demo')
  await expect(panel.getByTestId('finding-image-pull').first()).toBeVisible({ timeout: 20000 })
  await expect(panel.locator('.an-finding', { hasText: 'broken' }).first()).toBeVisible()
  await expect(panel.locator('.an-finding', { hasText: 'web-' })).toHaveCount(0)
})
