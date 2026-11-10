// The brand from hdtp-web-kit, vendored into the SPA the way hdtp-gateway's portal vendors it (this
// file is its web/tools/kit.mjs, cut to the lock): by release, every file checked against the
// release's manifest, pinned in kit/kit.lock. It works on the directory above its own (web/).
//
//   node tools/kit.mjs fetch <x.y.z>   download the release with gh, check it against its manifest, vendor it
//   node tools/kit.mjs check           THE GATE, hermetic: the vendored bytes, the lock, the manifest
//
// This app takes its own brand files and nothing else: the fonts, the stylesheet, analytics.js,
// mixpanel and waitlist.js are not used here, and neither are the marks of the kit's other products
// (kit/brand/hdtp-*), which a BatonDeck binary has no page for.
//
//   kit/brand/*      ->  public/brand/        served at /brand/
//   manifest.json    ->  kit/manifest.json    the release's own, verbatim
import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

export const APP = join(dirname(fileURLToPath(import.meta.url)), '..')
export const REPO = 'humandelegatedtrustprotocol/hdtp-web-kit'
/** Release path prefix -> where it lands in the app. A release file matching none is not vendored. */
export const TAKE = [
  ['kit/brand/', 'public/brand/'],
]
const LOCK = join(APP, 'kit/kit.lock')
const MANIFEST = join(APP, 'kit/manifest.json')

const sha256 = (b) => createHash('sha256').update(b).digest('hex')
/** The kit's other products' marks: under a taken path, and not taken. */
const NOT_TAKEN = /^kit\/brand\/hdtp-/
const destOf = (path) => {
  if (NOT_TAKEN.test(path)) return null
  for (const [from, to] of TAKE) if (path === from || (from.endsWith('/') && path.startsWith(from))) return to + path.slice(from.length)
  return null
}
function walk(dir, base = dir) {
  if (!existsSync(dir)) return []
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    return statSync(p).isDirectory() ? walk(p, base) : [relative(base, p)]
  })
}

// ---------------------------------------------------------------- fetch

function fetchRelease(version) {
  if (!/^\d+\.\d+\.\d+$/.test(version || '')) throw new Error('usage: kit.mjs fetch <x.y.z>')
  const tmp = mkdtempSync(join(tmpdir(), 'hdtp-web-kit-'))
  try {
    execFileSync('gh', ['release', 'download', `v${version}`, '-R', REPO, '-D', tmp, '-p', `hdtp-web-kit-${version}.tar.gz`, '-p', 'manifest.json'], { stdio: 'inherit' })
    const rawManifest = readFileSync(join(tmp, 'manifest.json'))
    const manifest = JSON.parse(rawManifest)
    if (manifest.version !== version) throw new Error(`the manifest is for ${manifest.version}, not ${version}`)
    const root = join(tmp, 'x'); mkdirSync(root)
    execFileSync('tar', ['-xzf', join(tmp, `hdtp-web-kit-${version}.tar.gz`), '-C', root])
    // The whole unpacked release must be the manifest, byte for byte, before any of it is taken.
    const have = Object.fromEntries(['kit', 'bin'].flatMap((d) => walk(join(root, d)).map((f) => [`${d}/${f}`, sha256(readFileSync(join(root, d, f)))])))
    if (JSON.stringify(Object.entries(have).sort()) !== JSON.stringify(Object.entries(manifest.files).sort())) {
      throw new Error('the unpacked release does not match its manifest; nothing was vendored')
    }
    for (const [, to] of TAKE) rmSync(join(APP, to), { recursive: true, force: true })
    const files = {}
    for (const [path, hash] of Object.entries(manifest.files).sort()) {
      const to = destOf(path)
      if (!to) continue
      mkdirSync(dirname(join(APP, to)), { recursive: true })
      cpSync(join(root, path), join(APP, to))
      files[path] = { to, sha256: hash }
    }
    mkdirSync(dirname(LOCK), { recursive: true })
    writeFileSync(MANIFEST, rawManifest)
    writeFileSync(LOCK, JSON.stringify({ version, manifest_sha256: sha256(rawManifest), files }, null, 2) + '\n')
    console.log(`hdtp-web-kit ${version}: ${Object.keys(files).length} files vendored, pinned in kit/kit.lock`)
  } finally {
    rmSync(tmp, { recursive: true, force: true })
  }
}

// ---------------------------------------------------------------- the lock

/** Every way the vendored copy can differ from the pinned release. Empty is a match. */
export function lockProblems(app = APP) {
  const out = []
  const lockPath = join(app, 'kit/kit.lock'), manPath = join(app, 'kit/manifest.json')
  if (!existsSync(lockPath) || !existsSync(manPath)) return ['kit/kit.lock or kit/manifest.json is missing: run `node tools/kit.mjs fetch <x.y.z>`']
  const lock = JSON.parse(readFileSync(lockPath, 'utf8'))
  const rawManifest = readFileSync(manPath)
  const manifest = JSON.parse(rawManifest)
  if (sha256(rawManifest) !== lock.manifest_sha256) out.push('kit/manifest.json is not the manifest the lock pinned')
  if (manifest.version !== lock.version) out.push(`the manifest is ${manifest.version}, the lock ${lock.version}`)
  // Every release file destOf takes is vendored: the whole of this app's brand set, not a pick of it.
  const want = Object.keys(manifest.files).filter((p) => destOf(p)).sort()
  const got = Object.keys(lock.files).sort()
  if (JSON.stringify(want) !== JSON.stringify(got)) out.push(`the lock lists ${got.length} files, the release has ${want.length} under the taken paths`)
  const vendored = new Set()
  for (const [path, { to, sha256: hash }] of Object.entries(lock.files)) {
    if (manifest.files[path] !== hash) out.push(`${path}: the lock's hash is not the manifest's`)
    if (destOf(path) !== to) out.push(`${path}: locked at ${to}, belongs at ${destOf(path)}`)
    const p = join(app, to)
    if (!existsSync(p)) { out.push(`${to} is missing`); continue }
    if (sha256(readFileSync(p)) !== hash) out.push(`${to} differs from hdtp-web-kit ${lock.version}'s ${path}`)
    vendored.add(to)
  }
  // Nothing else in the vendored directories: a stray file there would be served as if it were the kit's.
  for (const dir of [...TAKE.map(([, to]) => to.replace(/\/$/, '')), 'kit']) {
    for (const f of walk(join(app, dir))) {
      const rel = `${dir}/${f}`
      if (!vendored.has(rel) && rel !== 'kit/kit.lock' && rel !== 'kit/manifest.json') out.push(`${rel} is in a vendored directory but not in the lock`)
    }
  }
  return out
}

// ---------------------------------------------------------------- main

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [cmd, arg] = process.argv.slice(2)
  try {
    if (cmd === 'fetch') fetchRelease(arg)
    else if (cmd === 'check') {
      const problems = lockProblems()
      if (problems.length) {
        console.error(`kit: ${problems.length} problem(s)\n  ` + problems.join('\n  '))
        process.exit(1)
      }
      const lock = JSON.parse(readFileSync(LOCK, 'utf8'))
      console.log(`kit: hdtp-web-kit ${lock.version}, ${Object.keys(lock.files).length} files match the release`)
    } else {
      console.error('usage: kit.mjs fetch <x.y.z> | kit.mjs check'); process.exit(2)
    }
  } catch (e) {
    console.error(`kit: ${e.message}`); process.exit(1)
  }
}
