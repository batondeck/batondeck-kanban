/**
 * The white-screen guard.
 *
 * Before `errors.tsx` the SPA had NO error boundary: any throw during render unmounted the whole tree
 * and left `#root` empty — the user saw a blank page and we saw nothing at all. These tests assert the
 * two halves of the fix that actually matter to a user: something is still on screen, and the crash
 * left the building.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { ErrorBoundary, reportClientError } from '../errors.js';

function Boom(): never {
  throw new Error('Cannot read properties of undefined (reading map)');
}

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
  vi.stubGlobal('fetch', fetchMock);
  // React logs caught errors to console.error by design; silence it so a passing run reads clean.
  vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('ErrorBoundary', () => {
  it('renders a usable fallback instead of an empty page when a child throws', () => {
    const { container } = render(
      <MemoryRouter>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </MemoryRouter>,
    );
    // THE assertion this whole file exists for: the DOM is not empty.
    expect(container.textContent?.trim().length, 'the crash produced a blank page').toBeGreaterThan(0);
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByText(/unexpected error/i)).toBeInTheDocument();
    // A way out, not just an apology.
    expect(screen.getByRole('button', { name: /reload/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
  });

  it('reports the crash to the gateway with its stack (otherwise the failure is undiscoverable)', () => {
    render(
      <MemoryRouter>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </MemoryRouter>,
    );
    const call = fetchMock.mock.calls.find((c) => String(c[0]).includes('/api/client-error'));
    expect(call, 'the crash was never reported — a white screen we cannot see is the status quo').toBeTruthy();
    const body = JSON.parse(String((call?.[1] as RequestInit).body)) as { msg: string; stack: string };
    expect(body.msg).toContain('Cannot read properties of undefined');
    expect(body.stack).toContain('component stack');
    // keepalive: a crash is usually followed by a reload, which cancels a normal fetch mid-flight.
    expect((call?.[1] as RequestInit).keepalive).toBe(true);
  });

  it('POSITIVE CONTROL: a child that does not throw renders untouched', () => {
    // Without this, the fallback assertions above would pass just as happily on a boundary that
    // replaced every screen with an error panel.
    render(
      <ErrorBoundary>
        <p>the actual screen</p>
      </ErrorBoundary>,
    );
    expect(screen.getByText('the actual screen')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('reporting never throws, even when fetch itself is broken', () => {
    // A reporter that throws inside an error handler turns one bug into a loop.
    vi.stubGlobal('fetch', () => {
      throw new Error('network stack is gone');
    });
    expect(() => reportClientError(new Error('x'))).not.toThrow();
  });
});
