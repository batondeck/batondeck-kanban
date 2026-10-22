import { useEffect, useState } from 'react';

import { BrandLockup } from '../brand';

export function SignIn() {
  const params = new URLSearchParams(window.location.search);
  const error = params.get('error');
  const access = params.get('access');
  const [demoLogin, setDemoLogin] = useState(false);
  // `workosLogin` from /api/public-config is deliberately NOT read any more. It used to gate whether
  // the WorkOS button rendered alongside Google's (T-77, "both providers live at once"); with Google
  // gone there is nothing to gate — WorkOS is the only front door, so a false value would leave this
  // page with no way in at all rather than a fallback. The gateway still reports the field; it is the
  // Admin/config surface's, not this screen's.
  useEffect(() => {
    fetch('/api/public-config')
      .then((r) => r.json())
      .then((c) => {
        setDemoLogin(!!c?.demoLogin);
      })
      .catch(() => {});
  }, []);
  // THERE IS NO PROVIDER CHOOSER (owner, 2026-07-29). WorkOS brokers Google, Apple, email+password,
  // email OTP and passkey, so a local chooser was a second, weaker copy of a menu AuthKit already
  // renders — and it asked users to choose between two words ("Google" / "WorkOS") that mean the same
  // thing to them. `/login` now goes straight to WorkOS, which is also why the landing page needed no
  // edits: all seven of its CTAs point at `/login`.
  //
  // THREE CONDITIONS STOP THE REDIRECT, and each one exists to prevent a specific dead end:
  //  1. `error` — a failed sign-in MUST stay readable. Redirecting here sends the user back to the
  //     IdP that just refused them: an infinite loop, with no message explaining why.
  //  2. `access` — same, for pending/denied workspace access. The whole point of that message is
  //     that signing in again will not help.
  //  3. `demoLogin` — `/auth/demo` is only reachable from this page. Auto-redirecting past it makes
  //     the demo unreachable wherever it is switched on.
  // In all three cases the page renders with a manual button instead.
  //
  // `replace`, not `href`: Back must not land on a page that immediately bounces forward again.
  const autoRedirect = !error && !access && !demoLogin;
  useEffect(() => {
    if (autoRedirect) window.location.replace('/auth/workos');
  }, [autoRedirect]);
  const accessMsg =
    access === 'requested'
      ? { error: false, text: 'Access requested — an admin will review it. Sign in again once approved.' }
      : access === 'pending'
        ? { error: false, text: 'Your access request is still pending admin approval.' }
        : access === 'denied'
          ? { error: true, text: 'This account has been denied access to the workspace.' }
          : null;
  return (
    <div className="center" style={{ position: 'relative', overflow: 'hidden' }}>
      <div
        aria-hidden
        style={{
          position: 'absolute',
          top: '50%',
          left: '50%',
          transform: 'translate(-50%, -50%)',
          width: 400,
          height: 400,
          background:
            // Tokens, not literals: this baked in #6d4fd0 (brand-literal-ok: the retired violet,
            // named so the note stays checkable) fading into the old
            // grey canvas. Both moved with the revamp, so the halo was painting the previous
            // palette over the new one on the very first screen a visitor sees.
            'radial-gradient(circle, color-mix(in srgb, var(--accent) 13%, transparent) 0%, transparent 70%)',
          pointerEvents: 'none',
          zIndex: 0,
        }}
      />
      <div
        className="signin-card"
        style={{
          position: 'relative',
          zIndex: 1,
          width: 360,
          padding: 32,
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
        }}
      >
        {/* The kit's lockup (brand.tsx). It stays an <h1> for the document outline; the accessible
            name comes from the lockup's aria-label, so the brand is announced exactly once. */}
        <h1 style={{ margin: '0 0 8px', display: 'flex', justifyContent: 'center' }}>
          <BrandLockup height={32} />
        </h1>
        <p
          className="muted"
          style={{ fontSize: 14, lineHeight: '20px', margin: '0 0 28px' }}
        >
          The task board your agents and your team share
        </p>

        {error && (
          <p className="error" style={{ fontSize: 12, margin: '0 0 16px' }}>
            {error === 'forbidden'
              ? 'This account is not permitted to sign in.'
              : error === 'no_tenant'
                ? // T-270: the gateway resolved no organization for this account, so no session was
                  // minted. A CODE, not the message itself, for the same reason `forbidden` is one —
                  // the fallback line below reflects the query parameter verbatim, and copy a user is
                  // expected to act on does not belong in a URL anyone can edit. Naming the remedy is
                  // the whole point: "sign in again" is what this user would otherwise try, and it
                  // cannot work until somebody else does something.
                  // T-387 NARROWED WHO REACHES THIS, so the copy had to change with it. It used to be
                  // every tenant-less sign-in; a user with NO membership is now onboarded instead, so
                  // the only people left here are those who HAVE a membership that is not usable —
                  // overwhelmingly an invitation that has not been accepted. Telling them they "are
                  // not a member of any organization" would be false and would send them to ask an
                  // administrator for access they have already been granted.
                  'Your BatonDeck organization membership is not active yet. If you were invited, accept the invitation in your email — otherwise an administrator must activate your account. Signing in again will not help.'
                : error === 'tenant_unavailable'
                  ? // T-388: the OTHER cause of a missing tenant, and the one where the advice above is
                    // exactly wrong. We could not reach WorkOS, so we do not know what this account is
                    // a member of — telling an existing customer they belong to no organization would
                    // be false, and "an administrator must add you" would send them to ask for
                    // something they already have. This is the one tenant refusal where retrying is
                    // the correct remedy, so it is the one that says so.
                    'We could not verify your organization right now. This is a temporary problem on our side, not a problem with your account — please try signing in again in a few minutes.'
                  : error === 'access_unavailable'
                    ? // T-393, the FIRST failure in the measured three-attempt sign-in. The service did
                      // not answer in time — overwhelmingly a cold start. Retrying works, so the copy
                      // says so plainly; the button below starts a fresh authorization, which is the
                      // part that matters, because the old one's code is spent either way.
                      'We could not reach the service in time — this is usually a slow first request after an idle period, not a problem with your account. Sign in again and it should work.'
                    : error === 'signin_expired'
                      ? // T-393, the SECOND failure: they refreshed the callback url, so the one-time
                        // code had already been used. Naming the CAUSE matters here — the old response
                        // was a bare 401 that read like a broken account, and the user had done nothing
                        // wrong except follow the previous screen's advice.
                        'That sign-in link had already been used — they only work once. This is normal after a failed attempt; sign in again to get a fresh one.'
                      : `Sign-in failed: ${error}`}
          </p>
        )}

        {accessMsg && (
          <p className={accessMsg.error ? 'error' : 'muted'} style={{ fontSize: 14, margin: '0 0 16px' }}>
            {accessMsg.text}
          </p>
        )}

        {/* No auto-redirect (error, access message, or demo enabled) -> one manual button.
            There is deliberately no provider choice here either: WorkOS is the only front door. */}
        <button
          className="btn"
          style={{ width: '100%', padding: '12px 16px', fontSize: 14 }}
          onClick={() => {
            window.location.href = '/auth/workos';
          }}
        >
          Sign in
        </button>

        {demoLogin && (
          <button
            className="btn secondary"
            style={{
              width: '100%',
              marginTop: 12,
              padding: '12px 16px',
              fontSize: 14,
            }}
            onClick={() => {
              window.location.href = '/auth/demo';
            }}
          >
            View the live demo →
          </button>
        )}

        <div
          style={{
            width: '100%',
            marginTop: 28,
            paddingTop: 20,
            borderTop: '1px solid var(--border)',
          }}
        >
          <p
            className="mono"
            style={{
              color: 'var(--text-muted)',
              fontSize: 11,
              lineHeight: '16px',
              margin: 0,
            }}
          >
            AI agents authenticate via service accounts — no login required.
          </p>
          <a
            href="/"
            style={{ marginTop: 14, fontSize: 12, color: 'var(--accent-tint)', display: 'inline-block' }}
          >
            What is BatonDeck? →
          </a>
        </div>
      </div>
    </div>
  );
}
