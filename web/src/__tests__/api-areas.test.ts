// R1.12 AC3's placeholder: every web/src/api/<area>.ts function is one of api.ts's, with its type
// (`satisfies` fails tsc on a signature that drifted), so R1.14 can split api.ts onto them.
import { describe, expect, it } from 'vitest';
import { api } from '../api.js';
import * as agents from '../api/agents.js';
import * as analytics from '../api/analytics.js';
import * as chronicle from '../api/chronicle.js';
import * as comments from '../api/comments.js';
import * as followups from '../api/followups.js';
import * as me from '../api/me.js';
import * as notifications from '../api/notifications.js';
import * as oncall from '../api/oncall.js';
import * as runs from '../api/runs.js';
import * as sprints from '../api/sprints.js';

const areas = { agents, analytics, chronicle, comments, followups, me, notifications, oncall, runs, sprints };
const all = {
  ...agents,
  ...analytics,
  ...chronicle,
  ...comments,
  ...followups,
  ...me,
  ...notifications,
  ...oncall,
  ...runs,
  ...sprints,
} satisfies Partial<typeof api>;

describe('web/src/api/<area>.ts', () => {
  it('names only api.ts functions, each a function', () => {
    for (const [area, mod] of Object.entries(areas)) {
      for (const [name, fn] of Object.entries(mod)) {
        expect(typeof fn, `${area}.${name}`).toBe('function');
        expect(Object.keys(api), `${area}.${name}`).toContain(name);
      }
    }
    expect(Object.keys(all)).toHaveLength(41);
  });
});
