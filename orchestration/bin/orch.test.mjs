// Tests for orch.mjs over a fixture plan built from the real templates in ../schema.
// No network: the "origin" a merge SHA is checked against is a bare repository in a temp folder.
import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const BIN = path.dirname(fileURLToPath(import.meta.url));
const ORCH = path.join(BIN, 'orch.mjs');
const SCHEMA = path.join(BIN, '..', 'schema');
const NOW = '2026-10-21T09:00:00Z';
const TOMORROW = '2026-10-22T00:00:00Z';
const tpl = (n) => fs.readFileSync(path.join(SCHEMA, `${n}.md`), 'utf8');

const cleanEnv = () => {
  const env = { ...process.env };
  for (const k of Object.keys(env)) if (k.startsWith('GIT_')) delete env[k];
  delete env.ORCH_NOW;
  return env;
};
function git(cwd, ...args) {
  const r = spawnSync('git', ['-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', '-c', 'user.name=fixture',
    '-c', 'user.email=fixture@example.invalid', ...args], { cwd, encoding: 'utf8', env: cleanEnv() });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
  return r.stdout.trim();
}

// ---- building files from the templates ----
function setFront(text, values) {
  for (const [k, v] of Object.entries(values)) {
    const re = new RegExp(`^${k}: .*$`, 'm');
    assert.ok(re.test(text), `template has key ${k}`);
    const val = Array.isArray(v) ? `[${v.join(', ')}]` : v === '' ? '""' : v;
    text = text.replace(re, `${k}: ${val}`);
  }
  return text;
}
function setSection(text, heading, content) {
  const lines = text.split('\n');
  const i = lines.indexOf(heading);
  assert.ok(i >= 0, `heading ${heading} present`);
  let j = i + 1;
  while (j < lines.length && !/^#{1,6} /.test(lines[j])) j++;
  lines.splice(i + 1, j - i - 1, ...content.split('\n'));
  return lines.join('\n');
}
const setH1 = (text, h1) => text.replace(/^# .*$/m, `# ${h1}`);

const BODY = {
  '## Why': 'The fixture needs a task that exercises the harness (fixture plan line 1).',
  '## Requirements': '1. The tool accepts the task (orchestration/README.md).',
  '## Inputs': '- `orchestration/schema/task.md` — the template this file was built from.',
  '## Outputs': '- `widget.txt` — the file the task makes.',
  '## Acceptance criteria': '- [ ] AC1 `cat widget.txt` prints widget — evidence:\n- [ ] AC2 `orch validate` prints valid — evidence:',
  '## Definition of done': '- The standard list in `orchestration/README.md`, and:\n- nothing beyond the standard list',
  '## Context to load': '1. `orchestration/README.md` — the method, all of it\nDo not read: anything else.',
  '## Zones': '- none',
};
function taskText(v) {
  let t = setFront(tpl('task'), {
    id: v.id, title: v.title, plan: 'fx', feature: 'F1', release: v.release, status: 'todo', depends_on: v.depends_on || [],
    not_before: v.not_before || '', repo: v.repo, agent: v.agent, size: 'M', zones: v.zones || [], next: v.next || '',
  });
  t = setH1(t, `${v.id} ${v.title}`);
  for (const [h, c] of Object.entries(BODY)) t = setSection(t, h, c);
  return setSection(t, '## Handoff', 'Nothing beyond this file.');
}
const TASKS = [
  { id: 'A', title: 'Build the widget', release: 'R1', repo: 'umbrella', agent: 'builder', next: 'B' },
  { id: 'B', title: 'Wire the widget', release: 'R1', repo: 'umbrella', agent: 'builder', depends_on: ['A'] },
  { id: 'C', title: 'Approve the release', release: 'R2', repo: 'none', agent: 'owner', zones: ['Z3'] },
  { id: 'D', title: 'Observe the acceptance', release: 'R1', repo: 'none', agent: 'orchestrator', not_before: '2026-10-22' },
];
const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, '-');
const PLAN_DIR = 'orchestration/fx';
const TASK_DIR = `${PLAN_DIR}/F1-widget/tasks`;
const taskRel = (id) => `${TASK_DIR}/${id}-${slug(TASKS.find((t) => t.id === id)?.title || '')}.md`;

// One golden fixture (a git repository with a bare origin) is built once; each test works on a copy of both.
const temps = [];
after(() => { for (const d of temps) fs.rmSync(d, { recursive: true, force: true }); });
const tempDir = (prefix) => { const d = fs.mkdtempSync(path.join(os.tmpdir(), prefix)); temps.push(d); return d; };
let golden = null;
function fixture() {
  golden ??= buildGolden();
  const root = tempDir('orch-fx-');
  const origin = tempDir('orch-origin-');
  fs.cpSync(golden.root, root, { recursive: true });
  fs.cpSync(golden.origin, origin, { recursive: true });
  git(root, 'remote', 'set-url', 'origin', origin);
  return { root, origin, head: golden.head };
}
function buildGolden() {
  const root = tempDir('orch-golden-');
  const origin = tempDir('orch-origin-');
  const w = (rel, text) => { fs.mkdirSync(path.dirname(path.join(root, rel)), { recursive: true }); fs.writeFileSync(path.join(root, rel), text); };
  w('source-plan.md', '# The fixture plan of record\n');
  const sha = crypto.createHash('sha256').update('# The fixture plan of record\n').digest('hex');
  w(`${PLAN_DIR}/PLAN.md`, setH1(setFront(tpl('plan'), {
    id: 'fx', title: 'Fixture plan', source: path.join(root, 'source-plan.md'), source_sha256: sha, releases: ['R1', 'R2'],
  }), 'Fixture plan'));
  w(`${PLAN_DIR}/CONTEXT.md`, setH1(tpl('context'), 'Context: the fixture plan'));
  w(`${PLAN_DIR}/F1-widget/FEATURE.md`, setH1(setFront(tpl('feature'), { id: 'F1', title: 'Widget', plan: 'fx', releases: ['R1', 'R2'] }), 'F1 Widget'));
  w(`${PLAN_DIR}/F1-widget/CONTEXT.md`, setH1(tpl('context'), 'Context: F1 Widget'));
  for (const t of TASKS) w(taskRel(t.id), taskText({ ...t, not_before: '' }));
  w('.claude/agents/builder.md', '---\nname: builder\ndescription: fixture builder\nautoCompactWindow: 300000\n---\n\nBUILDER-DEFINITION You build exactly one approved item.\n');
  w('.claude/agents/cheap.md', '---\nname: cheap\n---\n\nCHEAP-DEFINITION Mechanical work.\n');
  // not_before is a field the tool writes and metrics.jsonl records, so the fixture sets it through the tool.
  for (const t of TASKS.filter((x) => x.not_before)) ok(orch({ root }, ['not-before', t.id, t.not_before]));
  git(root, 'init', '-q', '-b', 'main');
  git(root, 'add', '.');
  git(root, 'commit', '-q', '-m', 'fixture');
  git(origin, 'init', '-q', '--bare', '-b', 'main');
  git(root, 'remote', 'add', 'origin', origin);
  git(root, 'push', '-q', 'origin', 'main');
  return { root, origin, head: git(root, 'rev-parse', 'HEAD') };
}
function orch(fx, args, at = NOW) {
  const r = spawnSync(process.execPath, [ORCH, ...args], { cwd: fx.root, encoding: 'utf8', env: { ...cleanEnv(), ORCH_NOW: at } });
  return { code: r.status, out: r.stdout, err: r.stderr };
}
const ok = (r) => { assert.equal(r.code, 0, `expected success, got ${r.code}: ${r.err}`); return r; };
function refused(r, line) {
  assert.equal(r.code, 1, `expected a refusal (exit 1), got ${r.code}: ${r.out}${r.err}`);
  const lines = r.err.split('\n');
  const hit = line instanceof RegExp ? lines.some((l) => line.test(l)) : lines.includes(line);
  assert.ok(hit, `expected the line\n  ${line}\nin\n${r.err}`);
}
const readRel = (fx, rel) => fs.readFileSync(path.join(fx.root, rel), 'utf8');
const writeRel = (fx, rel, text) => fs.writeFileSync(path.join(fx.root, rel), text);
const edit = (fx, id, fn) => writeRel(fx, taskRel(id), fn(readRel(fx, taskRel(id))));
const field = (fx, id, k) => (new RegExp(`^${k}: (.*)$`, 'm').exec(readRel(fx, taskRel(id))) || [])[1];
const events = (fx) => readRel(fx, `${PLAN_DIR}/metrics.jsonl`).split('\n').filter(Boolean).map((l) => JSON.parse(l));
// The refusal validate gives when a field differs from what the task's events in metrics.jsonl give.
const held = (rel, k, have, want) => `orch: ${rel}: ${k} is ${JSON.stringify(have)}; the events in metrics.jsonl give ${JSON.stringify(want)}`;
const setKey = (text, k, v) => text.replace(new RegExp(`^${k}: .*$`, 'm'), `${k}: ${v === '' ? '""' : v}`);
const forge = (fx, ev) => fs.appendFileSync(path.join(fx.root, PLAN_DIR, 'metrics.jsonl'), `${JSON.stringify(ev)}\n`);
const tick = (text) => text.replace('- [ ] AC1 `cat widget.txt` prints widget — evidence:', '- [x] AC1 `cat widget.txt` prints widget — evidence: printed widget')
  .replace('- [ ] AC2 `orch validate` prints valid — evidence:', '- [x] AC2 `orch validate` prints valid — evidence: orch: valid: 4 tasks, 1 features');
const SIX = ['built', 'why', 'deviations', 'gates', 'review', 'observed'];

// Drive A to in_review (claim at `at`, the three log sections in_review needs, the PR).
function toReview(fx, at = NOW) {
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget'], at));
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`], at));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1'], at));
}
// Push a new commit to origin's `ref`, as a merge does; returns its SHA. A merge SHA must be newer than the claim.
function land(fx, name = 'work', ref = 'main') {
  writeRel(fx, `${name}.txt`, `${name}\n`);
  git(fx.root, 'add', `${name}.txt`);
  git(fx.root, 'commit', '-q', '-m', name);
  git(fx.root, 'push', '-q', 'origin', `HEAD:refs/heads/${ref}`);
  return git(fx.root, 'rev-parse', 'HEAD');
}
function toMerged(fx, at = NOW) {
  toReview(fx, at);
  for (const s of ['deviations', 'review']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`], at));
  const sha = land(fx);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha], at));
  ok(orch(fx, ['log', 'A', '--section', 'observed', '--text', 'observed after the merge'], at));
  return sha;
}

// ---------------- validate ----------------
test('validate is green on the fixture', () => {
  const fx = fixture();
  assert.equal(ok(orch(fx, ['validate'])).out, 'orch: valid: 4 tasks, 1 features\n');
});

test('validate refuses a missing heading', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => setSection(t, '## Why', BODY['## Why']).replace(`## Inputs\n${BODY['## Inputs']}\n`, ''));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: heading 4 is "## Outputs", the template has "## Inputs"`);
});

test('validate takes PLAN.md\'s ### release subsections and still refuses a missing ## heading', () => {
  const fx = fixture();
  const rel = `${PLAN_DIR}/PLAN.md`;
  writeRel(fx, rel, readRel(fx, rel).replace('## Releases and their acceptance\n', '## Releases and their acceptance\n### R1\nThe R1 row.\n'));
  ok(orch(fx, ['validate']));
  writeRel(fx, rel, readRel(fx, rel).replace('## Feature map\n', ''));
  refused(orch(fx, ['validate']), `orch: ${rel}: heading 4 is "## How to resume", the template has "## Feature map"`);
});

test('validate refuses a renamed key', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => t.replace('merge_sha: ""', 'merge_hash: ""'));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: missing front-matter key "merge_sha"`);
});

test('validate refuses a dependency cycle and names it', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => t.replace('depends_on: []', 'depends_on: [B]'));
  refused(orch(fx, ['validate']), `orch: ${PLAN_DIR}: dependency cycle: A -> B -> A`);
});

test('validate refuses an unknown status', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => t.replace('status: todo', 'status: started'));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: status "started" is not one of todo, in_progress, in_review, merged, done, blocked, dropped, proposed`);
});

test('validate refuses a ticked acceptance criterion without evidence', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => t.replace('- [ ] AC1', '- [x] AC1'));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: AC1 is ticked without evidence`);
});

test('validate refuses a done task whose observer is its claimer', () => {
  const fx2 = fixture();
  toMerged(fx2);
  edit(fx2, 'A', (t) => tick(t).replace('status: merged', 'status: done').replace('observed_by: ""', 'observed_by: builder-1')
    .replace('finished_at: ""', `finished_at: ${NOW}`));
  refused(orch(fx2, ['validate']), `orch: ${taskRel('A')}: observed_by "builder-1" is the claimer; the observer must be someone else`);
});

test('validate refuses an over-budget feature CONTEXT.md', () => {
  const fx = fixture();
  writeRel(fx, `${PLAN_DIR}/F1-widget/CONTEXT.md`, `# Context: F1 Widget\n\n${'word '.repeat(1001)}\n`);
  refused(orch(fx, ['validate']), `orch: ${PLAN_DIR}/F1-widget/CONTEXT.md: 1005 words, over the 1000-word budget`);
});

test('validate refuses a template placeholder left in a required section', () => {
  const fx = fixture();
  const placeholder = /## Why\n(.*)\n/.exec(tpl('task'))[1];
  edit(fx, 'A', (t) => setSection(t, '## Why', placeholder));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: ## Why is empty or still the template's placeholder`);
});

// Every other check validate makes, one edit each on a copy of the fixture, restored after.
test('validate refuses each malformed field, file and folder', () => {
  const fx = fixture();
  const A = taskRel('A');
  const keys = [...tpl('task').matchAll(/^([a-z_]+): /gm)].map((m) => m[1]);
  const cases = [
    ['depends_on: []', 'depends_on: [Q]', `orch: ${A}: depends_on Q, which does not exist`],
    ['next: B', 'next: Q', `orch: ${A}: next Q, which does not exist`],
    ['discovered_from: ""', 'discovered_from: Q', `orch: ${A}: discovered_from Q, which does not exist`],
    ['feature: F1', 'feature: F2', `orch: ${A}: feature is "F2", the folder's FEATURE.md is F1`],
    ['release: R1', 'release: R9', `orch: ${A}: release "R9" is not one of PLAN.md's releases (R1, R2)`],
    ['agent: builder', 'agent: robot', `orch: ${A}: agent "robot" is not one of builder, cheap, owner, orchestrator`],
    ['size: M', 'size: XL', `orch: ${A}: size "XL" is not one of S, M, L`],
    ['zones: []', 'zones: [Z9]', `orch: ${A}: zone "Z9" is not one of Z1..Z8`],
    ['zones: []', 'zones: Z2', `orch: ${A}: zones must be a list ([a, b] or [])`],
    ['repo: umbrella', 'repo: none', `orch: ${A}: repo none is for owner and orchestrator tasks; agent builder needs a repository`],
    ['repo: umbrella', 'repo: Bad_Repo', `orch: ${A}: repo "Bad_Repo" is not a repository name, "umbrella" or "none"`],
    ['not_before: ""', 'not_before: tomorrow', `orch: ${A}: not_before "tomorrow" is not YYYY-MM-DD`],
    ['merge_sha: ""', 'merge_sha: xyz', `orch: ${A}: merge_sha "xyz" is not a hex SHA`],
    ['base_sha: ""', 'base_sha: abc1234', `orch: ${A}: base_sha "abc1234" is not a full hex SHA`],
    ['plan: fx', 'plan: other', `orch: ${A}: plan is "other", the folder is fx`],
    ['id: A\n', 'id: A!\n', `orch: ${A}: id "A!" is not an id (letters, digits, "." and "-")`],
    ['title: Build the widget', 'title: ""', `orch: ${A}: title is empty`],
    ['# A Build the widget', '# A Build widget', `orch: ${A}: heading 1 is "# A Build widget", the template has "# A Build the widget"`],
    ['id: A\ntitle: Build the widget\n', 'title: Build the widget\nid: A\n', `orch: ${A}: front-matter keys out of order; the template's order is: ${keys.join(', ')}`],
    ['size: M\n', 'size: M\nsize: M\n', `orch: ${A}: a front-matter key appears twice`],
    ['- [ ] AC1 `cat', '- [ ] 1. `cat', `orch: ${A}: acceptance line 1 does not read "- [ ] ACn … — evidence: …": - [ ] 1. \`cat widget.txt\` prints widget — evidence:`],
    ['status: todo', 'status: in_progress', `orch: ${A}: in_progress needs claimed_by (orch claim --by)`],
    ['status: todo', 'status: in_progress', `orch: ${A}: in_progress needs branch (orch claim --branch)`],
    ['status: todo', 'status: in_progress', `orch: ${A}: in_progress needs started_at (set by orch claim)`],
    ['status: todo', 'status: in_progress', `orch: ${A}: in_progress needs base_sha (set by orch claim)`],
    ['status: todo', 'status: done', `orch: ${A}: done needs finished_at (set by orch set done)`],
    ['status: todo', 'status: done', `orch: ${A}: done needs at least one acceptance line`, (t) => t.replace(/- \[ \] AC\d.*\n/g, '')],
    ['## Why\n', `## Why\n${'word '.repeat(1300)}\n`, new RegExp(`^orch: ${A.replace(/\./g, '\\.')}: \\d+ words without the Execution log, over the 1200-word budget$`)],
  ];
  for (const [from, to, line, extra] of cases) {
    const before = readRel(fx, A);
    assert.ok(before.includes(from), `fixture holds ${from}`);
    writeRel(fx, A, (extra || ((t) => t))(before.replace(from, to)));
    refused(orch(fx, ['validate']), line);
    writeRel(fx, A, before);
  }
  ok(orch(fx, ['validate']));
  const P = `${PLAN_DIR}/PLAN.md`;
  const F = `${PLAN_DIR}/F1-widget/FEATURE.md`;
  const files = [
    [P, 'id: fx', 'id: fy', `orch: ${P}: id is "fy", the folder is fx`],
    [P, 'releases: [R1, R2]', 'releases: []', `orch: ${P}: releases must be a non-empty list`],
    [P, 'releases: [R1, R2]', 'releases: [R1, R2, R1]', `orch: ${P}: releases lists a release twice`],
    [P, /source_sha256: .*/, 'source_sha256: abc', `orch: ${P}: source_sha256 is not a sha256`],
    [F, 'releases: [R1, R2]', 'releases: [R1, R9]', `orch: ${F}: release "R9" is not one of PLAN.md's releases`],
    [F, 'plan: fx', 'plan: other', `orch: ${F}: plan is "other", the folder is fx`],
    [F, '# F1 Widget', '# F1 Gadget', `orch: ${F}: heading 1 is "# F1 Gadget", the template has "# F1 Widget"`],
    [`${PLAN_DIR}/CONTEXT.md`, '## Do not read', `## Do not read\n${'word '.repeat(1500)}`, new RegExp(`^orch: ${PLAN_DIR}/CONTEXT\\.md: \\d+ words, over the 1500-word budget$`)],
  ];
  for (const [rel, from, to, line] of files) {
    const before = readRel(fx, rel);
    writeRel(fx, rel, before.replace(from, to));
    refused(orch(fx, ['validate']), line);
    writeRel(fx, rel, before);
  }
  const copy = `${TASK_DIR}/A-copy.md`;
  writeRel(fx, copy, readRel(fx, A));
  refused(orch(fx, ['validate']), `orch: ${copy}: id A is also ${A}`);
  fs.renameSync(path.join(fx.root, copy), path.join(fx.root, `${TASK_DIR}/A_copy.md`));
  refused(orch(fx, ['validate']), `orch: ${TASK_DIR}/A_copy.md: file name must be <id>-<slug>.md with a lower-case slug (id A)`);
  fs.rmSync(path.join(fx.root, `${TASK_DIR}/A_copy.md`));
  fs.renameSync(path.join(fx.root, `${PLAN_DIR}/F1-widget/CONTEXT.md`), path.join(fx.root, `${PLAN_DIR}/F1-widget/context.md`));
  refused(orch(fx, ['validate']), `orch: ${PLAN_DIR}/F1-widget/CONTEXT.md: missing`);
  fs.renameSync(path.join(fx.root, `${PLAN_DIR}/F1-widget/context.md`), path.join(fx.root, `${PLAN_DIR}/F1-widget/CONTEXT.md`));
  fs.mkdirSync(path.join(fx.root, `${PLAN_DIR}/F2-stray/tasks`), { recursive: true });
  refused(orch(fx, ['validate']), `orch: ${PLAN_DIR}/F2-stray: a tasks/ folder without FEATURE.md`);
  fs.rmSync(path.join(fx.root, `${PLAN_DIR}/F2-stray`), { recursive: true });
  ok(orch(fx, ['validate']));
});

// ---------------- ready ----------------
test('ready orders by release, depth, id and holds D until its date', () => {
  const fx = fixture();
  const line = (id) => { const t = TASKS.find((x) => x.id === id); return [t.id, t.release, 'F1', t.repo, t.agent, 'M', t.title].join('\t'); };
  assert.equal(ok(orch(fx, ['ready'])).out, `${line('A')}\n${line('C')}\n`);
  assert.equal(ok(orch(fx, ['ready'], TOMORROW)).out, `${line('A')}\n${line('D')}\n${line('C')}\n`);
});

// ---------------- claim ----------------
test('claim refuses a task that is not ready', () => {
  const fx = fixture();
  refused(orch(fx, ['claim', 'B', '--by', 'builder-1', '--branch', 'feat/b']), 'orch: B: not in the ready frontier: depends on A, which is todo');
  assert.equal(field(fx, 'B', 'status'), 'todo');
});

test('claim refuses a second task in the same repository', () => {
  const fx = fixture();
  edit(fx, 'D', (t) => t.replace('repo: none', 'repo: umbrella'));
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  refused(orch(fx, ['claim', 'D', '--by', 'builder-2', '--branch', 'feat/d'], TOMORROW), 'orch: D: A is already in_progress in repo umbrella; one builder per repository');
  assert.equal(field(fx, 'D', 'status'), 'todo');
});

// The cap of two builder tasks in_progress, across repositories (P-7). C and D become builder tasks, each in a
// checkout of its own (a clone of the fixture's origin), so the one-per-repository rule never decides these tests.
function builderIn(fx, id, repo) {
  edit(fx, id, (t) => t.replace('repo: none', `repo: ${repo}`).replace(/^agent: .*$/m, 'agent: builder'));
  git(fx.root, 'clone', '-q', fx.origin, repo);
}
const capLine = (ids) => `at most 2 builder tasks in_progress at once (repo none is not counted); ${ids} are`;
test('claim refuses a third builder task in_progress, across repositories', () => {
  const fx = fixture();
  builderIn(fx, 'C', 'repo-c');
  builderIn(fx, 'D', 'repo-d');
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  ok(orch(fx, ['claim', 'C', '--by', 'builder-2', '--branch', 'feat/c']));
  refused(orch(fx, ['claim', 'D', '--by', 'builder-3', '--branch', 'feat/d'], TOMORROW), `orch: D: ${capLine('A and C')}`);
  assert.equal(field(fx, 'D', 'status'), 'todo');
});

test('a review round that would make a third builder task in_progress is refused, naming the two (P-7)', () => {
  const fx = fixture();
  builderIn(fx, 'C', 'repo-c');
  builderIn(fx, 'D', 'repo-d');
  toReview(fx);
  ok(orch(fx, ['claim', 'C', '--by', 'builder-2', '--branch', 'feat/c'], TOMORROW));
  ok(orch(fx, ['claim', 'D', '--by', 'builder-3', '--branch', 'feat/d'], TOMORROW));
  refused(orch(fx, ['set', 'A', 'in_progress', '--reason', 'x'], TOMORROW), `orch: A: ${capLine('C and D')}: set one blocked --reason, or wait for its in_review`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
  ok(orch(fx, ['validate']));
  ok(orch(fx, ['set', 'D', 'blocked', '--reason', 'A is sent back'], TOMORROW));
  ok(orch(fx, ['set', 'A', 'in_progress', '--reason', 'x'], TOMORROW));
  assert.equal(field(fx, 'A', 'status'), 'in_progress');
});

test('owner and orchestrator tasks (repo none) are not counted: claimed and reopened beside two builder tasks (#71)', () => {
  const fx = fixture();
  builderIn(fx, 'D', 'repo-d');
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  ok(orch(fx, ['claim', 'D', '--by', 'builder-2', '--branch', 'feat/d'], TOMORROW));
  ok(orch(fx, ['claim', 'C', '--by', 'owner'], TOMORROW));
  assert.equal(field(fx, 'C', 'status'), 'in_progress');
  ok(orch(fx, ['validate']));
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'C', '--section', s, '--text', `${s}: the owner ran the release`], TOMORROW));
  ok(orch(fx, ['set', 'C', 'in_review'], TOMORROW));
  ok(orch(fx, ['set', 'C', 'in_progress', '--reason', 'the owner is asked again'], TOMORROW));
  assert.equal(field(fx, 'C', 'status'), 'in_progress');
  ok(orch(fx, ['validate']));
});

test('validate reports three builder tasks in_progress, naming them (P-7)', () => {
  const fx = fixture();
  builderIn(fx, 'C', 'repo-c');
  builderIn(fx, 'D', 'repo-d');
  toReview(fx);
  ok(orch(fx, ['claim', 'C', '--by', 'builder-2', '--branch', 'feat/c'], TOMORROW));
  ok(orch(fx, ['claim', 'D', '--by', 'builder-3', '--branch', 'feat/d'], TOMORROW));
  ok(orch(fx, ['validate'], TOMORROW));
  edit(fx, 'A', (t) => t.replace('status: in_review', 'status: in_progress'));
  forge(fx, { at: TOMORROW, task: 'A', event: 'status', from: 'in_review', to: 'in_progress', reason: 'x' });
  refused(orch(fx, ['validate'], TOMORROW), `orch: ${PLAN_DIR}: ${capLine('A, C and D')}`);
});

test('claim needs --branch for a task with a repository', () => {
  const fx = fixture();
  refused(orch(fx, ['claim', 'A', '--by', 'builder-1']), 'orch: --branch: must not be empty');
});

const readyB = ['B', 'R1', 'F1', 'umbrella', 'builder', 'M', 'Wire the widget'].join('\t');
const readyC = ['C', 'R2', 'F1', 'none', 'owner', 'M', 'Approve the release'].join('\t');
test('ready and claim stack a task on a dependency in_review or merged on its branch, and only with that --branch', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']));
  assert.equal(ok(orch(fx, ['ready'])).out, `${readyC}\n`);
  refused(orch(fx, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/widget']), 'orch: B: not in the ready frontier: depends on A, which is in_progress');
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1']));
  assert.equal(ok(orch(fx, ['ready'])).out, `${readyB}\t(stack on feat/widget)\n${readyC}\n`);
  ok(orch(fx, ['index']));
  assert.ok(readRel(fx, `${PLAN_DIR}/INDEX.md`).split('\n').includes('- B (R1, F1, umbrella, builder) Wire the widget (stack on feat/widget)'));
  const stackLine = 'orch: B: A is in_review on feat/widget; claim with --branch feat/widget to stack on its PR, or wait for done';
  refused(orch(fx, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/b']), stackLine);
  assert.equal(field(fx, 'B', 'status'), 'todo');
  ok(orch(fx, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/widget']));
  assert.deepEqual([field(fx, 'B', 'status'), field(fx, 'B', 'branch')], ['in_progress', 'feat/widget']);
  assert.equal(events(fx).pop().stacked_on, 'feat/widget');

  const fm = fixture();
  toMerged(fm);
  assert.equal(ok(orch(fm, ['ready'])).out, `${readyB}\t(stack on feat/widget)\n${readyC}\n`);
  refused(orch(fm, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/b']), stackLine.replace('in_review', 'merged'));
});

// A stacked task is claimed while its dependency is in_review; the dependency's review round must not put a second
// task of the repository in_progress (P-1, found in #54's review). The two ways out both get through.
function stackB(fx) {
  toReview(fx);
  ok(orch(fx, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/widget']));
}
test('set <dep> in_progress is refused while a task stacked on it is in_progress, naming it and the way out', () => {
  const f = fixture();
  stackB(f);
  refused(orch(f, ['set', 'A', 'in_progress', '--reason', 'x']), 'orch: A: B is already in_progress in repo umbrella; one builder per repository: set B blocked --reason, or wait for its in_review');
  assert.deepEqual([field(f, 'A', 'status'), field(f, 'B', 'status')], ['in_review', 'in_progress']);
  ok(orch(f, ['validate']));
  ok(orch(f, ['set', 'B', 'blocked', '--reason', 'A is sent back']));
  ok(orch(f, ['set', 'A', 'in_progress', '--reason', 'x']));
  assert.equal(field(f, 'A', 'status'), 'in_progress');

  const w = fixture();
  stackB(w);
  for (const s of ['built', 'why', 'gates']) ok(orch(w, ['log', 'B', '--section', s, '--text', `${s} entry`]));
  ok(orch(w, ['set', 'B', 'in_review', '--pr', 'https://example.invalid/pr/1']));
  ok(orch(w, ['set', 'A', 'in_progress', '--reason', 'x']));
  ok(orch(w, ['validate']));
});

test('validate reports two tasks in_progress in one repository, naming both', () => {
  const fx = fixture();
  stackB(fx);
  ok(orch(fx, ['validate']));
  edit(fx, 'A', (t) => t.replace('status: in_review', 'status: in_progress'));
  forge(fx, { at: NOW, task: 'A', event: 'status', from: 'in_review', to: 'in_progress', reason: 'x' });
  refused(orch(fx, ['validate']), `orch: ${PLAN_DIR}: A and B are in_progress in repo umbrella; one builder per repository`);
});

test('ready does not stack across repositories, on two branches, or on a dependency with no branch', () => {
  const fx = fixture();
  edit(fx, 'B', (t) => t.replace('repo: umbrella', 'repo: batondeck'));
  toReview(fx);
  assert.equal(ok(orch(fx, ['ready'])).out, `${readyC}\n`);
  refused(orch(fx, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/widget']), 'orch: B: not in the ready frontier: depends on A, which is in_review');

  const f2 = fixture();
  writeRel(f2, `${TASK_DIR}/E-second-widget.md`, taskText({ id: 'E', title: 'Second widget', release: 'R2', repo: 'umbrella', agent: 'builder' }));
  edit(f2, 'B', (t) => t.replace('depends_on: [A]', 'depends_on: [A, E]'));
  toReview(f2);
  ok(orch(f2, ['claim', 'E', '--by', 'builder-3', '--branch', 'feat/other']));
  for (const s of ['built', 'why', 'gates']) ok(orch(f2, ['log', 'E', '--section', s, '--text', `${s} entry`]));
  ok(orch(f2, ['set', 'E', 'in_review', '--pr', 'https://example.invalid/pr/2']));
  assert.ok(!ok(orch(f2, ['ready'])).out.includes('Wire the widget'));
  refused(orch(f2, ['claim', 'B', '--by', 'builder-2', '--branch', 'feat/widget']), 'orch: B: not in the ready frontier: depends on A on feat/widget and E on feat/other; a task stacks on one branch, wait for done');

  const f3 = fixture();
  edit(f3, 'D', (t) => t.replace('depends_on: []', 'depends_on: [C]').replace('not_before: 2026-10-22', 'not_before: ""'));
  ok(orch(f3, ['claim', 'C', '--by', 'owner']));
  for (const s of ['built', 'why', 'gates']) ok(orch(f3, ['log', 'C', '--section', s, '--text', 'done by the owner']));
  ok(orch(f3, ['set', 'C', 'in_review']));
  assert.ok(!ok(orch(f3, ['ready'])).out.includes('Observe the acceptance'));
});

// ---------------- not-before ----------------
test('not-before sets and clears the date on a todo or blocked task and records the event', () => {
  const fx = fixture();
  const readyD = ['D', 'R1', 'F1', 'none', 'orchestrator', 'M', 'Observe the acceptance'].join('\t');
  assert.ok(!ok(orch(fx, ['ready'])).out.includes(readyD));
  ok(orch(fx, ['not-before', 'D', '2026-10-20']));
  assert.equal(field(fx, 'D', 'not_before'), '2026-10-20');
  assert.ok(ok(orch(fx, ['ready'])).out.split('\n').includes(readyD));
  assert.deepEqual(events(fx).pop(), { at: NOW, task: 'D', event: 'not_before', from: '2026-10-22', to: '2026-10-20' });
  ok(orch(fx, ['not-before', 'D', '-']));
  assert.equal(field(fx, 'D', 'not_before'), '""');
  assert.deepEqual(events(fx).pop(), { at: NOW, task: 'D', event: 'not_before', from: '2026-10-20', to: '' });
  ok(orch(fx, ['validate']));
  for (const bad of ['2026-02-30', '2026-13-01', 'tomorrow']) refused(orch(fx, ['not-before', 'D', bad]), `orch: not-before: "${bad}" is not a date YYYY-MM-DD or "-"`);
  refused(orch(fx, ['not-before', 'D']), 'orch: not-before: usage: not-before <id> <YYYY-MM-DD|->');
  ok(orch(fx, ['set', 'C', 'blocked', '--reason', 'waiting on the owner']));
  ok(orch(fx, ['not-before', 'C', '2026-11-01']));
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']));
  refused(orch(fx, ['not-before', 'A', '2026-11-01']), 'orch: A: not-before is for todo and blocked tasks; A is in_progress');
  assert.equal(field(fx, 'A', 'not_before'), '""');
});

// ---------------- set ----------------
test('set refuses a transition that is not in the table', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'orchestrator']), 'orch: A: in_progress -> done is not a transition (see orch help)');
  refused(orch(fx, ['set', 'B', 'in_progress']), 'orch: B: todo -> in_progress goes through orch claim');
});

test('set in_review refuses without --pr', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'A', '--section', s, '--text', 'entry']));
  refused(orch(fx, ['set', 'A', 'in_review']), `orch: ${taskRel('A')}: in_review needs pr (--pr)`);
  assert.equal(field(fx, 'A', 'status'), 'in_progress');
});

test('set in_review refuses without the built, why and gates log sections', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  ok(orch(fx, ['log', 'A', '--section', 'built', '--text', 'entry']));
  ok(orch(fx, ['log', 'A', '--section', 'why', '--text', 'entry']));
  refused(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1']), `orch: ${taskRel('A')}: in_review needs the log's "### Gates" (orch log --section gates)`);
});

test('set in_review -> in_progress needs --reason and counts a review round', () => {
  const fx = fixture();
  toReview(fx);
  refused(orch(fx, ['set', 'A', 'in_progress']), 'orch: A: in_review -> in_progress is a review round and needs --reason (the findings)');
  ok(orch(fx, ['set', 'A', 'in_progress', '--reason', '2 blocking findings', '--by', 'orchestrator']));
  const last = events(fx).pop();
  assert.deepEqual([last.from, last.to, last.reason], ['in_review', 'in_progress', '2 blocking findings']);
});

test('set merged refuses without --merge-sha', () => {
  const fx = fixture();
  toReview(fx);
  refused(orch(fx, ['set', 'A', 'merged']), `orch: ${taskRel('A')}: merged needs merge_sha (--merge-sha)`);
});

test('set merged refuses a SHA that is on no branch of origin', () => {
  const fx = fixture();
  toReview(fx);
  writeRel(fx, 'local-only.txt', 'not pushed\n');
  git(fx.root, 'add', 'local-only.txt');
  git(fx.root, 'commit', '-q', '-m', 'local only');
  const sha = git(fx.root, 'rev-parse', 'HEAD');
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha]), `orch: A: ${sha} is not on origin/main, the default branch, in the umbrella root; it is on no branch of origin`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
});

test('set merged refuses a SHA pushed only to a feature branch and names that branch', () => {
  const fx = fixture();
  toReview(fx);
  writeRel(fx, 'feature-only.txt', 'on the PR branch\n');
  git(fx.root, 'add', 'feature-only.txt');
  git(fx.root, 'commit', '-q', '-m', 'feature only');
  git(fx.root, 'push', '-q', 'origin', 'HEAD:refs/heads/feat/widget');
  const sha = git(fx.root, 'rev-parse', 'HEAD');
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha]), `orch: A: ${sha} is not on origin/main, the default branch, in the umbrella root; it is on origin/feat/widget`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
});

test('set merged reads the default branch from origin/HEAD', () => {
  const fx = fixture();
  toReview(fx);
  git(fx.root, 'push', '-q', 'origin', `${fx.head}:refs/heads/trunk`);
  writeRel(fx, 'main-only.txt', 'on main, not on trunk\n');
  git(fx.root, 'add', 'main-only.txt');
  git(fx.root, 'commit', '-q', '-m', 'main only');
  git(fx.root, 'push', '-q', 'origin', 'HEAD:refs/heads/main');
  git(fx.root, 'remote', 'set-head', 'origin', 'trunk');
  const sha = git(fx.root, 'rev-parse', 'HEAD');
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha]), `orch: A: ${sha} is not on origin/trunk, the default branch, in the umbrella root; it is on origin/main`);
  git(fx.root, 'checkout', '-q', '-b', 'trunk', fx.head);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', land(fx, 'trunk-work', 'trunk')]));
});

test('claim and set merged refuse when the repository is not checked out', () => {
  const fx = fixture();
  edit(fx, 'A', (t) => t.replace('repo: umbrella', 'repo: batondeck'));
  refused(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']), "orch: A: repo batondeck is not checked out at batondeck/; cannot read the tip of its origin's default branch");
  assert.equal(field(fx, 'A', 'status'), 'todo');
  edit(fx, 'A', (t) => t.replace('repo: batondeck', 'repo: umbrella'));
  toReview(fx);
  edit(fx, 'A', (t) => t.replace('repo: umbrella', 'repo: batondeck'));
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', fx.head]), held(taskRel('A'), 'repo', 'batondeck', 'umbrella'));
  // Forged into both files, the repository is still read from origin, and a missing checkout is refused.
  forge(fx, { at: '2026-10-21T09:30:00Z', task: 'A', event: 'backfill', repo: 'batondeck' });
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', fx.head]), `orch: A: repo batondeck is not checked out at batondeck/; cannot check ${fx.head} on its origin`);
});

test('set refuses merged -> blocked, so a merged task never returns to the frontier with its old merge SHA', () => {
  const fx = fixture();
  const sha = toMerged(fx);
  refused(orch(fx, ['set', 'A', 'blocked', '--reason', 'x']), 'orch: A: merged -> blocked is not a transition (see orch help)');
  refused(orch(fx, ['set', 'A', 'todo']), 'orch: A: merged -> todo is not a transition (see orch help)');
  for (const [k, v] of [['status', 'merged'], ['claimed_by', 'builder-1'], ['merge_sha', sha]]) assert.equal(field(fx, 'A', k), v, k);
  assert.deepEqual(ok(orch(fx, ['ready'])).out.split('\n').filter((l) => l.startsWith('A\t')), []);
});

test('set in_review and set merged need their flag every time; a populated field never stands in for it', () => {
  const fx = fixture();
  toReview(fx);
  ok(orch(fx, ['set', 'A', 'in_progress', '--reason', '1 finding']));
  refused(orch(fx, ['set', 'A', 'in_review']), `orch: ${taskRel('A')}: in_review needs pr (--pr)`);
  assert.equal(field(fx, 'A', 'status'), 'in_progress');
  assert.equal(field(fx, 'A', 'pr'), 'https://example.invalid/pr/1');
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/2']));
  edit(fx, 'A', (t) => t.replace('merge_sha: ""', `merge_sha: ${fx.head}`));
  refused(orch(fx, ['set', 'A', 'merged']), held(taskRel('A'), 'merge_sha', fx.head, ''));
  assert.equal(field(fx, 'A', 'status'), 'in_review');
});

// A hand edit of the task file alone is refused by validate against metrics.jsonl. One that forges both files
// consistently passes validate; done still re-checks the SHA on origin and against the claim.
test('set done re-checks the merge SHA on origin: a forgery of the file and the log together is refused at done', () => {
  const fx = fixture();
  toReview(fx);
  for (const s of ['deviations', 'review']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  edit(fx, 'A', (t) => t.replace('status: in_review', 'status: merged').replace('merge_sha: ""', 'merge_sha: deadbeefcafe'));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: status is "merged"; the events in metrics.jsonl give "in_review"`);
  forge(fx, { at: NOW, task: 'A', event: 'status', from: 'in_review', to: 'merged', merge_sha: 'deadbeefcafe' });
  ok(orch(fx, ['log', 'A', '--section', 'observed', '--text', 'observed']));
  edit(fx, 'A', tick);
  ok(orch(fx, ['validate']));
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']), 'orch: A: deadbeefcafe is not a commit known in the umbrella root');
  assert.equal(field(fx, 'A', 'status'), 'merged');
  writeRel(fx, 'redo-only.txt', 'on the redo branch, never merged\n');
  git(fx.root, 'add', 'redo-only.txt');
  git(fx.root, 'commit', '-q', '-m', 'redo only');
  git(fx.root, 'push', '-q', 'origin', 'HEAD:refs/heads/feat/redo');
  const sha = git(fx.root, 'rev-parse', 'HEAD');
  edit(fx, 'A', (t) => t.replace('merge_sha: deadbeefcafe', `merge_sha: ${sha}`));
  forge(fx, { at: NOW, task: 'A', event: 'status', from: 'in_review', to: 'merged', merge_sha: sha });
  ok(orch(fx, ['validate']));
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']), `orch: A: ${sha} is not on origin/main, the default branch, in the umbrella root; it is on origin/feat/redo`);
  assert.equal(field(fx, 'A', 'status'), 'merged');
});

test('set done refuses a ticked acceptance criterion with empty evidence (review B4 on PR #54)', () => {
  const fx = fixture();
  toMerged(fx);
  edit(fx, 'A', (t) => t.replace(/^- \[ \] (AC\d .* — evidence:)$/gm, '- [x] $1'));
  assert.ok(readRel(fx, taskRel('A')).includes('- [x] AC2 `orch validate` prints valid — evidence:\n'));
  // set runs validate's checks on the file first, so validate's own line refuses it before the done checks.
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']), `orch: ${taskRel('A')}: AC1 is ticked without evidence`);
  assert.equal(field(fx, 'A', 'status'), 'merged');
});

test('set merged refuses a merge SHA that predates the claim, after a reopen (review B5 on PR #54)', () => {
  const fx = fixture();
  toReview(fx);
  ok(orch(fx, ['set', 'A', 'blocked', '--reason', 'x']));
  ok(orch(fx, ['set', 'A', 'todo']));
  const other = land(fx, 'someone-else');
  ok(orch(fx, ['claim', 'A', '--by', 'builder-2', '--branch', 'feat/redo']));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/2']));
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', fx.head]),
    `orch: A: ${fx.head} is already in base_sha ${other}, origin/main's tip when A was claimed; the merge predates the work`);
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', other]),
    `orch: A: ${other} is already in base_sha ${other}, origin/main's tip when A was claimed; the merge predates the work`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
  const work = land(fx, 'the-work');
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', work]));
  assert.equal(field(fx, 'A', 'merge_sha'), work);
});

test('set done re-checks the merge SHA against the claim: an older SHA forged in the file and the log is refused at done', () => {
  const fx = fixture();
  const work = toMerged(fx);
  edit(fx, 'A', (t) => tick(t).replace(`merge_sha: ${work}`, `merge_sha: ${fx.head}`));
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: merge_sha is "${fx.head}"; the events in metrics.jsonl give "${work}"`);
  forge(fx, { at: NOW, task: 'A', event: 'status', from: 'in_review', to: 'merged', merge_sha: fx.head });
  ok(orch(fx, ['validate']));
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']),
    `orch: A: ${fx.head} is already in base_sha ${fx.head}, origin/main's tip when A was claimed; the merge predates the work`);
  assert.equal(field(fx, 'A', 'status'), 'merged');
  edit(fx, 'A', (t) => t.replace(`merge_sha: ${fx.head}`, `merge_sha: ${work}`));
  forge(fx, { at: NOW, task: 'A', event: 'status', from: 'in_review', to: 'merged', merge_sha: work });
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']));
});

test('claim records origin\'s tip as base_sha; blocked -> todo clears the claim, so a reopened task carries nothing from it', () => {
  const fx = fixture();
  toReview(fx);
  assert.equal(field(fx, 'A', 'base_sha'), fx.head);
  assert.equal(events(fx).find((e) => e.to === 'in_progress').base_sha, fx.head);
  ok(orch(fx, ['set', 'A', 'blocked', '--reason', 'x']));
  ok(orch(fx, ['set', 'A', 'todo']));
  for (const k of ['claimed_by', 'branch', 'pr', 'started_at', 'base_sha', 'blocked_reason']) assert.equal(field(fx, 'A', k), '""', k);
  ok(orch(fx, ['validate']));
  const tip = land(fx, 'later');
  ok(orch(fx, ['claim', 'A', '--by', 'builder-2', '--branch', 'feat/redo']));
  assert.deepEqual(['claimed_by', 'branch', 'base_sha'].map((k) => field(fx, 'A', k)), ['builder-2', 'feat/redo', tip]);
});

// ---- P-8: a repository whose origin has no commits yet ----
const EMPTY = '0'.repeat(40);
// Points the fixture at a new bare origin with no refs. remote remove drops the old tracking refs, so a stale
// refs/remotes/origin/main from the golden copy cannot stand in for the tip.
function freshOrigin(fx) {
  const origin = tempDir('orch-empty-origin-');
  git(origin, 'init', '-q', '--bare', '-b', 'main');
  git(fx.root, 'remote', 'remove', 'origin');
  git(fx.root, 'remote', 'add', 'origin', origin);
  return origin;
}

test('claim on an origin with no commits records forty zeros and empty_origin; one with branches but not main is still refused (P-8)', () => {
  const fx = fixture();
  freshOrigin(fx);
  git(fx.root, 'push', '-q', 'origin', `${fx.head}:refs/heads/develop`);
  refused(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']), 'orch: A: origin/main does not resolve in the umbrella root');
  assert.equal(field(fx, 'A', 'status'), 'todo');
  freshOrigin(fx);
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']));
  assert.equal(field(fx, 'A', 'base_sha'), EMPTY);
  const ev = events(fx).find((e) => e.to === 'in_progress');
  assert.equal(ev.base_sha, EMPTY);
  assert.equal(ev.empty_origin, true);
  ok(orch(fx, ['validate']));
});

test('validate accepts forty zeros as base_sha only from a claim event that recorded empty_origin (P-8)', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']));
  edit(fx, 'A', (t) => setKey(t, 'base_sha', EMPTY));
  forge(fx, { at: NOW, task: 'A', event: 'backfill', base_sha: EMPTY });
  refused(orch(fx, ['validate']), `orch: ${taskRel('A')}: base_sha is forty zeros, the empty-origin base, but no claim event recorded empty_origin`);
});

test('after the first push to an empty origin, merged and done take a SHA on main and refuse one only on another branch (P-8)', () => {
  const fx = fixture();
  freshOrigin(fx);
  toReview(fx);
  assert.equal(field(fx, 'A', 'base_sha'), EMPTY);
  git(fx.root, 'push', '-q', 'origin', `${fx.head}:refs/heads/main`);
  writeRel(fx, 'feature-only.txt', 'on the PR branch\n');
  git(fx.root, 'add', 'feature-only.txt');
  git(fx.root, 'commit', '-q', '-m', 'feature only');
  git(fx.root, 'push', '-q', 'origin', 'HEAD:refs/heads/feat/widget');
  const side = git(fx.root, 'rev-parse', 'HEAD');
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', side]), `orch: A: ${side} is not on origin/main, the default branch, in the umbrella root; it is on origin/feat/widget`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', fx.head]));
  for (const s of ['deviations', 'review', 'observed']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  edit(fx, 'A', tick);
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']));
  assert.equal(field(fx, 'A', 'status'), 'done');
});

test('claim and set merged refuse a checkout with no origin remote, naming the remote (P-8)', () => {
  const fx = fixture();
  git(fx.root, 'remote', 'remove', 'origin');
  refused(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']),
    "orch: A: repo umbrella has no origin remote at the umbrella root; cannot read the tip of its origin's default branch");
  assert.equal(field(fx, 'A', 'status'), 'todo');
  git(fx.root, 'remote', 'add', 'origin', fx.origin);
  toReview(fx);
  git(fx.root, 'remote', 'remove', 'origin');
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', fx.head]),
    `orch: A: repo umbrella has no origin remote at the umbrella root; cannot check ${fx.head} on its origin`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
});
test('set done refuses without evidence on every acceptance criterion', () => {
  const fx = fixture();
  toMerged(fx);
  const r = orch(fx, ['set', 'A', 'done', '--observed-by', 'orchestrator']);
  refused(r, `orch: ${taskRel('A')}: done needs AC1 ticked with its evidence`);
  assert.equal(field(fx, 'A', 'status'), 'merged');
});

test('set done refuses without an observer', () => {
  const fx = fixture();
  toMerged(fx);
  edit(fx, 'A', tick);
  refused(orch(fx, ['set', 'A', 'done']), `orch: ${taskRel('A')}: done needs observed_by (--observed-by)`);
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'builder-1']), `orch: ${taskRel('A')}: observed_by "builder-1" is the claimer; the observer must be someone else`);
  assert.equal(field(fx, 'A', 'status'), 'merged');
});

test('set done refuses without the six log sections', () => {
  const fx = fixture();
  toReview(fx);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', land(fx)]));
  edit(fx, 'A', tick);
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'orchestrator']), `orch: ${taskRel('A')}: done needs the log's "### Observed" (orch log --section observed)`);
});

test('set blocked and dropped need --reason; blocked -> todo clears the reason', () => {
  const fx = fixture();
  refused(orch(fx, ['set', 'C', 'blocked']), `orch: ${taskRel('C')}: blocked needs blocked_reason (--reason)`);
  ok(orch(fx, ['set', 'C', 'blocked', '--reason', 'waits on Z-07']));
  assert.equal(field(fx, 'C', 'blocked_reason'), 'waits on Z-07');
  ok(orch(fx, ['set', 'C', 'todo']));
  assert.equal(field(fx, 'C', 'blocked_reason'), '""');
  refused(orch(fx, ['set', 'D', 'dropped']), "orch: D: dropped needs --reason (the order's words)");
  ok(orch(fx, ['set', 'D', 'dropped', '--reason', 'owner: not needed', '--by', 'owner']));
  assert.match(readRel(fx, taskRel('D')), /### Deviations\n\n?2026-10-21T09:00:00Z Dropped by owner: owner: not needed\n/);
});

test('an owner task (repo none) goes in_progress -> in_review -> merged -> done with no branch, PR or SHA', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'C', '--by', 'owner']));
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'C', '--section', s, '--text', `${s}: the owner ran the release`]));
  ok(orch(fx, ['set', 'C', 'in_review']));
  ok(orch(fx, ['set', 'C', 'merged']));
  for (const s of ['deviations', 'review', 'observed']) ok(orch(fx, ['log', 'C', '--section', s, '--text', 'none']));
  edit(fx, 'C', tick);
  refused(orch(fx, ['set', 'C', 'done', '--observed-by', 'owner']), `orch: ${taskRel('C')}: observed_by "owner" is the claimer; the observer must be someone else`);
  ok(orch(fx, ['set', 'C', 'done', '--observed-by', 'orchestrator']));
  for (const [k, v] of [['status', 'done'], ['branch', '""'], ['pr', '""'], ['merge_sha', '""'], ['base_sha', '""']]) assert.equal(field(fx, 'C', k), v, k);
  ok(orch(fx, ['validate']));
});

// ---------------- every field the tool writes, held to metrics.jsonl (review round 4 on PR #54) ----------------

test('the reviewer\'s repro: a hand-edited base_sha is refused by validate, at merged and at done', () => {
  const fx = fixture();
  const A = taskRel('A');
  const old = land(fx, 'old-work');
  toReview(fx);
  assert.equal(field(fx, 'A', 'base_sha'), old);
  assert.equal(events(fx).find((e) => e.to === 'in_progress').base_sha, old);
  edit(fx, 'A', (t) => setKey(t, 'base_sha', fx.head));
  refused(orch(fx, ['validate']), held(A, 'base_sha', fx.head, old));
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', old]), held(A, 'base_sha', fx.head, old));
  assert.equal(field(fx, 'A', 'status'), 'in_review');
  edit(fx, 'A', (t) => setKey(t, 'base_sha', old));
  for (const s of ['deviations', 'review']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  const work = land(fx, 'the-work');
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', work]));
  ok(orch(fx, ['log', 'A', '--section', 'observed', '--text', 'observed']));
  edit(fx, 'A', (t) => setKey(setKey(tick(t), 'base_sha', fx.head), 'merge_sha', old));
  const r = orch(fx, ['set', 'A', 'done', '--observed-by', 'rev']);
  refused(r, held(A, 'base_sha', fx.head, old));
  refused(r, held(A, 'merge_sha', old, work));
  assert.equal(field(fx, 'A', 'status'), 'merged');
});

test('a hand-edited claimed_by or observed_by is refused, so the claimer cannot observe its own work', () => {
  const fx = fixture();
  const A = taskRel('A');
  toMerged(fx);
  edit(fx, 'A', (t) => setKey(tick(t), 'claimed_by', 'someone-else'));
  refused(orch(fx, ['validate']), held(A, 'claimed_by', 'someone-else', 'builder-1'));
  refused(orch(fx, ['set', 'A', 'done', '--observed-by', 'builder-1']), held(A, 'claimed_by', 'someone-else', 'builder-1'));
  assert.equal(field(fx, 'A', 'status'), 'merged');
  edit(fx, 'A', (t) => setKey(t, 'claimed_by', 'builder-1'));
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']));
  assert.equal(events(fx).pop().observed_by, 'reviewer');
  edit(fx, 'A', (t) => setKey(t, 'observed_by', 'builder-2'));
  refused(orch(fx, ['validate']), held(A, 'observed_by', 'builder-2', 'reviewer'));
});

test('set runs every validate check first: a 7-hex base_sha that validate refuses is refused at set', () => {
  const fx = fixture();
  toReview(fx);
  const work = land(fx);
  const short = fx.head.slice(0, 7);
  edit(fx, 'A', (t) => setKey(t, 'base_sha', short));
  refused(orch(fx, ['set', 'A', 'merged', '--merge-sha', work]), `orch: ${taskRel('A')}: base_sha "${short}" is not a full hex SHA`);
  assert.equal(field(fx, 'A', 'status'), 'in_review');
});

test('a hand-edited status is refused by validate; claim and not-before refuse a hand-edited file', () => {
  const fx = fixture();
  const A = taskRel('A');
  edit(fx, 'A', (t) => setKey(setKey(t, 'status', 'blocked'), 'blocked_reason', 'waits'));
  const r = orch(fx, ['validate']);
  refused(r, held(A, 'status', 'blocked', 'todo'));
  refused(r, held(A, 'blocked_reason', 'waits', ''));
  refused(orch(fx, ['not-before', 'A', '2026-11-01']), held(A, 'status', 'blocked', 'todo'));
  assert.equal(field(fx, 'A', 'not_before'), '""');
  edit(fx, 'A', (t) => setKey(setKey(setKey(t, 'status', 'todo'), 'blocked_reason', ''), 'claimed_by', 'ghost'));
  refused(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']), held(A, 'claimed_by', 'ghost', ''));
  assert.equal(field(fx, 'A', 'status'), 'todo');
  edit(fx, 'A', (t) => setKey(t, 'claimed_by', ''));
  toReview(fx);
  for (const s of ['deviations', 'review']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  const work = land(fx);
  edit(fx, 'A', (t) => setKey(setKey(t, 'status', 'merged'), 'merge_sha', work));
  const m = orch(fx, ['validate']);
  refused(m, held(A, 'status', 'merged', 'in_review'));
  refused(m, held(A, 'merge_sha', work, ''));
  // Fields the command itself rewrites: only the check before acting sees the hand edit.
  edit(fx, 'D', (t) => setKey(t, 'not_before', '2026-10-25'));
  refused(orch(fx, ['not-before', 'D', '2026-10-30']), held(taskRel('D'), 'not_before', '2026-10-25', '2026-10-22'));
  assert.equal(field(fx, 'D', 'not_before'), '2026-10-25');
});

test('an owner task whose status is hand-edited to merged cannot reach done', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'C', '--by', 'owner']));
  for (const s of SIX) ok(orch(fx, ['log', 'C', '--section', s, '--text', 'none']));
  ok(orch(fx, ['set', 'C', 'in_review']));
  edit(fx, 'C', (t) => setKey(tick(t), 'status', 'merged'));
  refused(orch(fx, ['set', 'C', 'done', '--observed-by', 'orchestrator']), held(taskRel('C'), 'status', 'merged', 'in_review'));
  assert.equal(field(fx, 'C', 'status'), 'merged');
});

test('every field the tool writes is held to the events: one hand edit each is refused by validate', () => {
  const fx = fixture();
  const work = toMerged(fx);
  edit(fx, 'A', tick);
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'reviewer']));
  ok(orch(fx, ['set', 'C', 'blocked', '--reason', 'waits on Z-07']));
  const P = ok(orch(fx, ['discover', 'A', '--title', 'Cache the widget'])).out.trim();
  ok(orch(fx, ['validate']));
  const cases = [
    ['A', 'status', 'done', 'merged'],
    ['A', 'claimed_by', 'builder-1', 'builder-9'],
    ['A', 'branch', 'feat/widget', 'feat/other'],
    ['A', 'pr', 'https://example.invalid/pr/1', 'https://example.invalid/pr/9'],
    ['A', 'merge_sha', work, fx.head],
    ['A', 'observed_by', 'reviewer', 'someone'],
    ['A', 'started_at', NOW, '2026-10-21T08:00:00Z'],
    ['A', 'base_sha', fx.head, work],
    ['A', 'finished_at', NOW, '2026-10-21T10:00:00Z'],
    ['C', 'blocked_reason', 'waits on Z-07', 'something else'],
    ['D', 'not_before', '2026-10-22', '2026-10-23'],
    [P, 'discovered_from', 'A', 'B'],
    ['B', 'started_at', '', NOW],
  ];
  for (const [id, k, have, to] of cases) {
    const rel = id.includes('/') ? id : taskRel(id);
    const before = readRel(fx, rel);
    assert.ok(new RegExp(`^${k}: ${have === '' ? '""' : have.replace(/[.?]/g, '\\$&')}$`, 'm').test(before), `${rel} holds ${k}: ${have}`);
    writeRel(fx, rel, setKey(before, k, to));
    refused(orch(fx, ['validate']), held(rel, k, to, have));
    writeRel(fx, rel, before);
  }
  ok(orch(fx, ['validate']));
});

test('a claim event older than base_sha is read as it is; a backfill event sets the fields it names (O1-O3)', () => {
  const fx = fixture();
  toReview(fx);
  const log = `${PLAN_DIR}/metrics.jsonl`;
  const strip = (l) => { const e = JSON.parse(l); delete e.base_sha; return JSON.stringify(e); };
  writeRel(fx, log, readRel(fx, log).split('\n').map((l) => (l.includes('"to":"in_progress"') ? strip(l) : l)).join('\n'));
  assert.equal(events(fx).find((e) => e.to === 'in_progress').base_sha, undefined);
  refused(orch(fx, ['validate']), held(taskRel('A'), 'base_sha', fx.head, ''));
  forge(fx, { at: '2026-10-21T11:59:00Z', task: 'A', event: 'backfill', base_sha: fx.head });
  ok(orch(fx, ['validate']));
  assert.ok(ok(orch(fx, ['resume'], '2026-10-21T12:00:00Z')).out.includes(' stale (no event for 3h)'), 'a backfill is not work on the task');
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', land(fx)]));
  ok(orch(fx, ['validate']));
});

// ---------------- repo and agent held to the claim event (review round 5 on PR #54) ----------------

test('the reviewer\'s repro (round 5): a claimed task hand-edited to repo none, agent orchestrator is refused at validate and at set', () => {
  const fx = fixture();
  const A = taskRel('A');
  toReview(fx);
  edit(fx, 'A', (t) => setKey(setKey(t, 'repo', 'none'), 'agent', 'orchestrator'));
  const v = orch(fx, ['validate']);
  refused(v, held(A, 'repo', 'none', 'umbrella'));
  refused(v, held(A, 'agent', 'orchestrator', 'builder'));
  for (const s of ['deviations', 'review', 'observed']) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} entry`]));
  edit(fx, 'A', tick);
  refused(orch(fx, ['set', 'A', 'merged']), held(A, 'repo', 'none', 'umbrella'));
  assert.equal(field(fx, 'A', 'status'), 'in_review');
  assert.equal(events(fx).filter((e) => e.task === 'A' && e.to === 'merged').length, 0);
});

test('the claim event records repo and agent; they are free before the claim and held from it', () => {
  const fx = fixture();
  const A = taskRel('A');
  edit(fx, 'B', (t) => setKey(t, 'agent', 'cheap'));
  ok(orch(fx, ['validate']));
  toReview(fx);
  const claim = events(fx).find((e) => e.task === 'A' && e.to === 'in_progress');
  assert.equal(claim.repo, 'umbrella');
  assert.equal(claim.agent, 'builder');
  edit(fx, 'A', (t) => setKey(t, 'agent', 'cheap'));
  refused(orch(fx, ['validate']), held(A, 'agent', 'cheap', 'builder'));
  edit(fx, 'A', (t) => setKey(setKey(t, 'agent', 'builder'), 'repo', 'batondeck'));
  refused(orch(fx, ['validate']), held(A, 'repo', 'batondeck', 'umbrella'));
  edit(fx, 'A', (t) => setKey(t, 'repo', 'umbrella'));
  ok(orch(fx, ['validate']));
});

test('blocked -> todo frees repo and agent with the rest of the claim; the next claim records the new values', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget']));
  ok(orch(fx, ['set', 'A', 'blocked', '--reason', 'waits']));
  ok(orch(fx, ['set', 'A', 'todo']));
  edit(fx, 'A', (t) => setKey(t, 'agent', 'cheap'));
  ok(orch(fx, ['validate']));
  ok(orch(fx, ['claim', 'A', '--by', 'cheap-1', '--branch', 'feat/widget']));
  assert.equal(events(fx).filter((e) => e.task === 'A' && e.to === 'in_progress').pop().agent, 'cheap');
  ok(orch(fx, ['validate']));
});

test('a claim event older than repo and agent is read as it is; a backfill naming them holds them (O1-O3)', () => {
  const fx = fixture();
  toReview(fx);
  const log = `${PLAN_DIR}/metrics.jsonl`;
  const strip = (l) => { const e = JSON.parse(l); delete e.repo; delete e.agent; return JSON.stringify(e); };
  writeRel(fx, log, readRel(fx, log).split('\n').map((l) => (l.includes('"to":"in_progress"') ? strip(l) : l)).join('\n'));
  const r = orch(fx, ['validate']);
  refused(r, held(taskRel('A'), 'repo', 'umbrella', ''));
  refused(r, held(taskRel('A'), 'agent', 'builder', ''));
  forge(fx, { at: '2026-10-21T11:59:00Z', task: 'A', event: 'backfill', repo: 'umbrella', agent: 'builder' });
  ok(orch(fx, ['validate']));
  edit(fx, 'A', (t) => setKey(t, 'agent', 'cheap'));
  refused(orch(fx, ['validate']), held(taskRel('A'), 'agent', 'cheap', 'builder'));
});

test('repo none carries no branch, pr, base_sha or merge_sha: validate, claim and set refuse one', () => {
  const fx = fixture();
  const C = taskRel('C');
  refused(orch(fx, ['claim', 'C', '--by', 'owner', '--branch', 'feat/c']), 'orch: --branch: repo none carries no branch');
  assert.equal(field(fx, 'C', 'status'), 'todo');
  ok(orch(fx, ['claim', 'C', '--by', 'owner']));
  for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', 'C', '--section', s, '--text', `${s}: the owner ran the release`]));
  refused(orch(fx, ['set', 'C', 'in_review', '--pr', 'https://example.invalid/pr/2']), `orch: ${C}: repo none carries no pr; it is "https://example.invalid/pr/2"`);
  assert.equal(field(fx, 'C', 'status'), 'in_progress');
  ok(orch(fx, ['validate']));
  // Each field forged into both files at once, so the hold to the events agrees and only the repo-none check refuses.
  const log = `${PLAN_DIR}/metrics.jsonl`;
  for (const [k, v] of [['branch', 'feat/c'], ['pr', 'https://example.invalid/pr/3'], ['base_sha', fx.head], ['merge_sha', fx.head]]) {
    const before = { task: readRel(fx, C), log: readRel(fx, log) };
    forge(fx, { at: '2026-10-21T09:30:00Z', task: 'C', event: 'backfill', [k]: v });
    edit(fx, 'C', (t) => setKey(t, k, v));
    const r = orch(fx, ['validate']);
    refused(r, `orch: ${C}: repo none carries no ${k}; it is ${JSON.stringify(v)}`);
    assert.ok(!r.err.includes('the events in metrics.jsonl give'), `only the repo-none check refuses ${k}:\n${r.err}`);
    writeRel(fx, C, before.task);
    writeRel(fx, log, before.log);
  }
  ok(orch(fx, ['validate']));
});

// ---------------- the happy path ----------------
test('happy path: claim, six log sections, in_review, a review round, merged on origin, done, then B is ready', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget'], '2026-10-21T09:00:00Z'));
  for (const s of SIX) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s} for A`], '2026-10-21T10:00:00Z'));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1'], '2026-10-21T10:30:00Z'));
  ok(orch(fx, ['set', 'A', 'in_progress', '--reason', '1 finding'], '2026-10-21T11:00:00Z'));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1'], '2026-10-21T11:30:00Z'));
  const sha = land(fx);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha], '2026-10-21T12:00:00Z'));
  edit(fx, 'A', tick);
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'orchestrator'], '2026-10-21T13:00:00Z'));
  const text = readRel(fx, taskRel('A'));
  for (const [k, v] of [['status', 'done'], ['claimed_by', 'builder-1'], ['branch', 'feat/widget'], ['pr', 'https://example.invalid/pr/1'],
    ['merge_sha', sha], ['observed_by', 'orchestrator'], ['started_at', '2026-10-21T09:00:00Z'], ['finished_at', '2026-10-21T13:00:00Z']]) {
    assert.equal(field(fx, 'A', k), v, k);
  }
  assert.match(text, /### Built\n2026-10-21T10:00:00Z built for A\n/);
  assert.equal(ok(orch(fx, ['validate'], '2026-10-21T13:00:00Z')).out, 'orch: valid: 4 tasks, 1 features\n');
  assert.ok(ok(orch(fx, ['ready'], '2026-10-21T13:00:00Z')).out.split('\n').includes(readyB));
  const ev = events(fx);
  assert.deepEqual(ev.filter((e) => e.event === 'status').map((e) => e.to), ['in_progress', 'in_review', 'in_progress', 'in_review', 'merged', 'done']);
  assert.equal(ev.filter((e) => e.event === 'log').length, 6);
  refused(orch(fx, ['log', 'A', '--section', 'built', '--text', 'late']), 'orch: A: the log is written only in_progress, in_review or merged; the task is done');
});

test('log refuses a task that is not in progress, a bad section and heading text', () => {
  const fx = fixture();
  refused(orch(fx, ['log', 'A', '--section', 'built', '--text', 'x']), 'orch: A: the log is written only in_progress, in_review or merged; the task is todo');
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/a']));
  refused(orch(fx, ['log', 'A', '--section', 'notes', '--text', 'x']), 'orch: --section: must be one of built, why, deviations, gates, review, observed');
  refused(orch(fx, ['log', 'A', '--section', 'built', '--text', 'ok\n## Status\ndone']), 'orch: A: the log text must not contain a heading or a --- line');
});

// ---------------- brief ----------------
test('brief prints the agent body, plan CONTEXT, feature CONTEXT, the task without its log, then the write-back block', () => {
  const fx = fixture();
  const r = ok(orch(fx, ['brief', 'A']));
  const order = ['BUILDER-DEFINITION', `<!-- orch brief: ${PLAN_DIR}/CONTEXT.md -->`, `<!-- orch brief: ${PLAN_DIR}/F1-widget/CONTEXT.md -->`,
    `<!-- orch brief: ${taskRel('A')} -->`, '## Write-back'].map((s) => r.out.indexOf(s));
  assert.ok(order.every((i) => i >= 0), `all parts present: ${order}`);
  assert.deepEqual([...order].sort((a, b) => a - b), order, 'parts in order');
  assert.ok(!r.out.includes('name: builder') && !r.out.includes('autoCompactWindow'), 'the agent front matter is left out');
  assert.ok(!r.out.includes('## Execution log') && !r.out.includes('### Built'), 'the log is left out');
  assert.ok(r.out.includes(`log A --section <built|why|deviations|gates|review|observed> --text "…"`));
  assert.match(r.err, /^orch: brief A: \d+ words\n$/);
  const owner = ok(orch(fx, ['brief', 'C']));
  assert.ok(!owner.out.includes('BUILDER-DEFINITION') && !owner.out.includes('## Write-back') && owner.out.includes('# C Approve the release'));
});

test('brief refuses a brief over 4,000 words', () => {
  const fx = fixture();
  fs.appendFileSync(path.join(fx.root, '.claude/agents/builder.md'), `${'word '.repeat(4000)}\n`);
  const r = orch(fx, ['brief', 'A']);
  assert.equal(r.out, '');
  assert.equal(r.code, 1);
  assert.match(r.err, /^orch: A: the brief is \d+ words, over the 4000-word budget\n$/);
});

// ---------------- discover and new ----------------
test('discover makes a proposed task that ready ignores and that only the owner turns into todo', () => {
  const fx = fixture();
  const rel = ok(orch(fx, ['discover', 'A', '--title', 'Cache the widget'])).out.trim();
  assert.equal(rel, `${TASK_DIR}/P-1-cache-the-widget.md`);
  const text = readRel(fx, rel);
  for (const l of ['id: P-1', 'status: proposed', 'discovered_from: A', 'feature: F1', 'release: R1', 'repo: umbrella', 'agent: builder', '# P-1 Cache the widget']) {
    assert.ok(text.split('\n').includes(l), l);
  }
  assert.ok(!ok(orch(fx, ['ready'])).out.includes('P-1'));
  assert.equal(ok(orch(fx, ['validate'])).out, 'orch: valid: 5 tasks, 1 features\n');
  refused(orch(fx, ['set', 'P-1', 'todo']), "orch: P-1: proposed -> todo needs an owner's ruling (--by owner)");
  refused(orch(fx, ['set', 'P-1', 'todo', '--by', 'owner']), `orch: ${rel}: ## Why is empty or still the template's placeholder`);
  assert.ok(readRel(fx, rel).split('\n').includes('status: proposed'));
  let filled = text;
  for (const [h, c] of Object.entries(BODY)) filled = setSection(filled, h, c);
  writeRel(fx, rel, filled.replace('- [ ] AC2 `orch validate` prints valid — evidence:', '- AC2 orch validate prints valid'));
  refused(orch(fx, ['set', 'P-1', 'todo', '--by', 'owner']), `orch: ${rel}: acceptance line 2 does not read "- [ ] ACn … — evidence: …": - AC2 orch validate prints valid`);
  writeRel(fx, rel, filled);
  ok(orch(fx, ['set', 'P-1', 'todo', '--by', 'owner']));
  assert.match(ok(orch(fx, ['ready'])).out, /^P-1\tR1\t/m);
  assert.equal(ok(orch(fx, ['discover', 'A', '--title', 'Second'])).out.trim(), `${TASK_DIR}/P-2-second.md`);
});

test('new scaffolds a todo task and refuses a duplicate id or an unknown dependency', () => {
  const fx = fixture();
  const rel = ok(orch(fx, ['new', 'E', '--feature', 'F1', '--release', 'R2', '--title', 'Extra task', '--repo', 'umbrella', '--depends-on', 'A,C', '--zones', 'Z2'])).out.trim();
  assert.equal(rel, `${TASK_DIR}/E-extra-task.md`);
  const text = readRel(fx, rel);
  for (const l of ['status: todo', 'depends_on: [A, C]', 'zones: [Z2]', 'next: ""', '# E Extra task']) assert.ok(text.split('\n').includes(l), l);
  refused(orch(fx, ['new', 'E', '--feature', 'F1', '--release', 'R2', '--title', 'Again', '--repo', 'umbrella']), `orch: E: already exists: ${rel}`);
  refused(orch(fx, ['new', 'G', '--feature', 'F1', '--release', 'R2', '--title', 'G', '--repo', 'umbrella', '--depends-on', 'Q']), 'orch: --depends-on Q: no such task');
});

// ---------------- index, graph, metrics, resume ----------------
test('index and graph write every task', () => {
  const fx = fixture();
  ok(orch(fx, ['set', 'C', 'blocked', '--reason', 'waits on Z-07']));
  assert.equal(ok(orch(fx, ['index'])).out, `${PLAN_DIR}/INDEX.md\n${PLAN_DIR}/GRAPH.md\n`);
  const index = readRel(fx, `${PLAN_DIR}/INDEX.md`);
  const graph = readRel(fx, `${PLAN_DIR}/GRAPH.md`);
  assert.ok(index.startsWith('Generated by `orch index` at 2026-10-21T09:00:00Z.'));
  assert.ok(graph.startsWith('Generated by `orch index` at 2026-10-21T09:00:00Z.'));
  for (const t of TASKS) {
    assert.ok(index.includes(`| ${t.id} | ${t.title} |`), `index has ${t.id}`);
    assert.ok(graph.includes(`  ${t.id}["${t.id} ${t.title}"]`), `graph has ${t.id}`);
  }
  assert.ok(index.includes('## Ready now\n- A (R1, F1, umbrella, builder) Build the widget\n'));
  assert.ok(index.includes('| C | Approve the release | R2 | waits on Z-07 |'));
  assert.ok(graph.includes('flowchart LR') && graph.includes('  A --> B') && graph.includes('  class A,B,D todo') && graph.includes('  class C blocked'));
  fs.rmSync(path.join(fx.root, PLAN_DIR, 'GRAPH.md'));
  assert.equal(ok(orch(fx, ['graph'])).out, `${PLAN_DIR}/GRAPH.md\n`);
  assert.ok(readRel(fx, `${PLAN_DIR}/GRAPH.md`).startsWith('Generated by `orch graph`'));
});

test('metrics counts tasks, cycle time, review rounds, gates, hours blocked and owner wait', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget'], '2026-10-21T09:00:00Z'));
  for (const s of SIX) ok(orch(fx, ['log', 'A', '--section', s, '--text', `${s}`], '2026-10-21T10:00:00Z'));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1'], '2026-10-21T10:30:00Z'));
  ok(orch(fx, ['set', 'A', 'in_progress', '--reason', '1 finding'], '2026-10-21T11:00:00Z'));
  ok(orch(fx, ['set', 'A', 'in_review', '--pr', 'https://example.invalid/pr/1'], '2026-10-21T11:30:00Z'));
  const sha = land(fx);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha], '2026-10-21T12:00:00Z'));
  edit(fx, 'A', tick);
  ok(orch(fx, ['set', 'A', 'done', '--observed-by', 'orchestrator'], '2026-10-21T13:00:00Z'));
  ok(orch(fx, ['set', 'B', 'blocked', '--reason', 'waits'], '2026-10-21T13:00:00Z'));
  ok(orch(fx, ['set', 'B', 'todo'], '2026-10-21T15:00:00Z'));
  const out = ok(orch(fx, ['metrics'], '2026-10-21T15:00:00Z')).out;
  const row = (k) => { const l = out.split('\n').find((x) => x.startsWith(k)); assert.ok(l, k); return l.slice(k.length).trim(); };
  assert.equal(row('tasks planned'), '4');
  assert.equal(row('done'), '1');
  assert.equal(row('cycle time claim->done p50 (h)'), '4.0');
  assert.equal(row('cycle time claim->done p95 (h)'), '4.0');
  assert.equal(row('review rounds per merged task (mean)'), '1.00');
  assert.equal(row('review rounds, max'), '1');
  assert.equal(row('gate events (orch log --section gates)'), '1');
  assert.equal(row('hours blocked'), '2.0');
  assert.equal(row('owner-zone wait (h, 1 owner tasks on the frontier)'), '6.0');
  assert.equal(row('tasks planned'), '4');
  const r2 = ok(orch(fx, ['metrics', '--release', 'R2'], '2026-10-21T15:00:00Z')).out;
  assert.match(r2, /^tasks planned\s+1$/m);
  assert.equal(ok(orch(fx, ['metrics', '--md'], '2026-10-21T15:00:00Z')).out, `${PLAN_DIR}/METRICS.md\n`);
  assert.ok(readRel(fx, `${PLAN_DIR}/METRICS.md`).includes('| hours blocked | 2.0 |'));
});

test('resume fits in 40 lines and marks a task with no event for over two hours stale', () => {
  const fx = fixture();
  ok(orch(fx, ['claim', 'A', '--by', 'builder-1', '--branch', 'feat/widget'], NOW));
  for (let i = 0; i < 30; i++) {
    const rel = ok(orch(fx, ['new', `X${i}`, '--feature', 'F1', '--release', 'R1', '--title', `Extra ${i}`, '--repo', 'umbrella'])).out.trim();
    let t = readRel(fx, rel);
    for (const [h, c] of Object.entries(BODY)) t = setSection(t, h, c);
    writeRel(fx, rel, t);
  }
  for (let i = 0; i < 12; i++) ok(orch(fx, ['set', `X${i}`, 'blocked', '--reason', `reason ${i}`]));
  const out = ok(orch(fx, ['resume'], '2026-10-21T12:00:00Z')).out;
  const lines = out.trimEnd().split('\n');
  assert.ok(lines.length <= 40, `${lines.length} lines`);
  assert.ok(lines[0].startsWith('plan fx; source_sha256 ') && lines[0].endsWith('the source now matches'), lines[0]);
  assert.ok(lines.some((l) => l === '  A in_progress by builder-1 branch feat/widget pr - age 3h stale (no event for 3h)'), out);
  assert.ok(lines.some((l) => /^ready \(\d+\):$/.test(l)));
  assert.ok(lines.some((l) => /^ {2}\.\.\. and \d+ more$/.test(l)));
  assert.ok(lines.includes('proposed: 0'));
});

// P-4: a merged task waits on its observer, a held task on its date, a waiting task on dependencies in flight, merged,
// blocked or held. Each is a list of its own in resume, so a resume with nothing ready still says what it waits on.
test('resume lists a merged task with its PR, merge SHA, claimer, age since merged and who may observe it', () => {
  const fx = fixture();
  toReview(fx);
  const sha = land(fx);
  ok(orch(fx, ['set', 'A', 'merged', '--merge-sha', sha], '2026-10-21T10:00:00Z'));
  const lines = ok(orch(fx, ['resume'], '2026-10-21T13:00:00Z')).out.trimEnd().split('\n');
  const at = lines.indexOf('merged:');
  assert.ok(at > 0, lines.join('\n'));
  assert.equal(lines[at + 1], `  A pr https://example.invalid/pr/1 merge ${sha} by builder-1 merged 3h ago; observer: not builder-1`);
});

test('resume lists held tasks with their date and waiting tasks with every unmet dependency and its state', () => {
  const fx = fixture();
  ok(orch(fx, ['set', 'A', 'blocked', '--reason', 'waits on Z-07']));
  const extra = (id, deps) => {
    const rel = ok(orch(fx, ['new', id, '--feature', 'F1', '--release', 'R1', '--title', `Extra ${id}`, '--repo', 'umbrella', '--depends-on', deps])).out.trim();
    let t = readRel(fx, rel);
    for (const [h, c] of Object.entries(BODY)) t = setSection(t, h, c);
    writeRel(fx, rel, t);
  };
  extra('E', 'A,D');
  extra('F', 'B');
  const lines = ok(orch(fx, ['resume'])).out.trimEnd().split('\n');
  const list = (h) => { const i = lines.indexOf(`${h}:`); assert.ok(i > 0, `${h}:\n${lines.join('\n')}`); const r = []; for (let j = i + 1; j < lines.length && lines[j].startsWith('  '); j++) r.push(lines[j]); return r; };
  assert.deepEqual(list('held'), ['  D not before 2026-10-22']);
  // F depends on B, a todo task that is neither ready nor held: F is two steps away, not on the next frontier.
  assert.deepEqual(list('waiting'), ['  B waits on A (blocked)', '  E waits on A (blocked), D (held until 2026-10-22)']);
});

// A list's cap is its floor, not its ceiling: the lines a short list leaves go to one that overflows, so four holds on a
// quiet plan are printed in full (P-3's AC1 copy had four, and resume showed two).
test('resume gives the lines a short list leaves to a list over its cap', () => {
  const fx = fixture();
  const extra = (id) => {
    const rel = ok(orch(fx, ['new', id, '--feature', 'F1', '--release', 'R1', '--title', `Extra ${id}`, '--repo', 'umbrella'])).out.trim();
    let t = readRel(fx, rel);
    for (const [h, c] of Object.entries(BODY)) t = setSection(t, h, c);
    writeRel(fx, rel, t);
  };
  for (const id of ['E', 'F', 'G']) { extra(id); ok(orch(fx, ['not-before', id, '2026-10-30'])); }
  const lines = ok(orch(fx, ['resume'])).out.trimEnd().split('\n');
  const i = lines.indexOf('held:');
  assert.ok(i > 0, lines.join('\n'));
  assert.deepEqual(lines.slice(i + 1, i + 6), ['  D not before 2026-10-22', '  E not before 2026-10-30', '  F not before 2026-10-30', '  G not before 2026-10-30', 'waiting:'], lines.join('\n'));
});

test('resume holds every list to its cap and stays within 40 lines when every list overflows', () => {
  const fx = fixture();
  let n = 0;
  const extra = (opts = []) => {
    const id = `X${n++}`;
    const rel = ok(orch(fx, ['new', id, '--feature', 'F1', '--release', 'R1', '--title', `Extra ${id}`, '--repo', 'none', '--agent', 'orchestrator', ...opts])).out.trim();
    let t = readRel(fx, rel);
    for (const [h, c] of Object.entries(BODY)) t = setSection(t, h, c);
    writeRel(fx, rel, t);
    return id;
  };
  const review = (id) => {
    ok(orch(fx, ['claim', id, '--by', 'builder-1']));
    for (const s of ['built', 'why', 'gates']) ok(orch(fx, ['log', id, '--section', s, '--text', `${s} entry`]));
    ok(orch(fx, ['set', id, 'in_review']));
  };
  for (let i = 0; i < 14; i++) review(extra());
  for (let i = 0; i < 7; i++) ok(orch(fx, ['set', `X${i}`, 'merged']));
  const blocked = [];
  for (let i = 0; i < 12; i++) { const id = extra(); blocked.push(id); ok(orch(fx, ['set', id, 'blocked', '--reason', `reason ${i}`])); }
  for (let i = 0; i < 12; i++) ok(orch(fx, ['not-before', extra(), '2026-10-30']));
  for (let i = 0; i < 12; i++) extra(['--depends-on', blocked[i]]);
  for (let i = 0; i < 12; i++) extra();
  const lines = ok(orch(fx, ['resume'])).out.trimEnd().split('\n');
  assert.ok(lines.length <= 40, `${lines.length} lines:\n${lines.join('\n')}`);
  // With every list over its cap there is no spare line: each list gets exactly its floor.
  const floors = [['in flight', 6], ['merged', 5], [/^ready \(\d+\)$/, 6], ['held', 3], ['waiting', 6], ['blocked', 5]];
  for (const [h, floor] of floors) {
    const i = lines.findIndex((l) => (h instanceof RegExp ? h.test(l.slice(0, -1)) : l === `${h}:`));
    assert.ok(i > 0, `${h}:\n${lines.join('\n')}`);
    let j = i + 1;
    while (j < lines.length && lines[j].startsWith('  ')) j++;
    assert.match(lines[j - 1], /^ {2}\.\.\. and \d+ more$/, `${h} overflows into "... and N more"`);
    assert.equal(j - i - 1, floor, `${h} keeps its floor of ${floor} lines`);
  }
  assert.equal(lines.at(-1), 'proposed: 0');
});

// ---------------- the pre-commit step ----------------
test('pre-commit.sh validates the staged orchestration/ and refuses a hand-edited done', () => {
  const fx = fixture();
  fs.mkdirSync(path.join(fx.root, 'orchestration/bin'), { recursive: true });
  fs.cpSync(SCHEMA, path.join(fx.root, 'orchestration/schema'), { recursive: true });
  for (const f of ['orch.mjs', 'pre-commit.sh']) fs.copyFileSync(path.join(BIN, f), path.join(fx.root, 'orchestration/bin', f));
  const hook = () => spawnSync('sh', ['orchestration/bin/pre-commit.sh'], { cwd: fx.root, encoding: 'utf8', env: cleanEnv() });
  git(fx.root, 'add', 'orchestration');
  assert.equal(hook().status, 0, 'valid staged plan passes');
  edit(fx, 'A', (t) => t.replace('status: todo', 'status: done'));
  git(fx.root, 'add', taskRel('A'));
  const red = hook();
  assert.equal(red.status, 1);
  assert.ok(red.stderr.includes(`orch: ${taskRel('A')}: done needs observed_by (--observed-by)`), red.stderr);
  git(fx.root, 'reset', '-q', '--', taskRel('A'));
  assert.equal(hook().status, 0, 'the working tree is not what is checked; the staged file is');
  git(fx.root, 'add', '.');
  git(fx.root, 'reset', '-q', '--', 'orchestration');
  writeRel(fx, 'other.txt', 'x\n');
  git(fx.root, 'add', 'other.txt');
  assert.equal(hook().status, 0, 'nothing under orchestration/ staged: not run');
});

// ---------------- the skill's setup line (P-2) ----------------
// SKILL.md's setup block, run as written and followed by the skill's own `validate` form, under every shell a session
// may use, on a copy of the fixture that carries the tool. zsh does not word-split an unquoted variable, so a form
// that works under sh and bash can still exit 127 there. A shell not on the host is skipped by name, never passed.
const SKILL = path.join(BIN, '..', '..', '.claude', 'skills', 'orchestrate', 'SKILL.md');
test('SKILL.md\'s setup block then validate exits 0 under sh, bash and zsh', async (t) => {
  const skill = fs.readFileSync(SKILL, 'utf8');
  const block = /Start every Bash call with\n\n```sh\n([\s\S]*?)\n```/.exec(skill);
  assert.ok(block, 'SKILL.md has the setup block after "Start every Bash call with"');
  const use = /\*\*State\.\*\* `([^`\s]+) validate`/.exec(skill);
  assert.ok(use, 'SKILL.md\'s loop step 1 (State) shows the validate command');
  const fx = fixture();
  fs.cpSync(ORCH, path.join(fx.root, 'orchestration', 'bin', 'orch.mjs'));
  fs.cpSync(SCHEMA, path.join(fx.root, 'orchestration', 'schema'), { recursive: true });
  const script = `${block[1].replace('<umbrella root, absolute>', `'${fx.root}'`)}\n${use[1]} validate\n`;
  const env = { ...cleanEnv(), PATH: `${path.dirname(process.execPath)}${path.delimiter}${process.env.PATH}`, ORCH_NOW: NOW };
  delete env.BASH_ENV;
  delete env.ENV;
  // zsh -f: the default options and no startup files, so a user's `setopt shwordsplit` cannot hide the failure.
  for (const [shell, flags] of [['sh', ['-c']], ['bash', ['-c']], ['zsh', ['-f', '-c']]]) {
    await t.test(shell, (st) => {
      const probe = spawnSync(shell, ['-c', 'exit 0']);
      if (probe.error?.code === 'ENOENT') return st.skip(`${shell} is not installed`);
      const r = spawnSync(shell, [...flags, script], { cwd: os.tmpdir(), encoding: 'utf8', env });
      assert.equal(r.status, 0, `${shell} exited ${r.status}: ${r.stderr}`);
      assert.equal(r.stdout, 'orch: valid: 4 tasks, 1 features\n');
    });
  }
});

// ---------------- the skill's commands against the tool's (P-5) ----------------
// Two copies of one list: the commands `orch help` prints from the tool's own table, and the ones SKILL.md invokes
// (`orch <command>` in backticks). Each must hold the other's; the failure names what is missing on which side.
test('SKILL.md invokes every command the tool\'s usage lists, and no other', () => {
  const help = ok(orch(fixture(), ['help'])).out;
  const usage = new Set([...help.matchAll(/^ {2}([a-z][a-z-]*)/gm)].map((m) => m[1]));
  assert.ok(usage.has('help') && usage.has('validate'), `the usage parsed: ${[...usage].join(' ')}`);
  const skill = new Set([...fs.readFileSync(SKILL, 'utf8').matchAll(/`orch ([a-z][a-z-]*)/g)].map((m) => m[1]));
  const notInSkill = [...usage].filter((c) => !skill.has(c)).sort();
  const notInTool = [...skill].filter((c) => !usage.has(c)).sort();
  assert.deepEqual({ notInSkill, notInTool }, { notInSkill: [], notInTool: [] });
});
