/**
 * "Connect MCP" modal: one setup snippet per client, all pointing at THIS server's /mcp with an API
 * token in the Authorization header. The OAuth connector path shows only when public-config says
 * the server runs its OAuth server (public_url set). Each snippet's shape is the client's own:
 * `claude mcp add --help` (2.1.308), `codex mcp add --help` (0.145.0), `gemini mcp add --help`
 * (0.65.0), and cursor-agent 2026.04.17, which resolves `${NAME}` in `headers` (not the docs' `${env:NAME}`).
 */
import { useState } from 'react';
import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import * as sessionApi from './api/session.js';
import { Icon } from './icons.js';

type ClientTab = 'Claude Code' | 'Codex CLI' | 'Gemini CLI' | 'Cursor';
const TABS: ClientTab[] = ['Claude Code', 'Codex CLI', 'Gemini CLI', 'Cursor'];

type Snippet = { blocks: Array<{ label: string; text: string }>; note: string };

export function snippets(mcpUrl: string): Record<ClientTab, Snippet> {
  return {
    'Claude Code': {
      blocks: [
        {
          label: 'MCP server',
          text: `claude mcp add --scope user --transport http batondeck-kanban ${mcpUrl} \\\n  --header "Authorization: Bearer $BATONDECK_TOKEN"`,
        },
        {
          label: 'Or the plugin (MCP server, worker skill and the device wake)',
          text: 'claude --plugin-dir /path/to/batondeck-kanban/plugin',
        },
      ],
      note: "The plugin's .mcp.json reads http://localhost:8080/mcp and its wake needs a unix:// listen entry: plugin/README.md in your checkout has the setup.",
    },
    'Codex CLI': {
      blocks: [{ label: 'Terminal', text: `codex mcp add batondeck-kanban --url ${mcpUrl} \\\n  --bearer-token-env-var BATONDECK_TOKEN` }],
      note: 'Writes [mcp_servers.batondeck-kanban] to ~/.codex/config.toml; Codex reads BATONDECK_TOKEN at each launch.',
    },
    'Gemini CLI': {
      blocks: [
        {
          label: 'Terminal',
          text: `gemini mcp add --scope user --transport http batondeck-kanban ${mcpUrl} \\\n  -H "Authorization: Bearer $BATONDECK_TOKEN"`,
        },
      ],
      note: 'Writes ~/.gemini/settings.json; your shell fills in the token when you run it.',
    },
    Cursor: {
      blocks: [
        {
          label: '~/.cursor/mcp.json (or .cursor/mcp.json in a project)',
          text: `{\n  "mcpServers": {\n    "batondeck-kanban": {\n      "url": "${mcpUrl}",\n      "headers": { "Authorization": "Bearer \${BATONDECK_TOKEN}" }\n    }\n  }\n}`,
        },
      ],
      note: 'Merge into the file, then turn the server on in Cursor Settings → MCP. Checked with cursor-agent 2026.04.17; the Cursor IDE is unverified.',
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
  const mcpUrl = `${window.location.origin}/mcp`;
  const cur = snippets(mcpUrl)[tab];
  const cfg = useQuery({ queryKey: ['publicConfig'], queryFn: () => sessionApi.publicConfig(), staleTime: 60000 });

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
          This server's MCP endpoint is <span className="mono">{mcpUrl}</span>. Agents send an API token as{' '}
          <span className="mono">Authorization: Bearer $BATONDECK_TOKEN</span>:{' '}
          <Link to="/settings#cli-tokens" onClick={onClose} style={{ color: 'var(--accent-tint)' }}>create a token</Link>{' '}
          and export it as <span className="mono">BATONDECK_TOKEN</span> before running the command below. Claude Code and
          Gemini save the token into their config when you run it; Codex and Cursor read the variable each launch.
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

        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {cur.blocks.map((b) => (
            <CopyBlock key={b.label} label={b.label} text={b.text} />
          ))}
          <span className="muted" style={{ fontSize: 12 }}>{cur.note}</span>
        </div>

        <p className="muted" style={{ fontSize: 12, margin: '16px 0 0', paddingTop: 12, borderTop: '1px solid var(--border)' }}>
          Only Claude Code with the plugin gets the device wake (the server starts a turn in an idle session when there is
          work). Codex, Gemini, Cursor and Claude Code without the plugin pull the board each turn.
        </p>
        {cfg.data?.oauth && (
          <p className="muted" style={{ fontSize: 12, margin: '8px 0 0' }}>
            OAuth is on: a connector client (claude.ai, Claude Desktop, ChatGPT) can add{' '}
            <span className="mono">{mcpUrl}</span> as a custom connector and sign in in the browser instead of using a token.
          </p>
        )}
      </div>
    </div>
  );
}
