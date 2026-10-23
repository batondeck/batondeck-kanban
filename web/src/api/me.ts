// Owner: R2.5 fills these bodies; R1.12 copied the signatures from api.ts.
import type { MySettings } from '../types.js';
import { req } from './http.js';

/** T-201: the signed-in user's OWN preferences. No identity parameter exists — by design. */
export const getMySettings = () => req<MySettings>('GET', '/api/me/settings');

export const setMySettings = (patch: { maxTicketTokens?: number | null; tokenPolicy?: string | null; starredAssets?: string[] }) =>
  req<MySettings>('PATCH', '/api/me/settings', patch);

// CLI tokens (T-457). There is deliberately NO `getCliToken` — the value is returned by the mint and
// nowhere else, so a compromised session cannot walk the list to harvest secrets.
export const listCliTokens = () => req<{ tokens: CliTokenRow[] }>('GET', '/api/me/cli-tokens');

export const createCliToken = (body: { label: string; ttlSeconds?: number }) =>
  req<CliTokenRow & { token: string; shownOnce: true }>('POST', '/api/me/cli-tokens', body);

export const revokeCliToken = (id: string) =>
  req<{ revoked: boolean; token: CliTokenRow | null }>(
    'POST',
    `/api/me/cli-tokens/${encodeURIComponent(id)}/revoke`,
  );

/** One CLI token as a read path returns it — no value, by construction (T-457). */
export interface CliTokenRow {
  id: string;
  label: string;
  createdAt: string;
  expiresAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
  status: 'live' | 'expired' | 'revoked';
}

// UNMOUNTED: the plan dropped /api/me/marketplace-key (routes.json); Settings still calls these three
// until P-184 removes the panel. Kept so that screen compiles.
export const getMarketplaceKey = () =>
  req<{ key: string | null; createdAt: string | null; rotated: boolean; lastUsedAt: string | null; useCount: number; recentUses: Array<{ ts: string; kind: 'catalog' | 'archive'; pluginName?: string; semver?: string; ip?: string; userAgent?: string }> }>(
    'GET',
    '/api/me/marketplace-key',
  );

export const createMarketplaceKey = () =>
  req<{ key: string; rotated: boolean }>('POST', '/api/me/marketplace-key', {});

export const rotateMarketplaceKey = () =>
  req<{ key: string; rotated: boolean }>('POST', '/api/me/marketplace-key/rotate', {});
