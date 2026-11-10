/**
 * The SPA's crash floor: nothing a React render throws should reach the user as a blank page.
 *
 * There was no boundary at all before this file. Any throw during render — a screen reading a field
 * off an undefined API response, a bad cast, a broken third-party component — unmounted the entire
 * tree and left `#root` empty. The user saw white, with nothing to quote in a bug report.
 *
 * Two boundaries, not one, and the split is the whole point of "graceful":
 *   - the OUTER one in `main.tsx` catches provider/router crashes, where there is no shell left to keep;
 *   - the INNER one in `App.tsx` wraps only the routed screen, so a crashed Board leaves the nav, the
 *     project switcher and every other screen still clickable. That is the difference between "one
 *     screen is broken" and "the app is broken".
 *
 * NOT a replacement for per-screen query error states. React error boundaries catch RENDER throws only
 * — not a rejected fetch, not an event handler, not an async callback. Those are covered elsewhere
 * (`MutationCache.onError` → toast, `fire()` → toast, and each screen's own `isError` branch).
 *
 * NOTHING IS SENT ANYWHERE. This server has no client-error route (internal/api/routes.json leaves
 * `/api/client-error` out by decision), so the crash stays in the browser: React logs it to the
 * console, and the panel shows the message and asks the user to report it. It used to post every
 * crash, window error and unhandled rejection there and say "It has been reported" over a 405.
 *
 * THE ONE WHITE SCREEN THIS CANNOT CATCH is the bundle failing to load or parse at all — no JS runs, so
 * no boundary exists to catch anything. Low risk today only because nothing here is code-split (no
 * `React.lazy`, no dynamic `import()` in app code), so there is no stale-chunk 404 after a deploy. If
 * code-splitting is ever introduced, that gap opens and needs an answer outside this file.
 */
import { Component, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
  /** `full` owns the viewport (no shell survived); `inline` sits inside the app chrome. */
  variant?: 'full' | 'inline';
}

export class ErrorBoundary extends Component<Props, { err: Error | null }> {
  override state: { err: Error | null } = { err: null };

  static getDerivedStateFromError(err: Error): { err: Error } {
    return { err };
  }

  override render(): ReactNode {
    const { err } = this.state;
    if (!err) return this.props.children;
    const full = this.props.variant === 'full';
    return (
      <div className={`crash ${full ? 'crash-full' : ''}`} role="alert">
        <div className="crash-title">This screen hit an unexpected error</div>
        <p className="crash-hint">
          {full ? 'Reloading usually clears it.' : 'The rest of the app still works.'} If it keeps happening, report it
          with the message below: nothing was sent automatically.
        </p>
        {/* The message, not the stack: enough for a user to quote in a bug report, no internals on screen. */}
        <p className="crash-detail">{err.message}</p>
        <div className="crash-actions">
          {/* Clearing `err` re-renders the same subtree — worth one click before a full reload, since a
              transient bad render (a race on stale data) recovers from it and a real bug does not. */}
          <button className="btn" onClick={() => this.setState({ err: null })}>
            Try again
          </button>
          <button className="btn" onClick={() => location.reload()}>
            Reload
          </button>
          {!full && (
            <a className="btn" href="/projects">
              Back to projects
            </a>
          )}
        </div>
      </div>
    );
  }
}
