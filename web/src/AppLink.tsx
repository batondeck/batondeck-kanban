/**
 * Smart external-artifact links (Jira-style "integrations"): recognize the app behind a pasted URL
 * and render a branded link card. The icon is the site's favicon (via Google's favicon proxy — no
 * per-app SVG maintenance, and it covers the long tail of apps automatically); the label is a
 * curated pretty-name for known apps, falling back to the hostname.
 */
import { useState } from 'react';
import { Icon } from './icons.js';
import { uiSettings } from './settings.js';

export interface LinkApp {
  /** Pretty app name ("Lucidchart", "Google Docs") or the bare hostname for unknown apps. */
  name: string;
  /** Hostname (www. stripped) — used for the favicon lookup. */
  host: string;
  url: string;
  /** Spoof-safe short form (host + path) for display — never echoes raw userinfo/query tricks. */
  display: string;
}

const KNOWN: Array<{ re: RegExp; name: string }> = [
  { re: /(^|\.)lucid(chart|spark)?\.(app|com)$/, name: 'Lucidchart' },
  { re: /(^|\.)figma\.com$/, name: 'Figma' },
  { re: /(^|\.)(notion\.so|notion\.site)$/, name: 'Notion' },
  { re: /(^|\.)miro\.com$/, name: 'Miro' },
  // Forges. Matched on the host FAMILY, not one domain, so self-hosted installs (gitlab.acme.dev)
  // get the same treatment — artifacts arrive from whatever forge the work lives on.
  { re: /(^|\.)(github\.com)$/, name: 'GitHub' },
  { re: /(^|\.)gitlab\.[a-z.]+$/, name: 'GitLab' },
  { re: /(^|\.)bitbucket\.[a-z.]+$/, name: 'Bitbucket' },
  { re: /(^|\.)(sr\.ht|codeberg\.org|gitea\.[a-z.]+)$/, name: 'Git forge' },
  { re: /(^|\.)slack\.com$/, name: 'Slack' },
  { re: /(^|\.)loom\.com$/, name: 'Loom' },
  { re: /(^|\.)(youtube\.com|youtu\.be)$/, name: 'YouTube' },
  { re: /(^|\.)drive\.google\.com$/, name: 'Google Drive' },
  { re: /(^|\.)linear\.app$/, name: 'Linear' },
  { re: /(^|\.)airtable\.com$/, name: 'Airtable' },
  { re: /(^|\.)canva\.com$/, name: 'Canva' },
  { re: /(^|\.)dropbox\.com$/, name: 'Dropbox' },
  { re: /(^|\.)whimsical\.com$/, name: 'Whimsical' },
  { re: /(^|\.)excalidraw\.com$/, name: 'Excalidraw' },
  { re: /(^|\.)(draw\.io|diagrams\.net)$/, name: 'draw.io' },
  { re: /(^|\.)sharepoint\.com$/, name: 'SharePoint' },
];

/** Parse a URL into a recognized app link, or null when it isn't a valid http(s) URL. */
export function appForUrl(raw: string): LinkApp | null {
  let u: URL;
  try {
    u = new URL(raw.trim());
  } catch {
    return null;
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null;
  // Reject userinfo URLs outright: `https://docs.google.com@evil.com/x` would render a card that LOOKS
  // like docs.google while linking to evil.com. Real artifact links never carry credentials.
  if (u.username || u.password) return null;
  const host = u.hostname.replace(/^www\./, '').toLowerCase();
  const display = host + (u.pathname === '/' ? '' : u.pathname);
  const app = (name: string): LinkApp => ({ name, host, url: u.href, display });
  // Google editors share a host; the product is in the path.
  if (host === 'docs.google.com') {
    return app(
      u.pathname.startsWith('/spreadsheets')
        ? 'Google Sheets'
        : u.pathname.startsWith('/presentation')
          ? 'Google Slides'
          : u.pathname.startsWith('/forms')
            ? 'Google Forms'
            : 'Google Docs',
    );
  }
  // Atlassian cloud: /wiki is Confluence, anything else is Jira.
  if (host.endsWith('.atlassian.net')) return app(u.pathname.startsWith('/wiki') ? 'Confluence' : 'Jira');
  const hit = KNOWN.find((k) => k.re.test(host));
  return app(hit?.name ?? host);
}

/** Coerce free input into an attachable URL: as-typed, else with https:// prefixed. Null when neither works. */
export function normalizeLink(raw: string): string | null {
  const t = raw.trim();
  if (!t) return null;
  if (appForUrl(t)) return t;
  // `lucid.app/x` → `https://lucid.app/x` — require a dot so stray words don't become links.
  if (!/^[a-z][a-z0-9+.-]*:/i.test(t) && /^[^\s/]+\.[^\s/]{2,}(\/|$)/.test(t) && appForUrl(`https://${t}`)) return `https://${t}`;
  return null;
}

function FavIcon({ host }: { host: string }) {
  const [failed, setFailed] = useState(false);
  // linkFavicons off (admin privacy setting) → no external request, generic icon.
  if (failed || !uiSettings().linkFavicons) return <Icon name="link" size={14} />;
  return (
    <img
      src={`https://www.google.com/s2/favicons?domain=${encodeURIComponent(host)}&sz=32`}
      alt=""
      width={16}
      height={16}
      style={{ borderRadius: 'var(--r-1)', flex: '0 0 auto' }}
      onError={() => setFailed(true)}
    />
  );
}

/** One attached artifact link: favicon + app name + truncated target, opens in a new tab. A body that
 * isn't a bare URL (older/agent-written data) still renders — as plain text, never silently dropped. */
export function AppLinkCard({ url, meta }: { url: string; meta?: string }) {
  const app = appForUrl(url);
  if (!app) {
    return (
      <div className="card" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px' }}>
        <Icon name="link" size={14} />
        <span style={{ minWidth: 0, fontSize: 14, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{url}</span>
        {meta && <span className="muted" style={{ marginLeft: 'auto', fontSize: 11, flex: '0 0 auto' }}>{meta}</span>}
      </div>
    );
  }
  return (
    <a
      className="card"
      href={app.url}
      target="_blank"
      rel="noreferrer"
      style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px', textDecoration: 'none', color: 'inherit' }}
    >
      <FavIcon host={app.host} />
      <span style={{ minWidth: 0, display: 'flex', flexDirection: 'column' }}>
        <span style={{ fontSize: 14, fontWeight: 600 }}>
          {app.name}
          {meta && <span className="muted" style={{ fontWeight: 400, fontSize: 11, marginLeft: 8 }}>{meta}</span>}
        </span>
        <span className="mono muted" style={{ fontSize: 11, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{app.display}</span>
      </span>
      <span aria-hidden className="muted" style={{ marginLeft: 'auto', fontSize: 12 }}>↗</span>
    </a>
  );
}
