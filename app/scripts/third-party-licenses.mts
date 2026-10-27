// Vite plugin: writes THIRD_PARTY_LICENSES.txt next to the built UI with the license text of every
// npm package that actually ended up in the bundle (JS, CSS and fonts), so the packaged app carries
// the notices its dependencies require. The Go engine's list is produced by scripts/go-licenses.sh.
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import type { Plugin } from 'vite'

const LICENSE_FILE = /^(licen[cs]e|copying|notice)(\.(md|txt|markdown))?$/i

function packageRoot(id: string): string | undefined {
  const m = id.replace(/\\/g, '/').match(/^(.*\/node_modules\/(?:@[^/]+\/)?[^/]+)\//)
  return m?.[1]
}

export function thirdPartyLicenses(): Plugin {
  return {
    name: 'third-party-licenses',
    apply: 'build',
    generateBundle() {
      const roots = new Set<string>()
      for (const id of this.getModuleIds()) {
        const root = packageRoot(id.split('?')[0])
        if (root) roots.add(root)
      }
      const parts: string[] = []
      for (const root of [...roots].sort((a, b) => path.basename(a).localeCompare(path.basename(b)))) {
        const pkg = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8')) as { name: string; version: string; license?: string; repository?: string | { url?: string } }
        const repo = typeof pkg.repository === 'string' ? pkg.repository : pkg.repository?.url ?? ''
        const files = readdirSync(root).filter((f) => LICENSE_FILE.test(f))
        // Monaco bundles code from other projects and ships their notices alongside its own.
        if (existsSync(path.join(root, 'ThirdPartyNotices.txt'))) files.push('ThirdPartyNotices.txt')
        const texts = files.map((f) => readFileSync(path.join(root, f), 'utf8').trim())
        // A few packages publish without their license file: the text is vendored in scripts/licenses/<name>.txt
        // ("/" in scoped names becomes "__").
        const vendored = new URL(`./licenses/${pkg.name.replace('/', '__')}.txt`, import.meta.url)
        if (!texts.length && existsSync(vendored)) texts.push(readFileSync(vendored, 'utf8').trim())
        if (!texts.length) this.error(`no license file in ${pkg.name}@${pkg.version}`)
        parts.push([`${pkg.name}@${pkg.version} — ${pkg.license ?? 'see below'}${repo ? `\n${repo}` : ''}`, ...texts].join('\n\n'))
      }
      const header = 'Leme bundles the following third-party packages, unmodified. Their licenses follow.\n'
      this.emitFile({ type: 'asset', fileName: 'THIRD_PARTY_LICENSES.txt', source: [header, ...parts].join(`\n\n${'='.repeat(78)}\n\n`) + '\n' })
    },
  }
}
