// Owner: R2.7 fills these bodies; R1.12 copied the signatures from api.ts, which R1.14 splits onto them.
import type { ChronicleBlockInput, ChronicleConflicts, ChroniclePage, ChroniclePageKind, ChronicleVersion, ChronicleVersionMeta } from '../types.js';
import { req, P } from './http.js';

// chronicle (CHS-4) — the versioned, editable decision record
/**
 * PAGED. `nextCursor` present means MORE RECORDS EXIST; absent means the list is complete.
 *
 * The distinction is the whole point: Chronicle's pitch is checkable evidence, so an index that
 * silently stops at one page is a FALSE ABSENCE claim — a reader concludes the decision was never
 * recorded. The caller must surface "partial" rather than treat a page as the whole.
 */
export const listChroniclePages = (projectId: string, cursor?: string) =>
  req<{ pages: ChroniclePage[]; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/chronicle${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
  );

export const getChroniclePage = (projectId: string, pageId: string) =>
  req<{
    page: ChroniclePage | null;
    head: ChronicleVersion | null;
    drifted: string[];
    /** The persisted sweep-conflict state with THEIRS attached (T-298); null when none stands. */
    conflicts?: ChronicleConflicts | null;
  }>(
    'GET',
    `${P(projectId)}/chronicle/${pageId}`,
  );

/**
 * PAGED like the index, for the same reason (see `listChroniclePages`): a history that silently
 * ends at 50 claims "there is no earlier version" — false, and believed. `nextCursor` present
 * means OLDER VERSIONS EXIST; absent means the history is complete.
 */
export const listChronicleVersions = (projectId: string, pageId: string, cursor?: string) =>
  req<{ versions: ChronicleVersionMeta[]; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/chronicle/${pageId}/versions${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
  );

export const getChronicleVersion = (projectId: string, pageId: string, versionId: string) =>
  req<{ version: ChronicleVersion | null }>(
    'GET',
    `${P(projectId)}/chronicle/${pageId}/versions/${versionId}`,
  );

export const editChroniclePage = (
  projectId: string,
  pageId: string,
  expectedParentVersionId: string,
  blocks: ChronicleBlockInput[],
) =>
  req<{ versionId: string; drifted: string[] }>('POST', `${P(projectId)}/chronicle/${pageId}/edit`, {
    expectedParentVersionId,
    blocks,
  });

export const revertChroniclePage = (projectId: string, pageId: string, versionId: string) =>
  req<{ versionId: string }>('POST', `${P(projectId)}/chronicle/${pageId}/revert`, { versionId });

/**
 * Start a record by hand. `origin` is deliberately absent from the block shape here — the server
 * discards it anyway (only a sweep may assert that text was derived), so offering the field would
 * be inviting a caller to believe something it does not do.
 */
export const createChroniclePage = (
  projectId: string,
  input: {
    kind: ChroniclePageKind;
    slug: string;
    title: string;
    blocks: Array<{ id: string; text: string }>;
    /** Minted ONCE per form instance, so a double-submit or retried POST replays the create
     *  instead of bouncing off the taken-slug refusal. */
    idempotencyKey?: string;
  },
) => req<{ pageId: string; versionId: string }>('POST', `${P(projectId)}/chronicle`, input);

/**
 * IRREVERSIBLE — the page, every version and every stored body. Not a new version like everything
 * else. `expectedHeadVersionId` pins the head the confirmation displayed: a version landed
 * mid-confirm turns the destroy into a STALE instead of taking work the confirmer never saw.
 */
export const deleteChroniclePage = (projectId: string, pageId: string, expectedHeadVersionId?: string) =>
  req<{ ok: true }>(
    'DELETE',
    `${P(projectId)}/chronicle/${pageId}${
      expectedHeadVersionId ? `?expectedHeadVersionId=${encodeURIComponent(expectedHeadVersionId)}` : ''
    }`,
  );
