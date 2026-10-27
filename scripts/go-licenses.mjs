// Writes the license text of every Go module linked into the engine binary (from its build info),
// so the packaged app carries the notices its dependencies require. Works on Linux, macOS and Windows.
// Usage: node scripts/go-licenses.mjs <engine binary> <output file>   (GO=<go binary> to override)
import { execFileSync } from 'node:child_process'
import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'

const [bin, out] = process.argv.slice(2)
if (!bin || !out) throw new Error('usage: go-licenses.mjs <engine binary> <output file>')
const go = process.env.GO || 'go'
const modcache = execFileSync(go, ['env', 'GOMODCACHE'], { encoding: 'utf8' }).trim()
const LICENSE_FILE = /^(licen[cs]e|copying|notice)([-_.][a-z0-9._-]*)?$/i

// "dep <module> <version> <sum>" lines; "=>" lines carry the replacement actually linked.
const mods = new Map()
for (const line of execFileSync(go, ['version', '-m', bin], { encoding: 'utf8' }).split('\n')) {
  const f = line.trim().split(/\s+/)
  if (f[0] === 'dep' || f[0] === '=>') mods.set(`${f[1]}@${f[2]}`, [f[1], f[2]])
}

const sep = '='.repeat(78)
const parts = ['The Leme engine links the following Go modules, unmodified. Their licenses follow.']
for (const [mod, ver] of [...mods.values()].sort((a, b) => a[0].localeCompare(b[0]))) {
  // The module cache escapes upper-case letters as "!" + lower-case.
  const dir = path.join(modcache, `${mod.replace(/[A-Z]/g, (c) => '!' + c.toLowerCase())}@${ver}`)
  const files = readdirSync(dir).filter((f) => LICENSE_FILE.test(f)).sort()
  if (!files.length) throw new Error(`no license file for ${mod}@${ver} (${dir})`)
  parts.push([`${sep}\n\n${mod}@${ver}`, ...files.map((f) => readFileSync(path.join(dir, f), 'utf8').trim())].join('\n\n'))
}
writeFileSync(out, parts.join('\n\n') + '\n')
console.log(`${mods.size} modules -> ${out}`)
