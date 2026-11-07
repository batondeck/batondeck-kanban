// The suite's server: the real binary, built from this checkout with `make build`, then `migrate`,
// `user add` and `serve` on a free port. SQLite in a scratch directory, or a fresh database on
// BATONDECK_TEST_POSTGRES_DSN when that is set. No dev server and no mock API: the browser talks to
// exactly what `serve` ships, the embedded web/dist included.
//
// The returned function is the global teardown: it stops the server, drops the Postgres database and
// removes the scratch directory.
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { closeSync, mkdirSync, mkdtempSync, openSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { request } from '@playwright/test';
import { REPO_DIR, RESULTS_DIR, SCREENS_DIR, STATE_FILE, VISITS_DIR } from './support/paths.js';
import type { Account, Role, RunState } from './support/state.js';

const ACCOUNTS: Record<Role, { login: string; name: string; admin: boolean }> = {
  admin: { login: 'admin@e2e.test', name: 'Ada Admin', admin: true },
  viewer: { login: 'viewer@e2e.test', name: 'Vic Viewer', admin: false },
  worker: { login: 'worker@e2e.test', name: 'Wes Worker', admin: false },
};

function run(cmd: string, args: string[], opts: { env?: NodeJS.ProcessEnv; input?: string; cwd?: string } = {}): string {
  const r = spawnSync(cmd, args, { env: opts.env, input: opts.input, cwd: opts.cwd, encoding: 'utf8' });
  if (r.status !== 0) {
    throw new Error(`${cmd} ${args.join(' ')} exited ${r.status}:\n${r.stdout}\n${r.stderr}`);
  }
  return r.stdout;
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer();
    s.once('error', reject);
    s.listen(0, '127.0.0.1', () => {
      const addr = s.address();
      s.close(() => (typeof addr === 'object' && addr ? resolve(addr.port) : reject(new Error('no port'))));
    });
  });
}

/** Waits for /healthz to answer, or for the process to exit (a lost race for the port). */
async function healthy(child: ChildProcess, baseURL: string): Promise<boolean> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null || child.signalCode !== null) return false;
    try {
      const res = await fetch(`${baseURL}/healthz`);
      if (res.ok) return true;
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`serve did not answer ${baseURL}/healthz within 30 s`);
}

function stop(child: ChildProcess): Promise<void> {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) return resolve();
    const kill = setTimeout(() => child.kill('SIGKILL'), 10_000);
    child.once('exit', () => {
      clearTimeout(kill);
      resolve();
    });
    child.kill('SIGTERM');
  });
}

export default async function globalSetup(): Promise<() => Promise<void>> {
  for (const d of [VISITS_DIR, SCREENS_DIR]) rmSync(d, { recursive: true, force: true });
  mkdirSync(RESULTS_DIR, { recursive: true });
  rmSync(STATE_FILE, { force: true });

  const scratch = mkdtempSync(path.join(os.tmpdir(), 'batondeck-e2e-'));
  const bin = path.join(scratch, 'batondeck-kanban');
  const cleanups: Array<() => Promise<void> | void> = [() => rmSync(scratch, { recursive: true, force: true })];
  const teardown = async (): Promise<void> => {
    for (const c of cleanups.reverse()) {
      try {
        await c();
      } catch (e) {
        console.error(`e2e teardown: ${String(e)}`);
      }
    }
  };

  try {
    run('make', ['-C', REPO_DIR, 'build', `BINARY=${bin}`, 'VERSION=0.0.0-e2e']);

    // Only what this run sets: a BATONDECK_* variable from the caller's shell (a public URL, a config
    // file) would otherwise change the server under test.
    const env: NodeJS.ProcessEnv = Object.fromEntries(
      Object.entries(process.env).filter(([k]) => !k.startsWith('BATONDECK_')),
    );
    env.BATONDECK_MASTER_KEY = randomBytes(32).toString('base64');
    env.BATONDECK_BLOB_DIR = path.join(scratch, 'blobs');

    const dsn = process.env.BATONDECK_TEST_POSTGRES_DSN;
    let engine: RunState['engine'] = 'sqlite';
    if (dsn) {
      engine = 'postgres';
      const db = `bk_e2e_${randomBytes(6).toString('hex')}`;
      const goEnv = { ...process.env, GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? 'auto' };
      run('go', ['run', 'web/e2e/pgdb.go', 'create', dsn, db], { cwd: REPO_DIR, env: goEnv });
      cleanups.push(() => {
        run('go', ['run', 'web/e2e/pgdb.go', 'drop', dsn, db], { cwd: REPO_DIR, env: goEnv });
      });
      const u = new URL(dsn);
      u.pathname = `/${db}`;
      env.BATONDECK_DATABASE_URL = u.toString();
    } else {
      env.BATONDECK_DATABASE_URL = path.join(scratch, 'e2e.db');
    }

    run(bin, ['migrate'], { env });

    const accounts = {} as Record<Role, Account>;
    for (const [role, a] of Object.entries(ACCOUNTS) as Array<[Role, (typeof ACCOUNTS)[Role]]>) {
      const password = randomBytes(12).toString('hex');
      const out = run(bin, ['user', 'add', ...(a.admin ? ['--admin'] : []), '--name', a.name, a.login], {
        env,
        input: `${password}\n`,
      });
      const id = /\b(U-[0-9a-f]+)\b/.exec(out)?.[1];
      if (!id) throw new Error(`user add printed no account id: ${out}`);
      const token = run(bin, ['token', 'create', '--user', a.login, '--label', `e2e-${role}`], { env }).trim();
      if (!token.startsWith('bd_')) throw new Error(`token create printed no token for ${a.login}`);
      accounts[role] = { login: a.login, name: a.name, password, id, token, storageState: path.join(RESULTS_DIR, `storage-${role}.json`) };
    }

    // serve on a free port; a port taken between the probe and the bind is retried on another.
    let child: ChildProcess | undefined;
    let baseURL = '';
    const logPath = path.join(RESULTS_DIR, 'serve.log');
    rmSync(logPath, { force: true });
    for (let attempt = 0; attempt < 5 && !child; attempt++) {
      const port = await freePort();
      baseURL = `http://127.0.0.1:${port}`;
      const log = openSync(logPath, 'a');
      const c = spawn(bin, ['serve'], { env: { ...env, BATONDECK_LISTEN: `tcp://127.0.0.1:${port}` }, stdio: ['ignore', log, log] });
      closeSync(log);
      if (await healthy(c, baseURL)) child = c;
      else await stop(c);
    }
    if (!child) throw new Error(`serve did not start; see ${logPath}`);
    const server = child;
    cleanups.push(() => stop(server));

    // A signed-in browser session per account, through the same form login the sign-in page posts.
    for (const [role, a] of Object.entries(accounts) as Array<[Role, Account]>) {
      const ctx = await request.newContext({ baseURL });
      const res = await ctx.post('/api/login', { form: { login: a.login, password: a.password } });
      if (!res.ok()) throw new Error(`sign-in as ${role} answered ${res.status()}: ${await res.text()}`);
      await ctx.storageState({ path: a.storageState });
      await ctx.dispose();
    }

    const state: RunState = { baseURL, engine, accounts };
    writeFileSync(STATE_FILE, JSON.stringify(state, null, 2));
    console.log(`e2e: batondeck-kanban serve on ${baseURL} (${engine}), log ${logPath}`);
    return teardown;
  } catch (e) {
    await teardown();
    throw e;
  }
}
