/**
 * The SPA's crash floor: nothing a React render throws should reach the user as a blank page.
 *
 * There was no boundary at all before this file. Any throw during render — a screen reading a field
 * off an undefined API response, a bad cast, a broken third-party component — unmounted the entire
 * tree and left `#root` empty. The user saw white, and we saw NOTHING: the stack died in a console
 * nobody is attached to, so the failure could not be discovered, counted, or fixed.
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
 * THE ONE WHITE SCREEN THIS CANNOT CATCH is the bundle failing to load or parse at all — no JS runs, so
 * no boundary exists to catch anything. Low risk today only because nothing here is code-split (no
 * `React.lazy`, no dynamic `import()` in app code), so there is no stale-chunk 404 after a deploy. If
 * code-splitting is ever introduced, that gap opens and needs an answer outside this file.
 */
import { Component, type ErrorInfo, type ReactNode } from 'react';

/**
 * Post the crash to `/api/client-error`. This server mounts no such route (internal/api/routes.json
 * leaves it out), so the post answers 404 and the crash is only in the browser console; the call is
 * kept so a server that adds the route receives reports with no SPA change.
 *
 * `keepalive` because a crash is frequently followed by the user closing the tab or hitting reload —
 * a plain fetch would be cancelled in flight and the one report we needed would never arrive.
 * Failures are swallowed on purpose: a reporter that throws inside an error handler is a loop.
 */
export function reportClientError(err: unknown, extra?: Record<string, string>): void {
  const e = err instanceof Error ? err : new Error(String(err));
  try {
    void fetch('/api/client-error', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      keepalive: true,
      body: JSON.stringify({ msg: e.message, stack: e.stack ?? '', url: location.pathname + location.search, ...extra }),
    }).catch(() => {});
  } catch {
    /* reporting must never be the thing that breaks the page */
  }
}

/**
 * Window-level nets for what a boundary structurally cannot see: throws in event handlers, timers,
 * and unhandled promise rejections. Installed once from `main.tsx`.
 *
 * These REPORT but do not render — by the time they fire, React has usually kept the page intact, so
 * replacing it with an error panel would be a downgrade. The user-visible path for a failed action is
 * still the toast.
 */
export function installClientErrorReporting(): void {
  window.addEventListener('error', (ev) => reportClientError(ev.error ?? ev.message));
  window.addEventListener('unhandledrejection', (ev) => reportClientError(ev.reason));
}

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

  override componentDidCatch(err: Error, info: ErrorInfo): void {
    reportClientError(err, { stack: `${err.stack ?? err.message}\n--- component stack ---${info.componentStack ?? ''}` });
  }

  override render(): ReactNode {
    const { err } = this.state;
    if (!err) return this.props.children;
    const full = this.props.variant === 'full';
    return (
      <div className={`crash ${full ? 'crash-full' : ''}`} role="alert">
        <div className="crash-title">This screen hit an unexpected error</div>
        <p className="crash-hint">
          It has been reported. {full ? 'Reloading usually clears it.' : 'The rest of the app still works.'}
        </p>
        {/* The message, not the stack: enough for a user to quote in a bug report, no internals on screen. */}
        <p className="crash-detail">{err.message}</p>
        <div className="crash-actions">
          {/* Clearing `err` re-renders the same subtree — worth one click before a full reload, since a
              transient bad render (a race on stale data) recovers from it and a real bug does not.
              ponytail: KNOWN CEILING — a deterministic crash re-throws and fires another beacon POST
              per click. Bounded by a human clicking, so it is noise rather than an amplifier; add a
              retry counter here if a single crash ever shows up as dozens of grouped events. */}
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
