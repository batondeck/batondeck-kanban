// @vitest-environment node
/**
 * EVERY EVENT THE SERVER EMITS HAS A VERB, AND NO VERB NAMES AN EVENT IT DOES NOT (P-457).
 *
 * The activity surfaces print `eventVerb(type)`, which falls back to the raw dotted identifier. The
 * table had drifted both ways: it still listed the hosted product's plugin and org events, and it had
 * no entry for a quarter of this server's own (a completed task showed "task.review_requested"). The
 * server's list is `internal/domain/events.go`; this reads it, as tool-floors.test.ts reads grants.go.
 */
import { describe, it, expect } from 'vitest';
import { EVENT_VERB_TYPES, eventVerb } from '../components.js';

// node's fs through a computed specifier, as tool-floors.test.ts reads grants.go: events.go sits outside
// the web root, and the web's tsconfig carries no node types.
type Fs = { readFileSync(path: URL, encoding: 'utf8'): string };
const nodeFs = 'node:fs';
const fs = (await import(/* @vite-ignore */ nodeFs)) as Fs;
const source = fs.readFileSync(new URL('../../../internal/domain/events.go', import.meta.url), 'utf8');
const serverTypes = [...source.matchAll(/EventType = "([a-z_.]+)"/g)].map((m) => m[1]!);

describe('event verbs', () => {
  it('the parse finds the server\'s event types', () => {
    expect(serverTypes.length).toBeGreaterThan(40);
    expect(serverTypes).toContain('task.review_requested');
  });

  it.each(serverTypes)('%s has a verb that is not its own identifier', (type) => {
    expect(eventVerb(type)).not.toBe(type);
    expect(eventVerb(type)).not.toMatch(/[._]/);
  });

  it('no verb names an event the server does not emit', () => {
    expect(EVENT_VERB_TYPES.filter((t) => !serverTypes.includes(t))).toEqual([]);
  });

  it('an unknown type still falls back to itself rather than to nothing', () => {
    expect(eventVerb('future.event')).toBe('future.event');
  });
});
