import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  testIgnore: process.env.PERF ? [] : ['perf/**'],
  timeout: 120_000,
  expect: { timeout: 20_000 },
  workers: 1,
  reporter: [['list']],
  use: { baseURL: undefined, channel: 'chrome', headless: true, viewport: { width: 1500, height: 900 }, trace: 'retain-on-failure' },
  outputDir: './e2e/test-results',
})
