/**
 * "Connect MCP" modal — per-client install instructions for the BatonDeck MCP server, generated from
 * the deployed URLs. The optional agent name injects an `X-BatonDeck-Agent` header (purely cosmetic —
 * it names the session on the Agents page; everything works without it).
 */
import { useMemo, useState } from 'react';
import { Icon } from './icons.js';
import { pushToast } from './toast.js';

const MCP_URL = 'https://mcp.batondeck.com/mcp';
const DOCS_URL = '/docs';

type ClientTab = 'Claude Code' | 'Cursor' | 'Claude Desktop' | 'Codex' | 'Other';
const TABS: ClientTab[] = ['Claude Code', 'Cursor', 'Claude Desktop', 'Codex', 'Other'];

function snippets(
  agent: string,
): Record<ClientTab, { lang: string; text: string; note?: string; blocks?: Array<{ label: string; text: string }> }> {
  const name = agent.trim();
  const headerArgs = name ? `,\n        "--header",\n        "X-BatonDeck-Agent:${name}"` : '';
  const httpHeaders = name ? `,\n      "headers": { "X-BatonDeck-Agent": "${name}" }` : '';
  const claudeHeader = name ? ` --header "X-BatonDeck-Agent: ${name}"` : '';
  return {
    'Claude Code': {
      lang: 'bash',
      // `text` stays the terminal plugin form so the shared Copy path keeps working if a future
      // edit renders this tab through the single-block layout; the BLOCKS are what actually render.
      text: `claude plugin marketplace add tech-sumit/batondeck-plugin\nclaude plugin install batondeck@batondeck-marketplace`,
      blocks: [
        {
          label: 'Plugin — terminal (worker skill, commands, scripts, AND the MCP server)',
          text: `claude plugin marketplace add tech-sumit/batondeck-plugin\nclaude plugin install batondeck@batondeck-marketplace`,
        },
        {
          label: 'Plugin — inside a running session',
          text: `/plugin marketplace add tech-sumit/batondeck-plugin\n/plugin install batondeck@batondeck-marketplace`,
        },
        {
          label: 'MCP server only',
          text: `claude mcp add --transport http batondeck ${MCP_URL}${claudeHeader}`,
        },
      ],
      note: 'First use opens a browser for sign-in (OAuth — no tokens to paste).',
    },
    Cursor: {
      lang: 'json',
      text: `{\n  "mcpServers": {\n    "batondeck": {\n      "url": "${MCP_URL}"${httpHeaders}\n    }\n  }\n}`,
      note: 'Merge into ~/.cursor/mcp.json, then toggle the server on in Settings → MCP.',
    },
    'Claude Desktop': {
      lang: 'json',
      text: `{\n  "mcpServers": {\n    "batondeck": {\n      "command": "npx",\n      "args": [\n        "-y",\n        "mcp-remote",\n        "${MCP_URL}"${headerArgs}\n      ]\n    }\n  }\n}`,
      note: 'Merge into claude_desktop_config.json (Settings → Developer → Edit Config), then restart Claude Desktop. Prefer the plugin instead: + next to the prompt → Plugins → Add plugin → add marketplace tech-sumit/batondeck-plugin, install batondeck (local/SSH sessions).',
    },
    Codex: {
      lang: 'toml',
      text: `[mcp_servers.batondeck]\ncommand = "npx"\nargs = ["-y", "mcp-remote", "${MCP_URL}"${name ? `, "--header", "X-BatonDeck-Agent:${name}"` : ''}]`,
      note: 'Add to ~/.codex/config.toml.',
    },
    Other: {
      lang: 'text',
      text: MCP_URL,
      note: 'Streamable HTTP MCP with OAuth 2.1 discovery (RFC 8414/9728), dynamic client registration, and PKCE. Any spec-compliant client can connect; older clients can bridge via `npx mcp-remote <url>`.',
    },
  };
}

function CopyBlock({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>{label}</div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          background: 'var(--surface-lowest)',
          border: '1px solid var(--border)',
          borderRadius: 'var(--r-2)',
        }}
      >
        <pre
          className="mono"
          style={{ flex: 1, minWidth: 0, margin: 0, padding: '10px 12px', fontSize: 12, lineHeight: 1.5, overflowX: 'auto', whiteSpace: 'pre' }}
        >
          {text}
        </pre>
        <button
          className="btn secondary"
          style={{ fontSize: 12, padding: '4px 10px', margin: '0 8px', flexShrink: 0 }}
          onClick={() => {
            void navigator.clipboard?.writeText(text).then(() => {
              setCopied(true);
              setTimeout(() => { setCopied(false); }, 1500);
            });
          }}
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
    </div>
  );
}

export function ConnectMcp({ onClose }: { onClose: () => void }) {
  const [tab, setTab] = useState<ClientTab>('Claude Code');
  const [agent, setAgent] = useState('');
  const all = useMemo(() => snippets(agent), [agent]);
  const cur = all[tab];
  const cursorDeeplink = useMemo(() => {
    const cfg = btoa(JSON.stringify({ url: MCP_URL, ...(agent.trim() ? { headers: { 'X-BatonDeck-Agent': agent.trim() } } : {}) }));
    return `cursor://anysphere.cursor-deeplink/mcp/install?name=batondeck&config=${cfg}`;
  }, [agent]);

  const copy = (): void => {
    void navigator.clipboard.writeText(cur.text).then(() => pushToast('Copied', 'info'));
  };

  return (
    <div className="drawer-overlay" style={{ justifyContent: 'center', alignItems: 'center' }} onClick={onClose}>
      <div
        className="help-card"
        role="dialog"
        aria-label="Connect an MCP client"
        style={{ width: 640, maxWidth: '94vw' }}
        onClick={(e) => e.stopPropagation()}
      >
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
          <h2 style={{ margin: 0 }}>Connect an agent</h2>
          <button className="btn secondary icon" onClick={onClose} aria-label="Close"><Icon name="close" size={12} /></button>
        </div>
        <p className="muted" style={{ fontSize: 14, margin: '4px 0 14px' }}>
          Point any MCP client at BatonDeck. Sign-in is browser OAuth — no tokens, no gcloud.{' '}
          <a href={DOCS_URL} style={{ color: 'var(--accent-tint)' }}>Full guide →</a>
        </p>

        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
          {TABS.map((t) => (
            <button
              key={t}
              className={`btn ${t === tab ? '' : 'secondary'}`}
              style={{ fontSize: 12, padding: '5px 10px' }}
              onClick={() => setTab(t)}
            >
              {t}
            </button>
          ))}
        </div>

        {cur.blocks !== undefined ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {cur.blocks.map((b) => (
              <CopyBlock key={b.label} label={b.label} text={b.text} />
            ))}
            <span className="muted" style={{ fontSize: 12 }}>{cur.note}</span>
          </div>
        ) : (
          <>
            <pre
              className="mono"
              style={{
                background: 'var(--surface-lowest)', border: '1px solid var(--border)', borderRadius: 'var(--r-2)',
                padding: 14, fontSize: 12, lineHeight: 1.5, overflowX: 'auto', whiteSpace: 'pre', margin: 0,
              }}
            >
              {cur.text}
            </pre>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10 }}>
              <button className="btn" style={{ fontSize: 12 }} onClick={copy}>Copy</button>
              {tab === 'Cursor' && (
                <a className="btn secondary" style={{ fontSize: 12, textDecoration: 'none' }} href={cursorDeeplink}>
                  Add to Cursor →
                </a>
              )}
              <span className="muted" style={{ fontSize: 12, flex: 1 }}>{cur.note}</span>
            </div>
          </>
        )}

        <div style={{ marginTop: 16, paddingTop: 12, borderTop: '1px solid var(--border)', display: 'flex', gap: 10, alignItems: 'center' }}>
          <input
            className="input"
            style={{ width: 200 }}
            placeholder="Agent name (optional)"
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
          />
          <span className="muted" style={{ fontSize: 12 }}>
            Names this connection on the Agents page (sets <span className="mono">X-BatonDeck-Agent</span>). Optional — unnamed agents get a generated id.
          </span>
        </div>
      </div>
    </div>
  );
}
