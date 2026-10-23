// R1.14: api.ts is gone; every client function lives in exactly one web/src/api/<area>.ts, so a lane
// that owns an area edits that file and nothing shared.
import { describe, expect, it } from 'vitest';

const areas = import.meta.glob<Record<string, unknown>>('../api/*.ts', { eager: true });
const legacy = import.meta.glob('../api.ts');

describe('web/src/api/<area>.ts', () => {
  it('has no api.ts beside the area files', () => {
    expect(Object.keys(legacy)).toEqual([]);
  });

  it('exports each name from one area only', () => {
    const owner = new Map<string, string>();
    for (const [file, mod] of Object.entries(areas)) {
      for (const name of Object.keys(mod)) {
        expect(owner.get(name), `${name} in ${file}`).toBeUndefined();
        owner.set(name, file);
      }
    }
    expect(owner.get('req')).toBe('../api/http.ts');
    expect(owner.get('moveTask')).toBe('../api/tasks.ts');
    expect(owner.get('joinStream')).toBe('../api/stream.ts');
  });
});
