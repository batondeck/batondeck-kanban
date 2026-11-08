// @vitest-environment node
/**
 * P-440: `TOOL_FLOOR` copies the server's tiers, so this pins every row against
 * `internal/domain/grants.go`, the file `authorize` reads. A tier changed there and not here fails
 * this test instead of leaving a control open the server refuses (or closed when it would answer).
 *
 * Read with node's fs, not Vite's `?raw` like the other source guards: grants.go sits outside the web
 * root, which Vite refuses to serve. The specifier is a variable so the browser project needs no node
 * typings for it.
 */
import { describe, it, expect } from 'vitest';
import { TOOL_FLOOR, closedWhy, mayCall, type GatedTool } from '../toolFloors.js';

/** `TierMinRole` in `internal/domain/roles.go`. */
const TIER_ROLE: Record<string, string> = {
  TierWrite: 'worker',
  TierMaster: 'master',
  TierStructural: 'member',
  TierAdmin: 'admin',
};

type Fs = { readFileSync(path: URL, encoding: 'utf8'): string };
const nodeFs = 'node:fs';
const fs = (await import(/* @vite-ignore */ nodeFs)) as Fs;
const grantsSource = fs.readFileSync(new URL('../../../internal/domain/grants.go', import.meta.url), 'utf8');

function serverTiers(): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of grantsSource.matchAll(/"([a-z_]+)":\s*\{Tier:\s*(Tier[A-Za-z]+)/g)) {
    out.set(m[1]!, m[2]!);
  }
  return out;
}

describe('TOOL_FLOOR matches grants.go', () => {
  const tiers = serverTiers();

  it('reads the tool table', () => {
    // A parse that matched nothing would make every row below vacuous.
    expect(tiers.size).toBeGreaterThan(30);
  });

  for (const [tool, floor] of Object.entries(TOOL_FLOOR)) {
    it(`${tool} needs ${floor}`, () => {
      const tier = tiers.get(tool);
      expect(tier, `${tool} is not in grants.go`).toBeDefined();
      expect(TIER_ROLE[tier!], `${tool} is ${tier} in grants.go`).toBe(floor);
    });
  }
});

describe('mayCall and closedWhy', () => {
  it('fails closed for an absent or unknown role', () => {
    expect(mayCall(undefined, 'add_comment')).toBe(false);
    expect(mayCall('stranger', 'add_comment')).toBe(false);
  });

  it('opens each floor to its role and above, and closes it below', () => {
    const order = ['viewer', 'worker', 'master', 'member', 'admin'];
    for (const tool of Object.keys(TOOL_FLOOR) as GatedTool[]) {
      const floor = order.indexOf(TOOL_FLOOR[tool]);
      order.forEach((role, i) => expect(mayCall(role, tool), `${role} ${tool}`).toBe(i >= floor));
    }
  });

  it('gives no reason while the role loads or when the control is open', () => {
    expect(closedWhy(undefined, 'create_task', 'create tasks')).toBeUndefined();
    expect(closedWhy('master', 'create_task', 'create tasks')).toBeUndefined();
  });

  it('names who may, per floor', () => {
    expect(closedWhy('viewer', 'add_comment', 'comment')).toBe('A viewer cannot comment.');
    expect(closedWhy('worker', 'update_task', 'edit tasks')).toBe('Only a master can edit tasks.');
    expect(closedWhy('master', 'update_column', 'rename columns')).toBe(
      'Only a member or an admin can rename columns.',
    );
    expect(closedWhy('member', 'add_member', 'add members')).toBe('Only a project admin can add members.');
  });
});
