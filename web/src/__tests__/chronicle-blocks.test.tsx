/**
 * T-513 rich elements, driven through the REAL `ChroniclePageView` route (rule 12) — the slug
 * resolver is wired at that call site, so an injected one would stay green while it was deleted.
 * SANDBOX GUARD IS THE ATTRIBUTE: jsdom never parses srcdoc, so "the script did not run" is
 * vacuous here and cannot fail. Measurements and the nine mutations: CHANGELOG T-513.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { ChroniclePageView } from '../screens/Chronicle.js';
// `?raw` rather than node:fs — resolves relative to THIS file, so the guard cannot silently read
// the wrong stylesheet depending on cwd (the pattern tokens.test.ts established).
import rawCss from '../index.css?raw';

/**
 * Mermaid MOCKED to fail its render. Not cosmetic: the real ~500KB module renders for real here,
 * appends a scratch element to document.body and settles AFTER the test that triggered it, which
 * timed out an unrelated case at "Loading…" on a whole-suite run.
 */
vi.mock('mermaid', () => ({
  default: {
    initialize: () => undefined,
    render: () => Promise.reject(new Error('mermaid unavailable in this runner')),
  },
}));
import { ProjectProvider } from '../projectContext.js';

const PAGE = {
  id: 'CP-1', projectId: 'P-1', kind: 'adr' as const, slug: 'adr/t-513', title: 'A record',
  headVersionId: 'CV-1', sourcePath: null,
  createdAt: '2026-08-25T00:00:00Z', updatedAt: '2026-08-25T00:00:00Z',
};
/** The index the slug map is built from — one OTHER record, so `adr/t-494` resolves and nothing else does. */
const INDEX = {
  pages: [
    PAGE,
    { ...PAGE, id: 'CP-2', slug: 'adr/t-494', title: 'The cited record' },
    // The namespaces `emit.py:631,635` mints. Plural `topics/`/`features/` is a REPO PATH and has
    // never been a hosted slug, so a page under one cannot exist to resolve to.
    { ...PAGE, id: 'CP-3', kind: 'topic' as const, slug: 'topic/wake', title: 'Wake channel' },
    { ...PAGE, id: 'CP-4', kind: 'feature' as const, slug: 'feature/t-292', title: 'Feature: t-292' },
  ],
};

const SCRIPT_HTML = '<script>window.__chr_pwned = true;</script><p>after the script</p>';

const block = (id: string, text: string) => ({
  id, state: 'derived' as const, text, citations: [], origin: null,
});

const HEAD = {
  id: 'CV-1', pageId: 'CP-1', parentVersionId: null, source: 'sweep' as const,
  author: 'a@b.c', agent: null, ts: '2026-08-25T00:00:00Z',
  blocks: [
    block('ctx1/1', '```html\n' + SCRIPT_HTML + '\n```'),
    block('ctx1/2', '```html\n<p id="benign">a benign paragraph</p>\n```'),
    block('ctx1/3', '```html\n<!-- nothing but a comment -->\n```'),
    block('ctx2/1', 'See adr/t-494 and adr/no-such-record for context.'),
    block('ctx2/4', 'Grouped under topic/wake, and the feature page is feature/t-292.'),
    block('ctx2/5', 'The repo files are topics/wake.md and features/t-292.md, not records.'),
    block('ctx2/2', '```ts\nconst x: number = 1;\n```'),
    block('ctx2/3', '```\nplain fence, no language\n```'),
    block('ctx3/1', '> coverage: partial — derived from `T-1` on the board.'),
    block('ctx3/2', 'coverage: full — every field this record claims was read.'),
    block('ctx3/3', 'NOT VERIFIED — the frame height on a narrow viewport.'),
    block('ctx3/4', 'This supersedes the earlier REJECTED item about BigQuery, which no longer applies.'),
    block('ctx4/1', '## Evidence\n\nThe first section.'),
    block('ctx4/2', '## Evidence\n\nA second block with the same heading.'),
    block('ctx4/3', '- NOT VERIFIED — the list-item case.\n- a plain item.'),
    block('ctx5/1', '```mermaid\ngraph TD; A-->B;\n```'),
  ],
};

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

function stubServer(): void {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
    if (url.includes('/chronicle/CP-1')) return json({ page: PAGE, head: HEAD, drifted: [], conflicts: null });
    if (url.endsWith('/chronicle')) return json(INDEX);
    if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
    return json({});
  }));
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); delete (window as unknown as Record<string, unknown>).__chr_pwned; });

async function mount(): Promise<void> {
  stubServer();
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/chronicle/CP-1']}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => screen.getByText(/after the script|The first section/));
}

const frames = (): HTMLIFrameElement[] => [...document.querySelectorAll('iframe')] as HTMLIFrameElement[];
const scriptFrame = (): HTMLIFrameElement =>
  frames().find((f) => (f.getAttribute('srcdoc') ?? '').includes('__chr_pwned'))!;

describe('T-513 sandboxed HTML — the refusal', () => {
  it('a block whose HTML contains <script> renders in a frame with an EMPTY sandbox', async () => {
    await mount();
    const f = scriptFrame();
    expect(f, 'the ```html fence did not render a frame at all').toBeTruthy();
    // `toBe('')`, never `toBeFalsy()`: a MISSING attribute is null — full privileges — and would
    // satisfy a falsy check.
    expect(f.hasAttribute('sandbox'), 'the sandbox attribute is gone — the frame is fully privileged').toBe(true);
    expect(f.getAttribute('sandbox'), 'the sandbox was widened — an allow-* token re-enables execution').toBe('');
  });

  it('no allow-* token reaches the frame, and the CSP is in the document it loads', async () => {
    await mount();
    const f = scriptFrame();
    expect(f.getAttribute('sandbox') ?? '').not.toMatch(/allow-/);
    const doc = f.getAttribute('srcdoc') ?? '';
    expect(doc).toContain('http-equiv="Content-Security-Policy"');
    expect(doc).toContain("default-src 'none'");
  });

  it('the script is INERT, not stripped — the boundary is the sandbox, not a sanitizer', async () => {
    await mount();
    // The source survives verbatim: nothing here pretends to clean the markup.
    expect(scriptFrame().getAttribute('srcdoc')).toContain('<script>window.__chr_pwned = true;</script>');
    // NOT LOAD-BEARING under jsdom — a canary for a runner that does execute srcdoc.
    expect((window as unknown as Record<string, unknown>).__chr_pwned).toBeUndefined();
    expect(document.body.textContent).not.toContain('window.__chr_pwned = true');
  });

  it('POSITIVE CONTROL: a benign HTML block still renders its markup in a frame', async () => {
    await mount();
    const benign = frames().find((f) => (f.getAttribute('srcdoc') ?? '').includes('id="benign"'));
    expect(benign, 'the benign HTML block did not render — the guard over-restricted').toBeTruthy();
    expect(benign!.getAttribute('srcdoc')).toContain('a benign paragraph');
    expect(benign!.getAttribute('sandbox')).toBe('');
  });

  it('HTML that would render nothing degrades to its SOURCE, never a blank frame', async () => {
    await mount();
    const src = document.querySelector('pre.chr-html-src');
    expect(src, 'a comment-only fence produced a blank frame instead of showing its source').toBeTruthy();
    expect(src!.textContent).toContain('<!-- nothing but a comment -->');
    expect(frames().some((f) => (f.getAttribute('srcdoc') ?? '').includes('nothing but a comment'))).toBe(false);
  });
});

describe('T-513 internal record links', () => {
  it('POSITIVE CONTROL: a slug that resolves becomes a link to that record', async () => {
    await mount();
    // Waits for the slug-map query, which is a SEPARATE fetch from the page itself.
    await waitFor(() => expect(screen.getByText('adr/t-494').closest('a')).toBeTruthy());
    const a = screen.getByText('adr/t-494').closest('a')!;
    expect(a.getAttribute('href')).toBe('/chronicle/CP-2');
  });

  it('REFUSAL: an unknown slug stays plain text — no dead link, no "missing" label', async () => {
    await mount();
    await waitFor(() => expect(screen.getByText('adr/t-494').closest('a')).toBeTruthy());
    expect(screen.queryByText('adr/no-such-record')).toBeNull();
    const para = [...document.querySelectorAll('p')].find((p) => p.textContent?.includes('no-such-record'))!;
    expect(para.textContent).toContain('adr/no-such-record');
    expect([...para.querySelectorAll('a')].map((x) => x.textContent)).toEqual(['adr/t-494']);
  });

  it('the namespaces the EMITTER mints resolve — topic/ and feature/, not the plural repo paths', async () => {
    await mount();
    // `emit.py:631,635` mint `topic/<slug>` and `feature/<key>`. The regex read `topics|features`,
    // so a reference to either could never match and the feature was dead for those pages.
    await waitFor(() => expect(screen.getByText('adr/t-494').closest('a')).toBeTruthy());
    const linked = new Map(
      [...document.querySelectorAll('a.chr-record-link')].map((a) => [a.textContent, a.getAttribute('href')]),
    );
    expect(linked.get('topic/wake'), 'a topic/ reference is not linkified — the namespaces do not match emit.py').toBe('/chronicle/CP-3');
    expect(linked.get('feature/t-292'), 'a feature/ reference is not linkified').toBe('/chronicle/CP-4');
  });

  it('REFUSAL: a plural `topics/`/`features/` repo path is prose, not a record reference', async () => {
    await mount();
    await waitFor(() => expect(screen.getByText('adr/t-494').closest('a')).toBeTruthy());
    const para = [...document.querySelectorAll('p')].find((p) => p.textContent?.includes('topics/wake.md'))!;
    expect([...para.querySelectorAll('a')], 'a repo path was linkified as a record').toEqual([]);
  });

  it('an internal record link is ROUTED, never a new-tab full page load', async () => {
    await mount();
    await waitFor(() => expect(screen.getByText('adr/t-494').closest('a')).toBeTruthy());
    const a = screen.getByText('adr/t-494').closest('a')!;
    // `mdComponents.a` forces target="_blank" — right for an outbound reference, wrong for a
    // record in this same app, which would reload the SPA to reach a route it already has.
    expect(a.getAttribute('target'), 'an internal record link opens a new tab and reloads the SPA').toBeNull();
    expect(a.className).toContain('chr-record-link');
    // POSITIVE CONTROL: an OUTBOUND link keeps the new-tab treatment.
    const out = [...document.querySelectorAll('a[href^="http"]')];
    expect(out.every((x) => x.getAttribute('target') === '_blank')).toBe(true);
  });
});

describe('T-513 code fences carry their language', () => {
  it('a ts fence is labelled; a fence with no language is not wrapped', async () => {
    await mount();
    expect(document.querySelector('.chr-code[data-lang="ts"]')).toBeTruthy();
    expect(document.querySelector('.chr-code[data-lang="ts"] pre code')!.textContent).toContain('const x: number = 1;');
    expect([...document.querySelectorAll('.chr-code')].length).toBe(1);
    expect(document.body.textContent).toContain('plain fence, no language');
  });
});

describe('T-513 callouts', () => {
  it('coverage: partial and NOT VERIFIED are warnings; coverage: full is not', async () => {
    await mount();
    const warn = [...document.querySelectorAll('.chr-callout.is-warn')].map((n) => n.textContent ?? '');
    expect(warn.some((t) => t.includes('coverage: partial'))).toBe(true);
    expect(warn.some((t) => t.includes('NOT VERIFIED'))).toBe(true);
    const ok = [...document.querySelectorAll('.chr-callout.is-ok')].map((n) => n.textContent ?? '');
    expect(ok.some((t) => t.includes('coverage: full'))).toBe(true);
  });

  it('REFUSAL: the marker only counts where it OPENS a block — mid-sentence is prose', async () => {
    await mount();
    // 222 REJECTED occurrences in the records, only 7 open a block.
    const all = [...document.querySelectorAll('.chr-callout')].map((n) => n.textContent ?? '');
    expect(all.some((t) => t.includes('supersedes the earlier REJECTED'))).toBe(false);
  });

  it('a blockquote callout does not nest a second callout in its paragraph', async () => {
    await mount();
    expect(document.querySelector('.chr-callout .chr-callout')).toBeNull();
  });

  it('a list-item callout lives INSIDE the <li> — `<ul>` admits no <div>', async () => {
    await mount();
    expect(
      document.querySelector('ul > .chr-callout, ol > .chr-callout'),
      'a callout <div> is a direct child of a list — the content model forbids it',
    ).toBeNull();
    // POSITIVE CONTROL: the list item is still decorated, just from within.
    const inLi = document.querySelector('li > .chr-callout.is-warn');
    expect(inLi, 'the list-item callout was dropped — the fix over-restricted').toBeTruthy();
    expect(inLi!.textContent).toContain('NOT VERIFIED');
  });
});

describe('T-513 anchored headings', () => {
  it('a heading is linkable, and two blocks sharing a heading get DISTINCT ids', async () => {
    await mount();
    const hs = [...document.querySelectorAll('h2.chr-h')].filter((h) => h.textContent?.includes('Evidence'));
    expect(hs.length).toBe(2);
    const ids = hs.map((h) => h.id);
    expect(ids.every(Boolean), 'a heading has no id — the section is not linkable').toBe(true);
    expect(new Set(ids).size, 'two blocks produced the same DOM id — one anchor is unreachable').toBe(2);
    expect(hs[0]!.querySelector('a.chr-anchor')!.getAttribute('href')).toBe(`#${ids[0]}`);
  });

  it('the anchor glyph is out of the tab order and NOT hidden from assistive tech', async () => {
    await mount();
    const a = document.querySelector('h2.chr-h a.chr-anchor') as HTMLAnchorElement;
    expect(a, 'the heading carries no anchor link').toBeTruthy();
    // `aria-hidden` on a FOCUSABLE element is a WCAG violation (0985a837). `tabIndex={-1}` is what
    // keeps the decoration out of the tab order without hiding it.
    expect(a.tabIndex, 'the anchor glyph is back in the tab order — one stop per heading').toBe(-1);
    expect(a.getAttribute('aria-hidden'), 'a focusable element hidden from assistive tech').toBeNull();
  });
});

describe('T-513 the HTML frame is BOUNDED', () => {
  it('every rendered frame carries the class the height rule selects', async () => {
    await mount();
    // `className="chr-html"` is the ONLY thing binding the stylesheet rule below to the DOM, and
    // nothing observed it: without it the frame falls back to the UA default 300x150.
    expect(frames().length, 'no ```html fence rendered a frame at all').toBeGreaterThan(0);
    expect(
      document.querySelectorAll('iframe.chr-html').length,
      'a frame renders without .chr-html — the height rule selects nothing',
    ).toBe(frames().length);
  });

  it('index.css caps the frame height — an unbounded srcdoc frame pushes the record page around', () => {
    // Asserted on the STYLESHEET — jsdom computes no layout, and without the rule the frame
    // silently becomes the 150px unscrolled default.
    const rule = /\.chr-html\s*\{[^}]*\}/.exec(rawCss)?.[0] ?? '';
    expect(rule, '.chr-html has no rule at all — the frame is unbounded').toBeTruthy();
    // Read as DECLARATIONS, not a substring: `/height:\s*\d/` also matched the `height: 6` inside
    // `max-height: 60vh`, so deleting the fixed height it is named for kept this green.
    const decls = rule.replace(/^[^{]*\{|\}\s*$/g, '').split(';').map((d) => d.trim());
    expect(
      decls.some((d) => /^height:\s*\d/.test(d)),
      'the fixed frame height is gone — max-height alone leaves the 150px UA default',
    ).toBe(true);
  });
});

describe('T-513 a mermaid fence is not wrapped in a stray <pre>', () => {
  it('no <pre> nests inside another <pre>', async () => {
    await mount();
    // Both fences render their OWN container and each fallback is itself a <pre>, so the default
    // wrapper nests a <pre> in a <pre>.
    await waitFor(() => expect(document.querySelector('pre.chr-mermaid-src')).toBeTruthy());
    expect(document.querySelector('pre pre'), 'a <pre> nested inside a <pre> — the fence wrapper is back').toBeNull();
    expect(document.querySelector('pre.chr-mermaid-src')!.textContent).toContain('graph TD; A-->B;');
  });
});
