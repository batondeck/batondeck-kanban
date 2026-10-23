// Owner: R2.5 fills these bodies; R1.12 copied the signatures from api.ts.
import type { AgentSession } from '../types.js';
import { req } from './http.js';

// all=true (workspace-wide, core-enforced admin) lets admin viewers resolve client logos for every agent.
export const agentSessions = (limit?: number, all?: boolean) =>
  req<{ sessions: AgentSession[] }>(
    'GET',
    `/api/agents/sessions?${new URLSearchParams({ ...(limit ? { limit: String(limit) } : {}), ...(all ? { all: 'true' } : {}) }).toString()}`,
  );

/**
 * Disconnect one of YOUR OWN sessions. Distinct endpoint from `adminDisconnectAgent`, and the core
 * enforces owner-or-admin on it — so a member can end their own agent without the admin console.
 *
 * The gateway route has existed since the sessions work; the SPA only ever wired the ADMIN one, which
 * is why "Manage sessions" pointed at /admin and non-admins had no way to stop a session at all.
 */
export const disconnectOwnSession = (identityId: string, agentId: string) =>
  req<{ disconnected: boolean; wakeRevoked?: boolean }>('POST', '/api/agents/sessions/disconnect', { identityId, agentId });
