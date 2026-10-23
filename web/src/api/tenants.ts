// UNMOUNTED: the tenants group is out of scope (routes.json) and these four answer the mux's 404.
// Settings' teammate panel still calls them until R1.15's users page replaces it (P-60); kept so
// that screen compiles, not because a route serves them.
import { req } from './http.js';

/**
 * Invite a teammate into the CURRENT organization (T-389). The organization is NOT a parameter — the
 * server takes it from the session and re-validates it, so this cannot be pointed at someone else's
 * tenant. A 403 here is authoritative: the SPA's own check is a courtesy, not a control.
 */
export const inviteTeammate = (email: string) => req<{ ok: true }>('POST', '/api/tenants/invitations', { email });

/**
 * The CURRENT organization's outstanding invitations (T-396). The organization is NOT a parameter —
 * it comes from the session — so this cannot be pointed at another tenant's invitees. Only `pending`
 * rows come back, which is how the UI answers "did they accept?" without a second endpoint.
 */
export const listInvitations = () =>
  req<{ invitations: { id: string; email: string; state: 'pending'; createdAt: string; expiresAt: string }[] }>(
    'GET',
    '/api/tenants/invitations',
  );

/** Resend / revoke one invitation. 404 means it is not a pending invitation of THIS organization. */
export const resendInvitation = (id: string) =>
  req<{ ok: true }>('POST', `/api/tenants/invitations/${encodeURIComponent(id)}/resend`);

export const revokeInvitation = (id: string) =>
  req<{ ok: true }>('POST', `/api/tenants/invitations/${encodeURIComponent(id)}/revoke`);
