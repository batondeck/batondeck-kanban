// BatonDeck's logo, copied from the BatonDeck product (its portal/public/batondeck/ folder) into
// public/brand/, served at /brand/, and pinned here so a changed or stray file fails the gate.
//
//   node tools/brand.mjs check   THE GATE, hermetic: public/brand/ holds exactly the files
//                                brand.lock.json names, each with the sha256 it records
//
// To move to a newer logo: copy the files from the product's portal/public/batondeck/, then write
// their sha256s and the product commit into brand.lock.json in the same change.
import { createHash } from 'node:crypto'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

export const APP = join(dirname(fileURLToPath(import.meta.url)), '..')

/** Every way public/brand/ differs from brand.lock.json. Empty is a match. */
export function brandProblems(app = APP) {
  const lock = JSON.parse(readFileSync(join(app, 'brand.lock.json'), 'utf8'))
  const dir = join(app, 'public', 'brand')
  const out = []
  const present = readdirSync(dir).filter((f) => f !== '.DS_Store').sort()
  const want = Object.keys(lock.files).sort()
  for (const f of present) if (!want.includes(f)) out.push(`public/brand/${f} is not in brand.lock.json`)
  for (const f of want) {
    if (!present.includes(f)) { out.push(`public/brand/${f} is missing`); continue }
    const got = createHash('sha256').update(readFileSync(join(dir, f))).digest('hex')
    if (got !== lock.files[f]) out.push(`public/brand/${f} is not the file brand.lock.json pins (${lock.source})`)
  }
  return out
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [cmd] = process.argv.slice(2)
  if (cmd !== 'check') { console.error('usage: brand.mjs check'); process.exit(2) }
  const problems = brandProblems()
  if (problems.length) { console.error(`brand: ${problems.length} problem(s)\n  ${problems.join('\n  ')}`); process.exit(1) }
  const lock = JSON.parse(readFileSync(join(APP, 'brand.lock.json'), 'utf8'))
  console.log(`brand: public/brand/ is BatonDeck's logo, ${Object.keys(lock.files).length} files as pinned (${lock.source})`)
}
