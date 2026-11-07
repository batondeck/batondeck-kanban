// A minimal MCP client over the server's streamable HTTP endpoint (/mcp), for changes made OUTSIDE the
// browser: seeding a spec's project and board, and the writes the live-update specs expect the open
// page to show. It speaks the protocol the way any client does (initialize, then tools/call on the
// session the server names), with an account's API token.
import { runState, type Role } from './state.js';

const PROTOCOL = '2025-06-18';

interface RpcResponse {
  result?: { structuredContent?: unknown; content?: Array<{ type: string; text?: string }>; isError?: boolean };
  error?: { code: number; message: string };
}

/** The JSON-RPC message in a response body, which is either plain JSON or one SSE `data:` frame. */
function parseBody(contentType: string, text: string): RpcResponse {
  if (contentType.includes('text/event-stream')) {
    const data = text
      .split('\n')
      .filter((l) => l.startsWith('data:'))
      .map((l) => l.slice(5).trim())
      .filter(Boolean);
    if (data.length === 0) throw new Error(`MCP: an event stream with no data frame: ${text.slice(0, 200)}`);
    return JSON.parse(data[data.length - 1]!) as RpcResponse;
  }
  return JSON.parse(text) as RpcResponse;
}

export class McpClient {
  private session: string | null = null;
  private id = 0;

  constructor(private readonly role: Role) {}

  private async post(body: unknown): Promise<Response> {
    const { baseURL, accounts } = runState();
    const headers: Record<string, string> = {
      authorization: `Bearer ${accounts[this.role].token}`,
      'content-type': 'application/json',
      accept: 'application/json, text/event-stream',
      'mcp-protocol-version': PROTOCOL,
    };
    if (this.session) headers['mcp-session-id'] = this.session;
    return fetch(`${baseURL}/mcp`, { method: 'POST', headers, body: JSON.stringify(body) });
  }

  private async rpc(method: string, params: unknown): Promise<RpcResponse> {
    const res = await this.post({ jsonrpc: '2.0', id: ++this.id, method, params });
    const text = await res.text();
    if (!res.ok) throw new Error(`MCP ${method}: HTTP ${res.status}: ${text.slice(0, 300)}`);
    const sid = res.headers.get('mcp-session-id');
    if (sid) this.session = sid;
    const msg = parseBody(res.headers.get('content-type') ?? '', text);
    if (msg.error) throw new Error(`MCP ${method}: ${msg.error.code} ${msg.error.message}`);
    return msg;
  }

  private async init(): Promise<void> {
    if (this.session) return;
    await this.rpc('initialize', {
      protocolVersion: PROTOCOL,
      capabilities: {},
      clientInfo: { name: `e2e-${this.role}`, version: '1' },
    });
    const res = await this.post({ jsonrpc: '2.0', method: 'notifications/initialized' });
    if (!res.ok) throw new Error(`MCP notifications/initialized: HTTP ${res.status}`);
  }

  /** Calls one tool and returns its structured result; a tool error throws with the tool's text. */
  async call<T = Record<string, unknown>>(name: string, args: Record<string, unknown>): Promise<T> {
    await this.init();
    const msg = await this.rpc('tools/call', { name, arguments: args });
    const r = msg.result;
    if (!r) throw new Error(`MCP ${name}: no result`);
    if (r.isError) throw new Error(`MCP ${name} refused: ${r.content?.map((c) => c.text).join(' ')}`);
    if (r.structuredContent !== undefined) return r.structuredContent as T;
    return JSON.parse(r.content?.[0]?.text ?? '{}') as T;
  }
}

export interface Column {
  id: string;
  name: string;
  status: string;
}

export interface SeededBoard {
  projectId: string;
  projectName: string;
  boardId: string;
  boardName: string;
  columns: Column[];
}

/**
 * A project with one board, made by the admin over MCP, with the viewer and the worker added as
 * members when `members` says so. Each spec seeds its own, named uniquely, so specs never share rows.
 */
export async function seedProject(
  name: string,
  opts: { members?: Partial<Record<'viewer' | 'worker', string>> } = {},
): Promise<SeededBoard> {
  const admin = new McpClient('admin');
  const projectName = `${name} ${Date.now().toString(36)}`;
  const { projectId } = await admin.call<{ projectId: string }>('create_project', { name: projectName });
  const boardName = 'Main board';
  const { board } = await admin.call<{ board: { id: string; columns: Column[] } }>('create_board', {
    projectId,
    name: boardName,
  });
  const { accounts } = runState();
  for (const [who, role] of Object.entries(opts.members ?? {})) {
    await admin.call('add_member', { projectId, identityId: accounts[who as 'viewer' | 'worker'].login, role });
  }
  return { projectId, projectName, boardId: board.id, boardName, columns: board.columns };
}

export interface SeededTask {
  id: string;
  version: number;
  title: string;
}

export async function seedTask(
  b: SeededBoard,
  title: string,
  extra: Record<string, unknown> = {},
): Promise<SeededTask> {
  const { task } = await new McpClient('admin').call<{ task: SeededTask }>('create_task', {
    projectId: b.projectId,
    boardId: b.boardId,
    title,
    ...extra,
  });
  return task;
}
