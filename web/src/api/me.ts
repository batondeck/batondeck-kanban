// Owner: R2.5 fills these bodies; R1.12 copied the signatures from api.ts, which R1.14 splits onto them.
import type { MySettings } from '../types.js';
import type { CliTokenRow } from '../api.js';
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
