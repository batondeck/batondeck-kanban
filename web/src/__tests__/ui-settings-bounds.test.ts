/**
 * The board page size stays within what `list_tasks` accepts.
 *
 * `taskPageLimit` is the `limit` the drawer, the Activity log and the Agents screen send in ONE
 * request, and the server refuses a `limit` over 200 (internal/service/tasks.go listMaxLimit) with
 * 400 VALIDATION. An override above it could only make each of those reads fail.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { hydrateUiSettings, uiSettings } from '../settings.js';

afterEach(() => {
  hydrateUiSettings(undefined);
});

describe('taskPageLimit', () => {
  it('is held to 1..200, the range list_tasks accepts', () => {
    expect(hydrateUiSettings({ taskPageLimit: 500 }).taskPageLimit).toBe(200);
    expect(uiSettings().taskPageLimit).toBe(200);
    expect(hydrateUiSettings({ taskPageLimit: 0 }).taskPageLimit).toBe(1);
  });

  it('keeps a value inside the range, and the default without one', () => {
    expect(hydrateUiSettings({ taskPageLimit: 20 }).taskPageLimit).toBe(20);
    expect(hydrateUiSettings(undefined).taskPageLimit).toBe(200);
  });
});
