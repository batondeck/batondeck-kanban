// The facts global-setup.ts hands every worker: the server's base URL and the accounts it made.
import { readFileSync } from 'node:fs';
import { STATE_FILE } from './paths.js';

export type Role = 'admin' | 'viewer' | 'worker';

export interface Account {
  login: string;
  password: string;
  name: string;
  id: string;
  /** An API token (`bd_…`) for this account, for MCP and REST calls made outside the browser. */
  token: string;
  /** Playwright storage state holding a signed-in browser session for this account. */
  storageState: string;
}

export interface RunState {
  baseURL: string;
  engine: 'sqlite' | 'postgres';
  accounts: Record<Role, Account>;
}

let cached: RunState | undefined;

export function runState(): RunState {
  cached ??= JSON.parse(readFileSync(STATE_FILE, 'utf8')) as RunState;
  return cached;
}
