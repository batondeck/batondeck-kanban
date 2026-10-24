// The agent-session area (internal/api/agents.go): list_agent_sessions and disconnect_agent.
import type { AgentSession } from '../types.js';
import { req } from './http.js';

// all=true (workspace-wide, core-enforced admin) lets admin viewers resolve client logos for every agent.
export const agentSessions = (limit?: number, all?: boolean) =>
  req<{ sessions: AgentSession[] }>(
    'GET',
    `/api/agents/sessions?${new URLSearchParams({ ...(limit ? { limit: String(limit) } : {}), ...(all ? { all: 'true' } : {}) }).toString()}`,
  );

/**
 * End an agent session's live MCP connections: your own, or anyone's as the workspace admin (the
 * service enforces it). Its next request is refused and it initialises again.
 */
export const disconnectOwnSession = (identityId: string, agentId: string) =>
  req<{ disconnected: boolean }>('POST', '/api/agents/sessions/disconnect', { identityId, agentId });
