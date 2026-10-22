// THIRD_PARTY_NOTICES, written by `make notices` and held to its inputs by `make notices-check`.
//
// The file carries the licence text of everything the binary built from this repository
// redistributes, as the Go tool reports it, classified by nobody: the Go modules whose packages the
// shipped binary links on any platform a release ships: one `go list -deps ./cmd/batondeck-kanban`
// per entry of the Makefile's PLATFORMS (handed in as NOTICES_PLATFORMS, so the list has one copy),
// each with GOOS and GOARCH set and CGO_ENABLED=0 as `make dist` builds, under GOWORK=off; the
// union, each module with the LICENSE, COPYING, UNLICENSE and NOTICE files its directory holds.
//
// The header records the platforms and the sha256 of go.sum, what changes when the set does.
// `--check` recomputes those and compares, which needs no tool but node, so it runs on every
// `make check`. When the web UI's packages land, their set (web/package-lock.json) joins INPUTS.
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const OUT = 'THIRD_PARTY_NOTICES'
const INPUTS = ['go.sum']
const LICENCE_FILE = /^(licen[cs]e|copying|unlicense|notice)([.\-_].*)?$/i

const fail = (msg) => { console.error(`notices: ${msg}`); process.exit(1) }
const PLATFORMS = (process.env.NOTICES_PLATFORMS || '').trim().split(/\s+/).filter(Boolean)
if (!PLATFORMS.length) fail('NOTICES_PLATFORMS is not set: run it as make notices or make notices-check, which pass the Makefile\'s PLATFORMS')
const sha256 = (p) => createHash('sha256').update(readFileSync(join(root, p))).digest('hex')
const inputLines = () => [`  ${'platforms'.padEnd(28)}${PLATFORMS.join(' ')}`, ...INPUTS.map((p) => `  ${p.padEnd(28)}sha256 ${sha256(p)}`)]
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
  console.log(`notices-check: ${OUT} matches ${PLATFORMS.join(' ')} and ${INPUTS.join(', ')}`)
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

function goModules() {
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
      files: licenceFiles(m.Replace ? m.Replace.Dir : m.Dir),
    })
  }
  return [...mods.values()].sort(byId)
}

function render(sections) {
  const rule = '='.repeat(80), line = '-'.repeat(80)
  const out = [
    'Third-party notices for batondeck-kanban', rule, '',
    'What the binary built from this repository redistributes, under the licences below: the Go',
    'modules it links. Each entry carries the licence files as the dependency\'s own source holds',
    'them; this file classifies nothing.', '',
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
    { title: 'Go modules the shipped binary links', how: `go list -deps ./cmd/batondeck-kanban for each of ${PLATFORMS.join(', ')} (CGO_ENABLED=0, GOWORK=off), the union; the licence files of each module directory`, entries: goModules() },
  ]
  const missing = sections.flatMap((s) => s.entries.filter((e) => e.files.length === 0).map((e) => `${s.title}: ${e.id}`))
  if (missing.length) fail(`no licence file found for:\n  ${missing.join('\n  ')}`)
  writeFileSync(join(root, OUT), render(sections))
  console.log(`notices: wrote ${OUT} (${sections.map((s) => `${s.entries.length} in "${s.title}"`).join(', ')})`)
}
