/**
 * The web UI asks nothing of a third-party host.
 *
 * A self-hosted board holds a team's internal links. A link card used to load its icon from
 * Google's favicon service, so every viewer's browser told Google each linked hostname (an internal
 * CI or wiki host included) along with the viewer's address, and the page shell loaded Google Fonts
 * on every page, the sign-in page included. The "admin privacy setting" that was to turn the
 * favicons off has no server side in this build, so nobody could.
 */
import { describe, it, expect, afterEach } from 'vitest';
// `?raw`, not node:fs: the web tsconfig carries no node typings (card-pending.test.tsx says why).
import indexHtml from '../../index.html?raw';
import { render, cleanup } from '@testing-library/react';
import { AppLinkCard } from '../AppLink.js';
import { hydrateUiSettings, type UiSettings } from '../settings.js';

afterEach(() => {
  hydrateUiSettings(undefined);
  cleanup();
});

describe('link cards', () => {
  it('an internal link renders with no request to any other host', () => {
    hydrateUiSettings(undefined); // what /api/session delivers here: no overrides
    const { container } = render(<AppLinkCard url="https://build-secrets.internal-corp.example/job/42" />);
    expect(container.textContent).toContain('build-secrets.internal-corp.example');
    const external = [...container.querySelectorAll('img')].map((i) => i.getAttribute('src') ?? '').filter((s) => /^(https?:)?\/\//.test(s));
    expect(external, 'the card asked a third party about the linked host').toEqual([]);
  });

  it('no session override can bring the favicon lookup back', () => {
    // The shape auth.tsx hands over from /api/session. The card had a Google favicon branch behind a
    // `linkFavicons` setting; any later override that set it would have restored the request.
    const fromServer: Record<string, number | boolean> = { linkFavicons: true };
    hydrateUiSettings(fromServer as Partial<UiSettings>);
    const { container } = render(<AppLinkCard url="https://wiki.internal-corp.example/page" />);
    const external = [...container.querySelectorAll('img')].map((i) => i.getAttribute('src') ?? '').filter((s) => /^(https?:)?\/\//.test(s));
    expect(external, 'an override turned the third-party favicon lookup back on').toEqual([]);
  });
});

describe('the page shell', () => {
  it('index.html loads no stylesheet, font or script from another origin', () => {
    const refs = [...indexHtml.matchAll(/<(?:link|script|img)\b[^>]*\b(?:href|src)\s*=\s*"([^"]*)"/gi)].map((m) => m[1]!);
    expect(refs.length, 'no reference found: this check would pass vacuously').toBeGreaterThan(0);
    expect(refs.filter((r) => /^(https?:)?\/\//i.test(r))).toEqual([]);
  });
});
