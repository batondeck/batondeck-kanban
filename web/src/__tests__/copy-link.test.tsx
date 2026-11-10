/**
 * "Copy link" works on a board served over plain HTTP from another host.
 *
 * `navigator.clipboard` exists only in a secure context (HTTPS or localhost). The drawer's and the
 * ticket page's copy buttons called `navigator.clipboard.writeText` unguarded, so on
 * http://10.0.0.5:8080 the click threw `Cannot read properties of undefined (reading 'writeText')`:
 * nothing copied, nothing said. Where the API exists but is refused, the rejection went unhandled.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { TaskPage } from '../screens/TaskPage.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { ToastHost } from '../toast.js';
import { copyLink, copyText } from '../clipboard.js';
import { routeFetch } from './taskFixtures.js';

let copied: string[] = [];
let execResult = true;
const execCommand = vi.fn((cmd: string) => {
  if (cmd !== 'copy') return false;
  const ta = document.activeElement as HTMLTextAreaElement | null;
  if (execResult && ta?.tagName === 'TEXTAREA') copied.push(ta.value.slice(ta.selectionStart, ta.selectionEnd));
  return execResult;
});

beforeEach(() => {
  copied = [];
  execResult = true;
  execCommand.mockClear();
  vi.stubGlobal('fetch', routeFetch());
  // An insecure context: no Clipboard API at all.
  Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
  Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const qc = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

describe('the drawer and the ticket page copy their link without the Clipboard API', () => {
  it('the drawer copies the ?task= link and says so', async () => {
    render(
      <QueryClientProvider client={qc()}>
        <AuthProvider>
          <ToastHost />
          <TaskDrawer projectId="p1" taskId="T-1" onClose={() => {}} onOpenTask={() => {}} />
        </AuthProvider>
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Copy link' }));
    await waitFor(() => expect(screen.getByText('Link to T-1 copied')).toBeInTheDocument());
    expect(copied).toHaveLength(1);
    expect(new URL(copied[0]!).searchParams.get('task')).toBe('T-1');
    expect(document.querySelector('textarea[data-copy]')).toBeNull(); // the helper cleans up after itself
  });

  it('the ticket page copies its URL and says so', async () => {
    render(
      <QueryClientProvider client={qc()}>
        <AuthProvider>
          <ToastHost />
          <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
            <ProjectProvider>
              <Routes>
                <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
              </Routes>
            </ProjectProvider>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Copy link to this ticket' }));
    await waitFor(() => expect(screen.getByText('Link to T-1 copied')).toBeInTheDocument());
    expect(copied).toEqual([window.location.href]);
  });
});

describe('copyText', () => {
  it('uses the Clipboard API where there is one', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    await expect(copyText('hello')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('hello');
    expect(execCommand).not.toHaveBeenCalled();
  });

  it('a refused Clipboard API falls back to the textarea copy', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn(async () => { throw new Error('NotAllowedError'); }) }, configurable: true });
    await expect(copyText('hello')).resolves.toBe(true);
    expect(copied).toEqual(['hello']);
  });

  it('answers false, without throwing, when neither way copies', async () => {
    execResult = false;
    await expect(copyText('hello')).resolves.toBe(false);
  });

  it('copyLink offers the link in a prompt, selected, when nothing copies', async () => {
    execResult = false;
    const prompt = vi.fn(() => null);
    vi.stubGlobal('prompt', prompt);
    copyLink('http://10.0.0.5:8080/projects/p1/boards/b1?task=T-1', 'done');
    await waitFor(() => expect(prompt).toHaveBeenCalledWith('Copy this link:', 'http://10.0.0.5:8080/projects/p1/boards/b1?task=T-1'));
  });
});
