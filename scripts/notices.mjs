// THIRD_PARTY_NOTICES, written by `make notices` and held to its inputs by `make notices-check`.
//
// The file carries the licence text of everything the binary built from this repository
// redistributes, classified by nobody, in two sections.
//
// The Go modules whose packages the shipped binary links on any platform a release ships, as the Go
// tool reports them: one `go list -deps ./cmd/batondeck-kanban` per entry of the Makefile's
// PLATFORMS (handed in as NOTICES_PLATFORMS, so the list has one copy), each with GOOS and GOARCH
// set and CGO_ENABLED=0 as `make dist` builds, under GOWORK=off; the union, each module with the
// LICENSE, COPYING, UNLICENSE and NOTICE files its directory holds.
//
// The npm packages the embedded web UI (web/dist) is built from: every package
// web/package-lock.json installs that is not dev-only. That is a superset of what the bundler keeps
// (it drops code no page imports), which is the safe direction for a notices file. Each package
// with the licence files its directory under web/node_modules holds, or its package.json `license`
// field where it ships none; so `make notices` needs `cd web && npm ci` first.
//
// The header records the platforms, the sha256 of go.sum and of web/package-lock.json, and the two
// sets (each one's count and the sha256 of its sorted ids). `--check` recomputes those and
// compares: it reads the two lock files and runs `go list -deps` once per platform, reading no
// licence text and no node_modules, so it runs on every `make check`.
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const OUT = 'THIRD_PARTY_NOTICES'
const INPUTS = ['go.sum', 'web/package-lock.json']
const NPM_LOCK = 'web/package-lock.json'
const LICENCE_FILE = /^(licen[cs]e|copying|unlicense|notice)([.\-_].*)?$/i

const fail = (msg) => { console.error(`notices: ${msg}`); process.exit(1) }
const PLATFORMS = (process.env.NOTICES_PLATFORMS || '').trim().split(/\s+/).filter(Boolean)
if (!PLATFORMS.length) fail('NOTICES_PLATFORMS is not set: run it as make notices or make notices-check, which pass the Makefile\'s PLATFORMS')
const sha256 = (p) => createHash('sha256').update(readFileSync(join(root, p))).digest('hex')
// The header's input lines: the platforms, the sha256 of each of INPUTS, and the set of modules the
// binary links (their count and the sha256 of their sorted ids). go.sum alone does not say what is
// linked: it holds every module the module graph needs, so a package that starts importing one
// already in it changes the linked set and leaves go.sum as it was.
const linkedLine = () => {
  const ids = goModules().map((m) => m.id)
  const hash = createHash('sha256').update(ids.join('\n')).digest('hex')
  return `  ${'linked Go modules'.padEnd(28)}${ids.length}, sha256 ${hash}`
}
const npmLine = () => {
  const ids = npmPackages().map((m) => m.id)
  const hash = createHash('sha256').update(ids.join('\n')).digest('hex')
  return `  ${'bundled npm packages'.padEnd(28)}${ids.length}, sha256 ${hash}`
}
const inputLines = () => [`  ${'platforms'.padEnd(28)}${PLATFORMS.join(' ')}`, ...INPUTS.map((p) => `  ${p.padEnd(28)}sha256 ${sha256(p)}`), linkedLine(), npmLine()]
const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
const run = (cmd, args, cwd, env = {}) =>
  execFileSync(cmd, args, { cwd, encoding: 'utf8', maxBuffer: 1 << 28, env: { ...process.env, GOWORK: 'off', ...env } })

function check() {
  let text
  try { text = readFileSync(join(root, OUT), 'utf8') } catch { fail(`${OUT} is missing: run make notices and commit it`) }
  const stale = inputLines().filter((l) => !text.includes(`\n${l}\n`))
  if (stale.length) {
    fail(`${OUT} was not made from the dependency set on disk; its header does not carry:\n` +
      `${stale.map((l) => l.trim()).join('\n')}\nrun make notices and commit the result`)
  }
  console.log(`notices-check: ${OUT} matches ${PLATFORMS.join(' ')}, ${INPUTS.join(', ')}, the ${goModules().length} Go modules the binary links and the ${npmPackages().length} npm packages the web UI is built from`)
}

// Every licence-like file in dir, sorted by name; none when the directory has none or is not there.
function licenceFiles(dir) {
  let names
  try { names = readdirSync(dir) } catch { return [] }
  return names.filter((n) => LICENCE_FILE.test(n)).sort().map((n) => ({ name: n, text: readFileSync(join(dir, n), 'utf8') }))
}

// `go list -json` prints one object after another, so the stream is split at the top-level braces,
// skipping the ones inside strings.
function splitJSON(text) {
  const objs = []
  let depth = 0, start = 0, inString = false
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inString) { if (c === '\\') i++; else if (c === '"') inString = false; continue }
    if (c === '"') inString = true
    else if (c === '{') { if (depth++ === 0) start = i }
    else if (c === '}' && --depth === 0) objs.push(JSON.parse(text.slice(start, i + 1)))
  }
  return objs
}

let goModulesMemo
function goModules() {
  if (goModulesMemo) return goModulesMemo
  const mods = new Map()
  const linked = PLATFORMS.flatMap((plat) => {
    const [goos, goarch] = plat.split('/')
    return splitJSON(run('go', ['list', '-deps', '-json=Module,Standard', './cmd/batondeck-kanban'], root,
      { GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' }))
  })
  for (const p of linked) {
    if (p.Standard || !p.Module || p.Module.Main) continue
    const m = p.Module
    const id = `${m.Path} ${m.Version}`
    if (mods.has(id)) continue
    mods.set(id, {
      id,
      note: m.Replace ? `replaced by ${m.Replace.Path} (go.mod)` : '',
      dir: m.Replace ? m.Replace.Dir : m.Dir,
    })
  }
  goModulesMemo = [...mods.values()].sort(byId)
  return goModulesMemo
}

// The packages web/package-lock.json installs that are not dev-only, read from the lock file alone
// (lockfileVersion 3: one entry per installed path, `dev: true` on what only devDependencies
// reach). A name and version installed at two paths is one entry, read from the first.
let npmPackagesMemo
function npmPackages() {
  if (npmPackagesMemo) return npmPackagesMemo
  const lock = JSON.parse(readFileSync(join(root, NPM_LOCK), 'utf8'))
  if (lock.lockfileVersion !== 3) fail(`${NPM_LOCK} is lockfileVersion ${lock.lockfileVersion}; this script reads version 3`)
  const pkgs = new Map()
  for (const [path, e] of Object.entries(lock.packages)) {
    if (!path || e.dev || e.link) continue
    const id = `${path.slice(path.lastIndexOf('node_modules/') + 'node_modules/'.length)} ${e.version}`
    if (!pkgs.has(id)) pkgs.set(id, { id, note: '', dir: join(root, 'web', path) })
  }
  npmPackagesMemo = [...pkgs.values()].sort(byId)
  return npmPackagesMemo
}

// A package's licence files, or, where its directory holds none, the licence its package.json
// declares, as one line. None when it has neither (make notices then fails, naming it).
function npmLicenceFiles(dir) {
  const files = licenceFiles(dir)
  if (files.length) return files
  let pkg
  try { pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) } catch { fail(`${dir} is not installed: run cd web && npm ci, then make notices`) }
  const declared = typeof pkg.license === 'string' ? pkg.license : pkg.license && pkg.license.type
  if (!declared) return []
  const who = typeof pkg.author === 'string' ? pkg.author : pkg.author && pkg.author.name
  return [{ name: 'package.json', text: `license: ${declared}${who ? `\nauthor: ${who}` : ''}\n(the package ships no licence file; this is what its package.json declares)` }]
}

function render(sections) {
  const rule = '='.repeat(80), line = '-'.repeat(80)
  const out = [
    'Third-party notices for batondeck-kanban', rule, '',
    'What the binary built from this repository redistributes, under the licences below: the Go',
    'modules it links, and the npm packages its embedded web UI is built from. Each entry carries',
    'the licence files as the dependency\'s own source holds them; this file classifies nothing.', '',
    'Written by `make notices` (scripts/notices.mjs) for these platforms from these inputs, which `make notices-check`',
    '(part of `make check`) holds it to:', '', ...inputLines(), '', 'Contents',
  ]
  sections.forEach((s, i) => out.push(`  ${i + 1}. ${s.title}: ${s.entries.length}`))
  sections.forEach((s, i) => {
    out.push('', '', rule, `${i + 1}. ${s.title}`, `   ${s.how}`, rule)
    for (const e of s.entries) {
      out.push('', line, e.id, ...(e.note ? [e.note] : []), line)
      for (const f of e.files) out.push(`[${f.name}]`, f.text.replace(/\r\n/g, '\n').replace(/\s+$/, ''), '')
    }
  })
  return out.join('\n') + '\n'
}

if (process.argv.includes('--check')) {
  check()
} else {
  const sections = [
    { title: 'Go modules the shipped binary links', how: `go list -deps ./cmd/batondeck-kanban for each of ${PLATFORMS.join(', ')} (CGO_ENABLED=0, GOWORK=off), the union; the licence files of each module directory`, entries: goModules().map((m) => ({ ...m, files: licenceFiles(m.dir) })) },
    { title: 'npm packages the embedded web UI is built from', how: `every package ${NPM_LOCK} installs that is not dev-only (more than the bundle keeps); the licence files of each package directory, or its package.json licence where it ships none`, entries: npmPackages().map((m) => ({ ...m, files: npmLicenceFiles(m.dir) })) },
  ]
  const missing = sections.flatMap((s) => s.entries.filter((e) => e.files.length === 0).map((e) => `${s.title}: ${e.id}`))
  if (missing.length) fail(`no licence file found for:\n  ${missing.join('\n  ')}`)
  writeFileSync(join(root, OUT), render(sections))
  console.log(`notices: wrote ${OUT} (${sections.map((s) => `${s.entries.length} in "${s.title}"`).join(', ')})`)
}
