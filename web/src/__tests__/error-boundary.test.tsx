/**
 * The white-screen guard.
 *
 * Before `errors.tsx` the SPA had NO error boundary: any throw during render unmounted the whole tree
 * and left `#root` empty — the user saw a blank page and we saw nothing at all. These tests assert the
 * two halves of the fix that actually matter to a user: something is still on screen, and the screen
 * does not claim a report nobody received.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { ErrorBoundary } from '../errors.js';

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

  /**
   * NOTHING CLAIMS A REPORT THAT WAS NEVER MADE. The boundary posted every crash (and every
   * window error and unhandled rejection) to `/api/client-error`, a route this server leaves out by
   * decision (internal/api/routes.json), and told the user "It has been reported". The POST was
   * answered 405, the report existed nowhere, and the console gained a second error beside the real
   * one. The screen now asks the user to report it, with the message to quote.
   */
  it('sends nothing, and asks the user to report it instead of saying it was reported', () => {
    render(
      <MemoryRouter>
        <ErrorBoundary>
          <Boom />
        </ErrorBoundary>
      </MemoryRouter>,
    );
    expect(fetchMock, 'the crash was posted to a route the server does not have').not.toHaveBeenCalled();
    const alert = screen.getByRole('alert');
    expect(alert.textContent).not.toMatch(/has been reported/i);
    expect(alert.textContent).toMatch(/report it/i);
    expect(alert.textContent).toContain('Cannot read properties of undefined');
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
});
