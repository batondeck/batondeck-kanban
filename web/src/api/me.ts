// Owner: R2.5 fills these bodies; R1.12 copied the signatures from api.ts, which R1.14 splits onto them.
import type { MySettings } from '../types.js';
import { req } from './http.js';

/** T-201: the signed-in user's OWN preferences. No identity parameter exists — by design. */
export const getMySettings = () => req<MySettings>('GET', '/api/me/settings');

export const setMySettings = (patch: { maxTicketTokens?: number | null; tokenPolicy?: string | null; starredAssets?: string[] }) =>
  req<MySettings>('PATCH', '/api/me/settings', patch);
