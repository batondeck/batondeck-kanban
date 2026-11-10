/**
 * api-http#2 — WITH public_url SET, THE SERVER SIGNS EVERY BLOB URL AT public_url, whatever origin the
 * browser reached it at. The page's CSP is `connect-src 'self'` and `img-src 'self'` and /blobs has
 * no CORS, so a board used at http://localhost:8080 while public_url names https://board.example.com
 * could not PUT an upload (the attachment stayed pending) or show an embedded image. The signature
 * covers only (purpose, id, expiry), so the SPA moves a signed /blobs URL onto its own origin.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch, task } from './taskFixtures.js';
import { onOwnOrigin } from '../api/media.js';

const SHA = 'c'.repeat(64);
const FAR = 'https://board.example.com';
const signedAt = (base: string, id: string) => `${base}/blobs/${id}?exp=1793368000&sig=${'d'.repeat(64)}`;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('onOwnOrigin', () => {
  it('moves a signed /blobs URL onto the page origin and leaves every other URL as it was', () => {
    const own = window.location.origin;
    expect(onOwnOrigin(signedAt(FAR, SHA))).toBe(signedAt(own, SHA));
    expect(onOwnOrigin(signedAt('http://board.example.com', 'P-1.T-1.A-1'))).toBe(signedAt(own, 'P-1.T-1.A-1'));
    for (const other of [
      'https://example.com/spec.png',
      `${FAR}/blobs/${SHA}`, // not signed
      `${FAR}/assets/blobs/${SHA}?exp=1&sig=2`,
      'not a url at all ::',
      // Already on this origin: a relative signed URL (the Chronicle's stable form, P-244) and an
      // absolute one both come back exactly as they were.
      `/blobs/${SHA}?exp=1793368000&sig=${'d'.repeat(64)}`,
      signedAt(own, SHA),
    ]) {
      expect(onOwnOrigin(other)).toBe(other);
    }
  });
});

describe('the task drawer uses blob URLs on its own origin', () => {
  it('PUTs a dropped file to the upload URL on the page origin, links the download and shows an embedded image there', async () => {
    const own = window.location.origin;
    const base = routeFetch({
      '/tasks/T-1$': { json: { task: { ...task, description: `See ![shot](${signedAt(FAR, SHA)})` } } },
    });
    const calls: Array<{ url: string; method: string }> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (/\/tasks\/T-1\/attachments$/.test(url) && method === 'POST') {
        return new Response(JSON.stringify({ attachmentId: 'A-1', uploadUrl: signedAt(FAR, 'p1.T-1.A-1'), expiresAt: '2026-11-10T00:15:00Z' }), { status: 200 });
      }
      if (/\/tasks\/T-1\/attachments$/.test(url)) {
        return new Response(JSON.stringify({ attachments: [{
          id: 'A-0', taskId: 'T-1', gcsUri: 'blob:p1.T-1.A-0', mimeType: 'text/plain', bytes: 3, sha256: SHA, thumbUri: null,
          extractedText: null, kind: 'file', status: 'ready', createdBy: 'alice@example.com', ts: '2026-11-10T00:00:00Z', downloadUrl: signedAt(FAR, SHA),
        }] }), { status: 200 });
      }
      if (url.includes('/blobs/')) return new Response(null, { status: 200 });
      return base(input, init);
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider>
          <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
        </AuthProvider>
      </QueryClientProvider>,
    );
    const img = await screen.findByRole('img', { name: 'shot' });
    expect(img).toHaveAttribute('src', signedAt(own, SHA));
    await waitFor(() => expect(document.querySelector(`a[href*="/blobs/${SHA}"]`)).not.toBeNull());
    expect(document.querySelector(`a[href*="/blobs/${SHA}"]`)).toHaveAttribute('href', signedAt(own, SHA));

    const drop = await screen.findByRole('button', { name: /Drop a file/ });
    await waitFor(() => expect(drop).toBeEnabled());
    fireEvent.drop(drop, { dataTransfer: { files: [new File(['abc'], 'a.txt', { type: 'text/plain' })] } });
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true));
    const put = calls.find((c) => c.method === 'PUT')!;
    expect(put.url).toBe(signedAt(own, 'p1.T-1.A-1'));
  });
});
