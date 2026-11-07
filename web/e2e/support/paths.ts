// Where the suite reads and writes. Everything a run produces is under web/e2e-results/ (git-ignored)
// except the HTML report, which is web/e2e-report/ (git-ignored too): that is what an owner opens.
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const WEB_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
export const REPO_DIR = path.resolve(WEB_DIR, '..');
export const APP_TSX = path.join(WEB_DIR, 'src', 'App.tsx');
export const RESULTS_DIR = path.join(WEB_DIR, 'e2e-results');
/** The run's facts: base URL, accounts, storage states. Written by global-setup.ts. */
export const STATE_FILE = path.join(RESULTS_DIR, 'state.json');
/** One JSON line per screen a spec reached, read by route-guard.e2e.ts. */
export const VISITS_DIR = path.join(RESULTS_DIR, 'visits');
/** A full-page PNG of every screen reached, named by spec, test and route. */
export const SCREENS_DIR = path.join(RESULTS_DIR, 'screens');
