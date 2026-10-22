/**
 * THE CLI TOKEN TABLE NEVER RENDERS AN IDENTIFIER.
 *
 * Reported from staging as "redact token once generated — it shouldn't be shown ever once generated
 * and copied". Nothing was leaking: the secret is never stored (`src/domain/cli-token.ts` keeps
 * `secretHash` only and states there is deliberately no Store method that could return one), and the
 * column showed `display` — the public half of the id.
 *
 * That distinction is invisible to a reader. `bd_bd7c064b…` sitting in a credentials table looks
 * exactly like a leaked token, and renaming the header to "ID" was not enough — the owner's call was
 * to remove it, which is right: the label identifies the row, and the id is only ever needed as the
 * argument Revoke sends.
 *
 * LEXICAL, and deliberately so. The property is the ABSENCE of a rendering, and the cheapest check
 * that can see an absence in JSX is a read of the source. A render test can only assert that some
 * particular string is missing from some particular fixture, which is weaker: it passes for a
 * fixture whose token happens to be empty.
 *
 * The API is UNCHANGED on purpose. `display` still ships in the tool response, because removing a
 * field from a published contract to fix a display decision is the wrong lever — the surface is
 * pinned by `test/integration/contract.test.ts` and consumed by MCP clients this repo does not own.
 */
import { describe, it, expect } from 'vitest';

const SETTINGS = Object.values(
  import.meta.glob('../screens/Settings.tsx', { query: '?raw', import: 'default', eager: true }) as Record<string, string>,
)[0] as string;

/** Comments quote `t.display` while explaining why it is gone; an unstripped scan fails on itself. */
const strip = (s: string): string =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

describe('the CLI token table shows no identifier', () => {
  it('the source scan found its subject — otherwise every assertion here is vacuous', () => {
    expect(SETTINGS, 'Settings.tsx not found').toBeTruthy();
    expect(SETTINGS).toContain('CLI tokens');
  });

  it('never renders the token display half', () => {
    expect(
      strip(SETTINGS),
      'the CLI token table must not render `t.display` — an id in a credentials table reads as a leaked secret',
    ).not.toMatch(/\{\s*t\.display\s*\}/);
  });

  it('POSITIVE CONTROL: it still renders the label and the revoke control', () => {
    // Without this, deleting the whole table would satisfy the assertion above.
    const src = strip(SETTINGS);
    expect(src, 'the label is how a row is identified now').toMatch(/\{\s*t\.label\s*\}/);
    expect(src, 'Revoke still needs the id as its ARGUMENT, just never as output').toMatch(/revoke\.mutate\(t\.id\)/);
  });
});
