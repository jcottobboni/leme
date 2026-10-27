// MCP agent access (M4 G2): the dialog that enables writes per context and `leme mcp` driven over
// JSON-RPC. Kind cluster ONLY; the fake production context points to a closed port and is never connected.
import { test, expect, type Page } from '@playwright/test'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'
import os from 'node:os'
import path from 'node:path'

const KIND_KUBECONFIG = path.join(os.homedir(), '.kube', 'leme-dev', 'kubeconfig')
const KIND_CONTEXT = 'kind-leme-dev'
const FAKE_CONTEXT = 'fake-app-prd/api-ocp-prd-example:6443/someone'
const ENGINE = path.join(__dirname, '..', '..', 'engine', 'bin', 'leme')
const TOKEN = 'e2e-' + Math.random().toString(36).slice(2)
const CONFIG_DIR = path.join(os.tmpdir(), `leme-e2e-agents-${process.pid}`)
const ENV = { ...process.env, KUBECONFIG: `${KIND_KUBECONFIG}:${path.join(__dirname, 'fake-prod.kubeconfig')}`, LEME_CONFIG_DIR: CONFIG_DIR }
const LABEL = 'leme-e2e-mcp'

let engine: ChildProcess
let base = ''

test.describe.configure({ mode: 'serial' })

function kubectl(...args: string[]) {
  // Always pin both kubeconfig and context so nothing else can be targeted.
  return execFileSync('kubectl', ['--kubeconfig', KIND_KUBECONFIG, '--context', KIND_CONTEXT, ...args], { encoding: 'utf8' })
}

function removeLabel() {
  try {
    kubectl('label', 'deployment', 'web', '-n', 'demo', `${LABEL}-`)
  } catch {
    /* already absent */
  }
}

test.beforeAll(async () => {
  engine = spawn(ENGINE, ['serve', '--addr', '127.0.0.1:0', '--static', path.join(__dirname, '..', 'renderer-dist')], { env: { ...ENV, LEME_TOKEN: TOKEN } })
  base = await new Promise<string>((resolve, reject) => {
    createInterface({ input: engine.stdout! }).on('line', (l) => {
      const m = JSON.parse(l)
      if (m.event === 'ready') resolve(`http://127.0.0.1:${m.port}`)
    })
    setTimeout(() => reject(new Error('engine timeout')), 10000)
  })
  removeLabel()
})

test.afterAll(() => {
  engine?.kill('SIGTERM')
  removeLabel()
})

async function fresh(page: Page) {
  await page.goto(`${base}/#token=${TOKEN}`)
  await page.evaluate(() => localStorage.clear())
  await page.reload()
}

async function openDialog(page: Page, name: string) {
  await page.getByTestId(`cluster-card-${name}`).click({ button: 'right' })
  await page.getByRole('menuitem', { name: /Acesso de agentes/ }).click()
  await expect(page.getByTestId('agent-access')).toBeVisible()
}

/** Minimal MCP client over the stdio of `leme mcp`. */
class McpClient {
  private proc: ChildProcess
  private next = 1
  private waiting = new Map<number, (m: any) => void>()
  constructor() {
    this.proc = spawn(ENGINE, ['mcp'], { env: ENV })
    createInterface({ input: this.proc.stdout! }).on('line', (l) => {
      const m = JSON.parse(l) // stdout must carry only protocol messages
      this.waiting.get(m.id)?.(m)
    })
  }
  rpc(method: string, params?: unknown): Promise<any> {
    const id = this.next++
    return new Promise((resolve, reject) => {
      const t = setTimeout(() => reject(new Error(`mcp timeout: ${method}`)), 30000)
      this.waiting.set(id, (m) => {
        clearTimeout(t)
        resolve(m)
      })
      this.proc.stdin!.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n')
    })
  }
  async tool(name: string, args: Record<string, unknown> = {}) {
    const r = await this.rpc('tools/call', { name, arguments: args })
    expect(r.error).toBeUndefined()
    const text: string = r.result.content[0].text
    return { text, isError: !!r.result.isError }
  }
  close() {
    this.proc.kill('SIGTERM')
  }
}

test('AG-1: o diálogo libera escrita no kind, mostra o comando e persiste', async ({ page }) => {
  await fresh(page)
  await openDialog(page, KIND_CONTEXT)
  await expect(page.getByTestId('agent-command')).toContainText(/claude mcp add leme -- ".*leme" mcp/)
  await expect(page.getByTestId('agent-write')).not.toBeChecked()
  await expect(page.getByTestId('agent-confirm')).toHaveCount(0)
  await page.getByTestId('agent-write').check()
  await expect(page.getByTestId('agent-confirm')).toHaveCount(0) // not production
  await page.getByTestId('agent-save').click()
  await expect(page.getByTestId('agent-access')).toHaveCount(0)
  await openDialog(page, KIND_CONTEXT)
  await expect(page.getByTestId('agent-write')).toBeChecked()
})

test('AG-2: contexto de produção exige digitar o nome exato', async ({ page }) => {
  await fresh(page)
  await openDialog(page, FAKE_CONTEXT)
  await page.getByTestId('agent-write').check()
  const confirm = page.getByTestId('agent-confirm')
  await expect(confirm).toBeVisible()
  await expect(page.getByTestId('agent-save')).toBeDisabled()
  await confirm.fill('fake-app-prd')
  await expect(page.getByTestId('agent-save')).toBeDisabled()
  await confirm.fill(FAKE_CONTEXT)
  await expect(page.getByTestId('agent-save')).toBeEnabled()
  await page.getByTestId('agent-save').click()
  await expect(page.getByTestId('agent-access')).toHaveCount(0)
  // Turn it off again: no confirmation needed to reduce access.
  await openDialog(page, FAKE_CONTEXT)
  await expect(page.getByTestId('agent-write')).toBeChecked()
  await page.getByTestId('agent-write').uncheck()
  await page.getByTestId('agent-save').click()
  await expect(page.getByTestId('agent-access')).toHaveCount(0)
})

test('AG-3: agente lê, propõe e aplica uma mudança no kind (propose -> apply)', async () => {
  const mcp = new McpClient()
  try {
    const init = await mcp.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
    expect(init.result.serverInfo.name).toBe('leme')
    const tools = (await mcp.rpc('tools/list')).result.tools.map((t: any) => t.name)
    expect(tools).toContain('apply_change')
    expect(tools.some((n: string) => /delete|exec/.test(n))).toBe(false)

    const ctxs = JSON.parse((await mcp.tool('list_contexts')).text).contexts
    const kind = ctxs.find((c: any) => c.name === KIND_CONTEXT)
    expect(kind.agentWritesEnabled).toBe(true)
    expect(ctxs.find((c: any) => c.name === FAKE_CONTEXT).agentWritesEnabled).toBe(false)

    const diag = await mcp.tool('diagnose', { context: KIND_CONTEXT, namespace: 'demo' })
    expect(diag.isError).toBe(false)
    expect(JSON.parse(diag.text)).toHaveProperty('findings')

    const prop = await mcp.tool('propose_change', {
      context: KIND_CONTEXT, group: 'apps', kind: 'Deployment', namespace: 'demo', name: 'web',
      merge_patch: { metadata: { labels: { [LABEL]: 'yes' } } }, summary: 'label from e2e',
    })
    expect(prop.isError).toBe(false)
    const p = JSON.parse(prop.text)
    expect(p.dryRun).toBe(true)
    expect(p.diff).toContain(`+    ${LABEL}: `)
    expect(kubectl('get', 'deployment', 'web', '-n', 'demo', '-o', 'jsonpath={.metadata.labels}')).not.toContain(LABEL)

    const applied = await mcp.tool('apply_change', { context: KIND_CONTEXT, token: p.token })
    expect(applied.isError).toBe(false)
    expect(kubectl('get', 'deployment', 'web', '-n', 'demo', '-o', 'jsonpath={.metadata.labels}')).toContain(LABEL)
    // Single-use token.
    expect((await mcp.tool('apply_change', { context: KIND_CONTEXT, token: p.token })).isError).toBe(true)
    // Secrets are never writable.
    const sec = await mcp.tool('propose_change', { context: KIND_CONTEXT, kind: 'Secret', namespace: 'demo', name: 'x', merge_patch: { metadata: { labels: { a: 'b' } } }, summary: 'no' })
    expect(sec.isError).toBe(true)
  } finally {
    mcp.close()
    removeLabel()
  }
  expect(kubectl('get', 'deployment', 'web', '-n', 'demo', '-o', 'jsonpath={.metadata.labels}')).not.toContain(LABEL)
})

test('AG-4: com a escrita desligada no app, o apply é recusado e nada muda', async ({ page }) => {
  const mcp = new McpClient()
  try {
    await mcp.rpc('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'e2e', version: '1' } })
    const prop = await mcp.tool('propose_change', {
      context: KIND_CONTEXT, group: 'apps', kind: 'Deployment', namespace: 'demo', name: 'web',
      merge_patch: { metadata: { labels: { [LABEL]: 'yes' } } }, summary: 'label from e2e',
    })
    const p = JSON.parse(prop.text)
    expect(p.token).toBeTruthy()

    // The user switches it off in the app while the agent is still running.
    await fresh(page)
    await openDialog(page, KIND_CONTEXT)
    await page.getByTestId('agent-write').uncheck()
    await page.getByTestId('agent-save').click()
    await expect(page.getByTestId('agent-access')).toHaveCount(0)

    const refused = await mcp.tool('apply_change', { context: KIND_CONTEXT, token: p.token })
    expect(refused.isError).toBe(true)
    expect(refused.text).toMatch(/disabled/)
    expect(kubectl('get', 'deployment', 'web', '-n', 'demo', '-o', 'jsonpath={.metadata.labels}')).not.toContain(LABEL)

    // A new proposal is diff only: no token and no dry-run.
    const again = JSON.parse((await mcp.tool('propose_change', {
      context: KIND_CONTEXT, group: 'apps', kind: 'Deployment', namespace: 'demo', name: 'web',
      merge_patch: { metadata: { labels: { [LABEL]: 'yes' } } }, summary: 'label from e2e',
    })).text)
    expect(again.writesEnabled).toBe(false)
    expect(again.token).toBeUndefined()
    expect(again.note).toContain('Acesso de agentes')
  } finally {
    mcp.close()
    removeLabel()
  }
})
