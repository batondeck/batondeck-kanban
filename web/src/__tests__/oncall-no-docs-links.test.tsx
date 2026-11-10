/**
 * THE ON-CALL SETUP LINKS TO NO `/docs/` PAGE (P-247).
 *
 * `docsHref` built `/docs/<page>.html`, a path copied from the hosted product, where a docs site was
 * served there. This binary serves no `/docs/` route: the SPA catch-all answers the shell, so the
 * "Read the guide" and "Wiring guide" buttons were dead, so they were removed. The one guide the
 * setup links now is the repository's docs/oncall.md, on GitHub, from the endpoint panel
 * (oncall-endpoint-signing.test.tsx). Each case first finds the panel the link sat in, so a zero
 * count cannot come from a screen that never rendered.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';

/** The wire shape `GET /api/oncall/sources` answers: the server still sends `docsPage`. */
const CATALOG = {
  sources: [
    { id: 'webhook', label: 'Custom webhook', authModes: ['hmac'], docsPage: 'oncall-webhook', authNote: 'Any tool that can POST JSON.' },
    { id: 'grafana', label: 'Grafana', authModes: ['api_key'], docsPage: 'oncall-grafana', authNote: 'Grafana can send a custom header.' },
  ],
};

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('/api/oncall/sources')) return json(CATALOG);
      return json({ integrations: [], deliveries: [], boards: [] });
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

function renderSetup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[]} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

const docsLinks = (root: HTMLElement): string[] =>
  Array.from(root.querySelectorAll('a[href^="/docs/"]')).map((a) => a.getAttribute('href') ?? '');

describe('the on-call setup renders no /docs/ href', () => {
  it('in the source picker, beside the custom connector card', async () => {
    const { container } = renderSetup();
    expect(await screen.findByText('Not listed?')).toBeTruthy();
    expect(screen.getByRole('button', { name: /Use a custom webhook/i })).toBeTruthy();
    expect(docsLinks(container)).toEqual([]);
  });

  it('in the create form for the custom connector', async () => {
    const { container } = renderSetup();
    fireEvent.click(await screen.findByRole('button', { name: /Use a custom webhook/i }));
    expect(await screen.findByText('New Custom webhook integration')).toBeTruthy();
    expect(docsLinks(container)).toEqual([]);
  });

  it('in the create form for a catalogue connector', async () => {
    const { container } = renderSetup();
    fireEvent.click(await screen.findByRole('button', { name: /Grafana/i }));
    expect(await screen.findByText('New Grafana integration')).toBeTruthy();
    expect(docsLinks(container)).toEqual([]);
  });
});
