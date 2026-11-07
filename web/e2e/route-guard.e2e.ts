// THE ROUTE GUARD. Every route src/App.tsx declares (and the sign-in screen it renders when no one is
// signed in) must have been reached by a UI spec in this run, through `atRoute` or `atRedirect`,
// which check the URL against the pattern before recording it. A route added to App.tsx with no spec
// that reaches it fails here, by name. It runs as its own project after the `ui` project, so it reads
// the visits of the whole run.
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { expect, test } from '@playwright/test';
import { VISITS_DIR } from './support/paths.js';
import { appRoutes } from './support/routes.js';

interface Visit {
  route: string;
  url: string;
  spec: string;
  test: string;
}

function visits(): Visit[] {
  if (!existsSync(VISITS_DIR)) return [];
  return readdirSync(VISITS_DIR)
    .filter((f) => f.endsWith('.jsonl'))
    .flatMap((f) => readFileSync(path.join(VISITS_DIR, f), 'utf8').split('\n').filter(Boolean))
    .map((l) => JSON.parse(l) as Visit);
}

test('every route in src/App.tsx is visited by a spec', () => {
  const routes = appRoutes();
  expect(routes.length, 'App.tsx declares routes (the parser found none)').toBeGreaterThan(10);
  const seen = visits();
  const reached = new Set(seen.map((v) => v.route));
  const missing = routes.filter((r) => !reached.has(r));
  expect(missing, `App.tsx route(s) no e2e spec visited: ${missing.join(', ')}`).toEqual([]);
  // The other direction: a spec recording a route App.tsx no longer has is reaching a stale pattern.
  const stale = [...reached].filter((r) => !routes.includes(r));
  expect(stale, `spec(s) record route(s) App.tsx does not declare: ${stale.join(', ')}`).toEqual([]);
  console.log(
    routes
      .map((r) => {
        const by = [...new Set(seen.filter((v) => v.route === r).map((v) => v.spec))];
        return `  ${r.padEnd(46)} ${by.join(', ')}`;
      })
      .join('\n'),
  );
});
