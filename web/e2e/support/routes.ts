// The routes web/src/App.tsx declares, read from the file itself so the route guard cannot drift from
// the router: a `<Route path="...">` added there is a route some spec must reach (route-guard.e2e.ts).
import { readFileSync } from 'node:fs';
import { APP_TSX } from './paths.js';

/**
 * The sign-in screen is not a `<Route>`: App renders `<SignIn />` whenever no one is signed in. It is a
 * screen all the same, so it is required whenever App.tsx renders it.
 */
export const SIGN_IN = 'sign-in';

/**
 * Every `<Route>`'s literal path in `source`, `*` included. It walks each opening tag to its closing
 * `>` outside braces and quotes, so an attribute's JSX or string cannot end it, and reads `path` in any
 * attribute position and quoting. A `<Route>` it cannot pin (no literal path, as with a computed
 * `path={…}` or an index route, other than the `<Shell />` layout route) or a tag that never closes
 * throws, so a route the guard cannot see fails the run instead of passing it. The same walk as
 * internal/web/spa_test.go's appRoutes, which the server's client-route list is checked with.
 */
export function appRoutes(source = readFileSync(APP_TSX, 'utf8')): string[] {
  const routes: string[] = [];
  for (const m of source.matchAll(/<Route[\s/>]/g)) {
    const start = m.index!;
    let depth = 0;
    let end = -1;
    let top = ''; // the tag with everything inside braces dropped
    for (let i = start + '<Route'.length; i < source.length && end < 0; i++) {
      const ch = source[i]!;
      if (ch === '"' || ch === "'" || ch === '`') {
        let j = i + 1;
        while (j < source.length && source[j] !== ch) j += source[j] === '\\' ? 2 : 1;
        if (depth === 0) top += source.slice(i, j + 1);
        i = j;
      } else if (ch === '{') {
        if (depth === 0) top += '{';
        depth++;
      } else if (ch === '}') {
        depth--;
        if (depth === 0) top += '}';
      } else if (ch === '>' && depth === 0) {
        end = i;
      } else if (depth === 0) {
        top += ch;
      }
    }
    if (end < 0) throw new Error(`a <Route> tag at offset ${start} of App.tsx never closes`);
    const tag = source.slice(start, end + 1);
    const p = /(?:^|\s)path\s*=\s*(?:"([^"]*)"|'([^']*)')/.exec(top);
    if (!p) {
      if (tag === '<Route element={<Shell />}>') continue;
      throw new Error(`${tag}: a <Route> other than the <Shell /> layout route without a literal path; the route guard cannot pin it`);
    }
    routes.push(p[1] ?? p[2]!);
  }
  if (/<SignIn\b/.test(source)) routes.unshift(SIGN_IN);
  return routes;
}

/**
 * The declared route react-router picks for `pathname`: of the patterns that match, the one with the
 * fewest `:param` segments (a static segment outranks a param), `*` last.
 */
export function bestRoute(pathname: string, routes: string[]): string | undefined {
  const rank = (r: string) => (r === '*' ? Number.MAX_SAFE_INTEGER : r.split('/').filter((s) => s.startsWith(':')).length);
  return routes
    .filter((r) => r !== SIGN_IN && routeRegex(r).test(pathname))
    .sort((a, b) => rank(a) - rank(b))[0];
}

/** A pathname matcher for a react-router pattern: `:param` is one segment, `*` is anything. */
export function routeRegex(pattern: string): RegExp {
  if (pattern === '*') return /^\/.*$/;
  const body = pattern
    .split('/')
    .map((seg) => (seg.startsWith(':') ? '[^/]+' : seg.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
    .join('/');
  return new RegExp(`^${body}$`);
}
