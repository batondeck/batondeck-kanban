// The suite's `test`: Playwright's, pointed at the server global-setup.ts started and signed in as
// an account (`role`, admin unless a spec says otherwise), plus `atRoute`, which every spec calls on
// each screen it reaches: it checks the URL is that App.tsx route, saves a full-page screenshot into
// the report and web/e2e-results/screens/, and records the visit for the route guard.
import { appendFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { test as base, expect, type Page } from '@playwright/test';
import { SCREENS_DIR, VISITS_DIR } from './paths.js';
import { appRoutes, bestRoute, routeRegex, SIGN_IN } from './routes.js';
import { runState, type Role } from './state.js';

export { expect };

export const test = base.extend<{ role: Role | null }>({
  /** The account the browser is signed in as; `null` starts signed out. */
  role: ['admin', { option: true }],
  baseURL: async ({}, use) => {
    await use(runState().baseURL);
  },
  storageState: async ({ role }, use) => {
    await use(role ? runState().accounts[role].storageState : { cookies: [], origins: [] });
  },
});

const slug = (s: string): string =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 60) || 'root';

let shots = 0;

/** Records that the running test reached `route` (an App.tsx pattern, or SIGN_IN), for the guard. */
function recordVisit(route: string, url: string): void {
  const info = test.info();
  mkdirSync(VISITS_DIR, { recursive: true });
  const line = JSON.stringify({ route, url, spec: path.basename(info.file), test: info.title });
  appendFileSync(path.join(VISITS_DIR, `${info.workerIndex}.jsonl`), `${line}\n`);
}

async function screenshot(page: Page, route: string, label: string): Promise<void> {
  const info = test.info();
  // spec--test--NN-what.png: NN is the run's capture order; the route itself is in the report's caption.
  const what = slug(label || (route === '/' ? 'root' : route));
  const name = `${path.basename(info.file, '.e2e.ts')}--${slug(info.title).slice(0, 32)}--${String(++shots).padStart(2, '0')}-${what}.png`;
  mkdirSync(SCREENS_DIR, { recursive: true });
  // `animations: 'disabled'` runs CSS transitions to their end, so a nav highlight or a card that is
  // still fading in is captured as it settles rather than half-way.
  const body = await page.screenshot({ path: path.join(SCREENS_DIR, name), fullPage: true, animations: 'disabled' });
  await info.attach(`${route}${label ? ` (${label})` : ''}`, { body, contentType: 'image/png' });
}

/**
 * The page is on `route`: the URL's path matches the App.tsx pattern (the sign-in screen has no path
 * of its own and is checked by its form instead). Call it once the screen's content is asserted, so
 * the screenshot shows the screen rather than its loader.
 */
export async function atRoute(page: Page, route: string, label = ''): Promise<void> {
  if (route === SIGN_IN) {
    await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
  } else {
    const re = routeRegex(route);
    await expect.poll(() => new URL(page.url()).pathname, { message: `the URL is route ${route}` }).toMatch(re);
    const pathname = new URL(page.url()).pathname;
    expect(bestRoute(pathname, appRoutes()), `${pathname} is App.tsx's route ${route}, not a more specific one`).toBe(route);
  }
  await screenshot(page, route, label);
  recordVisit(route, page.url());
}

/**
 * A route that only redirects (`/` and `*` in App.tsx): reach `pathname` and land on `landing`. The
 * visit is recorded for the redirect's own pattern, since its element is the redirect.
 *
 * `client` navigates inside the running app (history.pushState and a popstate, which is what the
 * router listens to) instead of loading the document: the server answers a path that is not a client
 * route with a 404 of its own (internal/web/spa.go), so the `*` route is only ever reached that way.
 */
export async function atRedirect(
  page: Page,
  route: string,
  pathname: string,
  landing: string,
  opts: { client?: boolean } = {},
): Promise<void> {
  if (opts.client) {
    await page.evaluate((p) => {
      window.history.pushState({}, '', p);
      window.dispatchEvent(new PopStateEvent('popstate'));
    }, pathname);
  } else {
    await page.goto(pathname);
  }
  await expect.poll(() => new URL(page.url()).pathname, { message: `${pathname} redirects to ${landing}` }).toBe(landing);
  await screenshot(page, route, `redirected to ${landing}`);
  recordVisit(route, page.url());
}

export { SIGN_IN };
