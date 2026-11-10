/**
 * api-http#4 — attach_file requires a fileName, and list_attachments now answers it. The Files panel
 * shows it on the tile, and the download link names the saved file with it (the server answers
 * `Content-Disposition: attachment` with no filename, so the link's `download` name is the one the
 * browser uses). An attachment from before names were kept (fileName "") shows its kind, as before.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch } from './taskFixtures.js';

const row = (id: string, fileName: string, sha: string) => ({
  id, taskId: 'T-1', gcsUri: `blob:p1.T-1.${id}`, mimeType: 'application/pdf', bytes: 2048, sha256: sha, thumbUri: null, extractedText: null,
  kind: 'file', status: 'ready', createdBy: 'alice@example.com', ts: '2026-11-10T00:00:00Z', fileName,
  downloadUrl: `http://localhost:3000/blobs/${sha}?exp=1793368000&sig=${'d'.repeat(64)}`,
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('the Files panel names each attachment', () => {
  it('shows the file name on the tile and names the download with it', async () => {
    vi.stubGlobal('fetch', routeFetch({
      '/tasks/T-1/attachments$': { json: { attachments: [row('A-1', 'spec v2.pdf', 'a'.repeat(64)), row('A-0', '', 'b'.repeat(64))] } },
    }));
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider>
          <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
        </AuthProvider>
      </QueryClientProvider>,
    );
    expect(await screen.findByText('spec v2.pdf')).toBeInTheDocument();
    const named = document.querySelector(`a[href*="/blobs/${'a'.repeat(64)}"]`);
    expect(named).toHaveAttribute('download', 'spec v2.pdf');
    await waitFor(() => expect(document.querySelector(`a[href*="/blobs/${'b'.repeat(64)}"]`)).not.toBeNull());
    expect(document.querySelector(`a[href*="/blobs/${'b'.repeat(64)}"]`)).not.toHaveAttribute('download');
  });
});
