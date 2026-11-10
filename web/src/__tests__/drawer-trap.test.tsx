/**
 * THE DRAWER'S TAB TRAP (web-board#15, fix-up round 1).
 *
 * The trap counted a control hidden by the `hidden` attribute (or a stylesheet) as the dialog's last,
 * so Tab from the last control a person can see walked out once before the trap pulled it back; and
 * while the ticket was still loading there was no dialog at all, so Tab walked the board behind the
 * overlay.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { trapTab } from '../screens/task/trapTab.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.body.innerHTML = '';
});

function tab(shiftKey = false): KeyboardEvent {
  return new KeyboardEvent('keydown', { key: 'Tab', shiftKey, cancelable: true });
}

describe('trapTab', () => {
  it('a hidden control is not the last: Tab from the last shown one goes back to the first', () => {
    const root = document.createElement('div');
    root.innerHTML = '<button>first</button><button>shown last</button><div hidden><button>hidden</button></div><input type="hidden">';
    document.body.appendChild(root);
    const [first, shownLast] = [...root.querySelectorAll('button')];
    shownLast!.focus();
    const e = tab();
    trapTab(e, root);
    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(first);
    const back = tab(true);
    trapTab(back, root);
    expect(document.activeElement).toBe(shownLast);
  });

  it('a dialog with nothing to focus keeps the focus on itself', () => {
    const root = document.createElement('div');
    root.tabIndex = -1;
    document.body.appendChild(root);
    const outside = document.createElement('button');
    document.body.appendChild(outside);
    outside.focus();
    const e = tab();
    trapTab(e, root);
    expect(e.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(root);
  });
});

describe('the drawer while its ticket loads', () => {
  it('is a modal dialog that holds the focus, and Tab stays in it', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => {})));
    const behind = document.createElement('button');
    behind.textContent = 'a card behind';
    document.body.appendChild(behind);
    behind.focus();
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider>
          <TaskDrawer projectId="p1" taskId="T-1" onClose={() => {}} />
        </AuthProvider>
      </QueryClientProvider>,
    );
    const dialog = await screen.findByRole('dialog', { name: 'Loading T-1' });
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    await waitFor(() => expect(document.activeElement).toBe(dialog));
    behind.focus();
    fireEvent.keyDown(behind, { key: 'Tab' });
    expect(document.activeElement).toBe(dialog);
  });
});
