// The routes web/src/App.tsx declares, read from the file itself so the route guard cannot drift from
// the router: a `<Route path="...">` added there is a route some spec must reach (route-guard.e2e.ts).
import { readFileSync } from 'node:fs';
import { APP_TSX } from './paths.js';

/**
 * The sign-in screen is not a `<Route>`: App renders `<SignIn />` whenever no one is signed in. It is a
 * screen all the same, so it is required whenever App.tsx renders it.
 */
export const SIGN_IN = 'sign-in';

export function appRoutes(source = readFileSync(APP_TSX, 'utf8')): string[] {
  const routes = [...source.matchAll(/<Route\b[^>]*?\bpath="([^"]+)"/g)].map((m) => m[1]!);
  if (/<SignIn\b/.test(source)) routes.unshift(SIGN_IN);
  return routes;
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
