import { useState, type FormEvent } from 'react';

import { useAuth } from '../auth.js';
import { BrandLockup } from '../brand';

export function SignIn() {
  const { login } = useAuth();
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // No navigation on success: App renders this card at the URL the visitor asked for, and the same
  // URL renders its route once the session loads.
  const submit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await login(name.trim(), password);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setPassword('');
    } finally {
      setBusy(false);
    }
  };
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

        <form onSubmit={(e) => void submit(e)} style={{ width: '100%', display: 'flex', flexDirection: 'column', gap: 12, textAlign: 'left' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12 }}>
            Login
            <input
              className="input"
              name="login"
              autoComplete="username"
              autoFocus
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12 }}>
            Password
            <input
              className="input"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          {error && (
            <p role="alert" className="error" style={{ fontSize: 12, margin: 0 }}>
              {error}
            </p>
          )}
          <button
            type="submit"
            className="btn"
            disabled={busy}
            style={{ width: '100%', padding: '12px 16px', fontSize: 14 }}
          >
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
        </form>

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
