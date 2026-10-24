// The on-call routes of internal/api/oncall.go (R2.10): the source catalog, integrations, and each one's delivery log.
import type { CreateIntegrationBody, Delivery, Integration, IntegrationPatchBody, OncallSource } from '../types.js';
import { req, ON } from './http.js';

// --- on-call integrations (spec §9/§10) ---
/** The source catalog, SERVED rather than mirrored — see the route's docblock in the gateway. */
export const oncallSources = () => req<{ sources: OncallSource[] }>('GET', '/api/oncall/sources');

export const listIntegrations = (projectId: string, includeDeleted?: boolean) =>
  req<{ integrations: Integration[] }>(
    'GET',
    `${ON(projectId)}/integrations${includeDeleted ? '?includeDeleted=true' : ''}`,
  );

/**
 * Tombstone an integration. The endpoint stops answering; the row stays so an old incident's
 * delivery log is still readable. `version` rides the query string because a DELETE has no body —
 * it is still enforced (STALE on mismatch).
 */
export const deleteIntegration = (projectId: string, integrationId: string, version: number) =>
  req<{ integration: Integration; deleted: true }>(
    'DELETE',
    `${ON(projectId)}/integrations/${integrationId}?version=${version}`,
  );

export const createIntegration = (projectId: string, body: CreateIntegrationBody) =>
  req<{ integration: Integration; secret?: string; shownOnce?: true }>(
    'POST',
    `${ON(projectId)}/integrations`,
    body,
  );

export const updateIntegration = (projectId: string, integrationId: string, version: number, patch: IntegrationPatchBody) =>
  req<{ integration: Integration }>('POST', `${ON(projectId)}/integrations/${integrationId}`, {
    version,
    patch,
  });

/**
 * `externalSecret` is REQUIRED for a source that issues its own (PagerDuty) — the core refuses the
 * rotation otherwise, because a minted secret is one the source will never send.
 */
export const rotateIntegrationSecret = (
  projectId: string,
  integrationId: string,
  version: number,
  opts?: { revokePrevious?: boolean; externalSecret?: string },
) =>
  req<{ integration: Integration; secret?: string; shownOnce?: true }>(
    'POST',
    `${ON(projectId)}/integrations/${integrationId}/rotate-secret`,
    {
      version,
      ...(opts?.revokePrevious ? { revokePrevious: true } : {}),
      ...(opts?.externalSecret ? { externalSecret: opts.externalSecret } : {}),
    },
  );

export const sendTestEvent = (projectId: string, integrationId: string, title?: string) =>
  req<{ result: string; taskId: string | null; deliveryId: string; doorbell: string }>(
    'POST',
    `${ON(projectId)}/integrations/${integrationId}/test`,
    title ? { title } : {},
  );

/** `nextCursor` present means MORE ROWS EXIST; absent means the list is complete. */
export const listDeliveries = (projectId: string, integrationId: string, cursor?: string, limit = 25) =>
  req<{ deliveries: Delivery[]; nextCursor?: string }>(
    'GET',
    `${ON(projectId)}/integrations/${integrationId}/deliveries?limit=${limit}` +
      (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''),
  );

export const replayDelivery = (projectId: string, integrationId: string, deliveryId: string) =>
  req<{ result: string; taskId?: string | null; newDeliveryId: string }>(
    'POST',
    `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/replay`,
  );

/** The raw payload of a REFUSED delivery. A real navigation, never fetch-and-render: the response
 * is an attachment of unauthenticated attacker-controlled bytes (§8.3). Retained for the download
 * route, which still serves them as octet-stream + attachment + nosniff. */
export const rawDeliveryUrl = (projectId: string, integrationId: string, deliveryId: string) =>
  `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/raw`;

/**
 * The same bytes, as TEXT, for the in-app viewer.
 *
 * Decoded here rather than rendered by the browser: the caller puts it in a React text node, which
 * escapes it, so hostile bytes cannot become markup. Rejects when the payload has aged out — the
 * log entry survives 30 days and the body only 7, and the viewer explains that rather than showing
 * an empty box.
 */
export const readDeliveryRaw = async (projectId: string, integrationId: string, deliveryId: string): Promise<{ payload: string }> => {
  const res = await fetch(
    `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/raw`,
    { credentials: 'include' },
  );
  if (!res.ok) throw new Error(`payload unavailable (${res.status})`);
  return { payload: await res.text() };
};
