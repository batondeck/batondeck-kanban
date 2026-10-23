// ADR-0005: five states. `Status` is `(typeof STATUSES)[number]`, so the list IS the union; the
// retired names are kept out of web/src by `make web-states-check`, not by this test.
import { describe, expect, it } from 'vitest';
import { STATUSES } from '../types.js';

describe('the Status union', () => {
  it('has exactly the five states, in board order', () => {
    expect(STATUSES).toHaveLength(5);
    expect([...STATUSES]).toEqual(['TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE']);
  });
});
