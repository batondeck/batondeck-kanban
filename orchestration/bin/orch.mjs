#!/usr/bin/env node
// orch: the orchestration harness's one tool. Node built-ins only, no dependencies.
// Reads and writes the plan folders under orchestration/; `orch help` lists the commands.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { parseArgs } from 'node:util';
import { fileURLToPath } from 'node:url';

const TOOL = fileURLToPath(import.meta.url);
const SCHEMA = path.join(path.dirname(TOOL), '..', 'schema');

const STATUSES = ['todo', 'in_progress', 'in_review', 'merged', 'done', 'blocked', 'dropped', 'proposed'];
const AGENTS = ['builder', 'cheap', 'owner', 'orchestrator'];
const SIZES = ['S', 'M', 'L'];
const ZONES = ['Z1', 'Z2', 'Z3', 'Z4', 'Z5', 'Z6', 'Z7', 'Z8'];
const LIST_KEYS = new Set(['depends_on', 'zones', 'releases', 'copies']);
const LOG = { built: 'Built', why: 'Why like that', deviations: 'Deviations', gates: 'Gates', review: 'Review', observed: 'Observed' };
const REQUIRED = ['Why', 'Requirements', 'Inputs', 'Outputs', 'Acceptance criteria', 'Definition of done', 'Context to load', 'Zones'];
const AC_RE = /^- \[( |x)\] (AC\d+) .+ — evidence:(.*)$/;
const ID_RE = /^[A-Za-z0-9][A-Za-z0-9.-]*$/;
const REPO_RE = /^[a-z0-9][a-z0-9-]*$/;
// The states a task can be blocked from. A merged task that needs more work gets a new task (orch discover):
// sending it back to todo would keep its merge SHA from the finished cycle.
const BLOCKABLE = ['todo', 'in_progress', 'in_review'];
const CLAIMED = ['in_progress', 'in_review', 'merged', 'done'];
const BUDGET = { task: 1200, featureContext: 1000, planContext: 1500, brief: 4000, logEntry: 1000 };
const STALE_HOURS = 2;
const RESUME_LINES = 40;

class Refusal extends Error {
  constructor(items) { super('refused'); this.items = items; }
}
const refuse = (subject, reason) => { throw new Refusal([[subject, reason]]); };
const refuseAll = (items) => { if (items.length) throw new Refusal(items); };

// ---------- time (ORCH_NOW is the clock seam the tests move) ----------
function now() {
  const v = process.env.ORCH_NOW;
  if (!v) return new Date();
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) refuse('ORCH_NOW', `not an ISO-8601 time: ${v}`);
  return d;
}
const iso = (d) => d.toISOString().replace(/\.\d{3}Z$/, 'Z');
const hoursBetween = (a, b) => (new Date(b) - new Date(a)) / 3600000;
function age(fromIso, at) {
  if (!fromIso) return '';
  const h = Math.max(0, Math.floor(hoursBetween(fromIso, at)));
  return h < 48 ? `${h}h` : `${Math.floor(h / 24)}d ${h % 24}h`;
}

// ---------- files ----------
const isDir = (p) => { try { return fs.statSync(p).isDirectory(); } catch { return false; } };
const exists = (p) => fs.existsSync(p);
// Exact-name test: macOS folders are case-insensitive, so schema/plan.md must not pass for PLAN.md.
const hasFile = (dir, name) => isDir(dir) && fs.readdirSync(dir).includes(name);
const read = (p) => fs.readFileSync(p, 'utf8');
const words = (t) => t.split(/\s+/).filter(Boolean).length;
function writeAtomic(abs, text) {
  const tmp = `${abs}.orch-${process.pid}.tmp`;
  fs.writeFileSync(tmp, text);
  fs.renameSync(tmp, abs);
}

function findRoot() {
  let d = process.cwd();
  for (;;) {
    if (isDir(path.join(d, 'orchestration'))) return d;
    const up = path.dirname(d);
    if (up === d) refuse(process.cwd(), 'no orchestration/ folder here or in any parent');
    d = up;
  }
}

// ---------- front matter: the flat YAML subset of the templates ----------
function parseValue(raw, rel, key) {
  if (raw === '""') return '';
  if (raw.startsWith('"')) {
    try { const v = JSON.parse(raw); if (typeof v === 'string') return v; } catch { /* refused below */ }
    refuse(rel, `${key}: malformed quoted value: ${raw}`);
  }
  if (raw.startsWith('[')) {
    if (!raw.endsWith(']')) refuse(rel, `${key}: list not closed by ]`);
    const inner = raw.slice(1, -1).trim();
    return inner === '' ? [] : inner.split(',').map((s) => s.trim());
  }
  return raw.trim();
}
function formatValue(v) {
  if (Array.isArray(v)) return `[${v.join(', ')}]`;
  if (v === '') return '""';
  if (/^["[]/.test(v) || v !== v.trim()) return JSON.stringify(v);
  return v;
}
function parseDoc(text, rel) {
  if (text.includes('\r')) refuse(rel, 'CRLF line endings; the tool reads LF only');
  if (!text.startsWith('---\n')) refuse(rel, 'no front matter (the file must start with ---)');
  const end = text.indexOf('\n---\n', 3);
  if (end < 0) refuse(rel, 'front matter not closed by ---');
  const fields = [];
  text.slice(4, end).split('\n').forEach((line, i) => {
    const m = /^([a-z_][a-z0-9_]*):(?: (.*))?$/.exec(line);
    if (!m) refuse(rel, `front matter line ${i + 2} is not "key: value": ${line}`);
    fields.push([m[1], parseValue(m[2] ?? '', rel, m[1])]);
  });
  return { fields, body: text.slice(end + 5) };
}
const serialize = (doc) => `---\n${doc.fields.map(([k, v]) => `${k}: ${formatValue(v)}`).join('\n')}\n---\n${doc.body}`;
const fmObject = (doc) => Object.fromEntries(doc.fields);
function setField(doc, key, value) {
  const f = doc.fields.find(([k]) => k === key);
  if (!f) refuse(key, 'the file has no such front-matter key');
  f[1] = value;
}

// ---------- body: headings and sections (fenced code is not structure) ----------
function headingsOf(body) {
  const out = [];
  let fence = null;
  body.split('\n').forEach((line, i) => {
    const f = /^(```|~~~)/.exec(line);
    if (f) { if (!fence) fence = f[1]; else if (line.startsWith(fence)) fence = null; return; }
    if (fence) return;
    const m = /^(#{1,6}) (.*)$/.exec(line);
    if (m) out.push({ level: m[1].length, text: m[2].trim(), line: i });
  });
  return out;
}
const headingLabel = (h) => `${'#'.repeat(h.level)} ${h.text}`;
function sectionLines(body, level, text) {
  const lines = body.split('\n');
  const hs = headingsOf(body);
  const i = hs.findIndex((h) => h.level === level && h.text === text);
  if (i < 0) return null;
  const end = i + 1 < hs.length ? hs[i + 1].line : lines.length;
  return lines.slice(hs[i].line + 1, end).map((s) => s.trim()).filter(Boolean);
}
function withoutLog(text) {
  const doc = text.split('\n');
  const start = doc.findIndex((l) => l === '## Execution log');
  if (start < 0) return text;
  let end = start + 1;
  while (end < doc.length && !/^#{1,2} /.test(doc[end])) end++;
  return [...doc.slice(0, start), ...doc.slice(end)].join('\n');
}

// ---------- templates: keys, headings and placeholder lines are read from schema/*.md ----------
const templates = {};
function template(name) {
  if (templates[name]) return templates[name];
  const abs = path.join(SCHEMA, `${name}.md`);
  if (!exists(abs)) refuse(abs, 'template missing');
  const text = read(abs);
  const doc = text.startsWith('---\n') ? parseDoc(text, abs) : { fields: [], body: text };
  const hs = headingsOf(doc.body);
  const lines = doc.body.split('\n');
  const placeholders = new Map();
  hs.forEach((h, i) => {
    const end = i + 1 < hs.length ? hs[i + 1].line : lines.length;
    placeholders.set(headingLabel(h), new Set(lines.slice(h.line + 1, end).map((s) => s.trim()).filter(Boolean)));
  });
  const t = { abs, text, doc, keys: doc.fields.map(([k]) => k), headings: hs.map(headingLabel), placeholders };
  if (name === 'task') {
    for (const h of [...REQUIRED.map((x) => `## ${x}`), '## Execution log', ...Object.values(LOG).map((x) => `### ${x}`)]) {
      if (!t.headings.includes(h)) refuse(abs, `the template has no heading "${h}", which the tool checks`);
    }
  }
  return (templates[name] = t);
}

// ---------- loading a plan ----------
function listPlans(root) {
  const dir = path.join(root, 'orchestration');
  return fs.readdirSync(dir).filter((n) => hasFile(path.join(dir, n), 'PLAN.md')).sort();
}
function selectPlans(root, opt, all = false) {
  const plans = listPlans(root);
  if (opt) {
    if (!plans.includes(opt)) refuse(`--plan ${opt}`, `no orchestration/${opt}/PLAN.md`);
    return [opt];
  }
  if (all) return plans;
  if (!plans.length) refuse('orchestration/', 'no plan folder (a folder holding PLAN.md)');
  if (plans.length > 1) refuse('orchestration/', `several plans (${plans.join(', ')}); pass --plan <id>`);
  return plans;
}

function loadPlan(root, id) {
  const dir = path.join(root, 'orchestration', id);
  const rel = (abs) => path.relative(root, abs);
  const errors = [];
  const tryParse = (abs) => {
    try { return parseDoc(read(abs), rel(abs)); } catch (e) { if (e instanceof Refusal) { errors.push(...e.items); return null; } throw e; }
  };
  const planAbs = path.join(dir, 'PLAN.md');
  const planDoc = tryParse(planAbs);
  const plan = { root, id, dir, rel, errors, planAbs, planDoc, pm: planDoc ? fmObject(planDoc) : {}, features: [], tasks: [] };
  plan.releases = Array.isArray(plan.pm.releases) ? plan.pm.releases : [];
  // Read once per plan. An unreadable log is a refusal of its own; the fields are then not compared with it.
  try { plan.events = readEvents(plan); } catch (e) { if (!(e instanceof Refusal)) throw e; errors.push(...e.items); plan.events = null; }
  for (const name of fs.readdirSync(dir).sort()) {
    const fdir = path.join(dir, name);
    if (!isDir(fdir) || name === 'adr') continue;
    const fabs = path.join(fdir, 'FEATURE.md');
    if (!hasFile(fdir, 'FEATURE.md')) {
      if (isDir(path.join(fdir, 'tasks'))) errors.push([rel(fdir), 'a tasks/ folder without FEATURE.md']);
      continue;
    }
    const fdoc = tryParse(fabs);
    if (!fdoc) continue;
    const feature = { abs: fabs, dir: fdir, doc: fdoc, fm: fmObject(fdoc) };
    plan.features.push(feature);
    const tdir = path.join(fdir, 'tasks');
    if (!isDir(tdir)) continue;
    for (const f of fs.readdirSync(tdir).filter((n) => n.endsWith('.md')).sort()) {
      const abs = path.join(tdir, f);
      const doc = tryParse(abs);
      if (doc) plan.tasks.push({ abs, rel: rel(abs), file: f, doc, fm: fmObject(doc), feature });
    }
  }
  plan.byId = new Map();
  for (const t of plan.tasks) if (!plan.byId.has(t.fm.id)) plan.byId.set(t.fm.id, t);
  return plan;
}
function openPlan(root, opt) {
  const [id] = selectPlans(root, opt);
  const plan = loadPlan(root, id);
  refuseAll(plan.errors);
  return plan;
}
function taskById(plan, id) {
  const t = plan.byId.get(id);
  if (!t) refuse(id, `no such task in plan ${plan.id}`);
  return t;
}
function saveTask(t) {
  writeAtomic(t.abs, serialize(t.doc));
  t.fm = fmObject(t.doc);
}
function appendEvent(plan, ev) {
  fs.appendFileSync(path.join(plan.dir, 'metrics.jsonl'), `${JSON.stringify(ev)}\n`);
}
function readEvents(plan) {
  const abs = path.join(plan.dir, 'metrics.jsonl');
  if (!exists(abs)) return [];
  return read(abs).split('\n').filter(Boolean).map((l, i) => {
    try { return JSON.parse(l); } catch { return refuse(`${plan.rel(abs)}:${i + 1}`, 'not a JSON line'); }
  });
}

// ---------- checks ----------
function checkKeys(subject, fields, expected) {
  const actual = fields.map(([k]) => k);
  if (actual.join(',') === expected.join(',')) return [];
  const out = [];
  for (const k of expected) if (!actual.includes(k)) out.push([subject, `missing front-matter key "${k}"`]);
  for (const k of actual) if (!expected.includes(k)) out.push([subject, `unknown front-matter key "${k}"`]);
  if (new Set(actual).size !== actual.length) out.push([subject, 'a front-matter key appears twice']);
  if (!out.length) out.push([subject, `front-matter keys out of order; the template's order is: ${expected.join(', ')}`]);
  return out;
}
// maxLevel: PLAN.md's template asks for one ### subsection per release, so only its # and ## are fixed.
function checkHeadings(subject, body, expected, maxLevel = 6) {
  const actual = headingsOf(body).filter((h) => h.level <= maxLevel).map(headingLabel);
  for (let i = 0; i < Math.max(actual.length, expected.length); i++) {
    if (actual[i] === expected[i]) continue;
    if (actual[i] === undefined) return [[subject, `missing heading "${expected[i]}"`]];
    if (expected[i] === undefined) return [[subject, `unexpected heading "${actual[i]}"`]];
    return [[subject, `heading ${i + 1} is "${actual[i]}", the template has "${expected[i]}"`]];
  }
  return [];
}
function acLines(t) {
  return (sectionLines(t.doc.body, 2, 'Acceptance criteria') || []).map((line) => {
    const m = AC_RE.exec(line);
    return m ? { line, ok: true, ticked: m[1] === 'x', label: m[2], evidence: m[3].trim() } : { line, ok: false };
  });
}
function sectionEmpty(t, heading) {
  const lines = sectionLines(t.doc.body, 2, heading);
  if (!lines) return true;
  const ph = template('task').placeholders.get(`## ${heading}`) || new Set();
  return lines.every((l) => ph.has(l));
}
function logEmpty(t, key) {
  const lines = sectionLines(t.doc.body, 3, LOG[key]);
  return !lines || lines.length === 0;
}

// The checks a task's status implies; cumulative along todo -> in_progress -> in_review -> merged -> done.
// Tasks with repo "none" (owner and orchestrator work) carry no branch, PR or merge SHA.
// A proposed task is a stub from orch discover: its sections are filled before the owner makes it todo.
function stateChecks(t, status) {
  const e = [];
  const f = t.fm;
  const none = f.repo === 'none';
  if (status !== 'dropped' && status !== 'proposed') {
    for (const h of REQUIRED) if (sectionEmpty(t, h)) e.push(`## ${h} is empty or still the template's placeholder`);
  }
  if (CLAIMED.includes(status)) {
    if (!f.claimed_by) e.push(`${status} needs claimed_by (orch claim --by)`);
    if (!none && !f.branch) e.push(`${status} needs branch (orch claim --branch)`);
    if (!f.started_at) e.push(`${status} needs started_at (set by orch claim)`);
    if (!none && !f.base_sha) e.push(`${status} needs base_sha (set by orch claim)`);
  }
  if (['in_review', 'merged', 'done'].includes(status)) {
    if (!none && !f.pr) e.push(`${status} needs pr (--pr)`);
    for (const k of ['built', 'why', 'gates']) if (logEmpty(t, k)) e.push(`${status} needs the log's "### ${LOG[k]}" (orch log --section ${k})`);
  }
  if (['merged', 'done'].includes(status) && !none && !f.merge_sha) e.push(`${status} needs merge_sha (--merge-sha)`);
  if (status === 'done') {
    const acs = acLines(t).filter((a) => a.ok);
    if (!acs.length) e.push('done needs at least one acceptance line');
    for (const a of acs) if (!a.ticked || !a.evidence) e.push(`done needs ${a.label} ticked with its evidence`);
    for (const k of ['deviations', 'review', 'observed']) if (logEmpty(t, k)) e.push(`done needs the log's "### ${LOG[k]}" (orch log --section ${k})`);
    if (!f.observed_by) e.push('done needs observed_by (--observed-by)');
    else if (f.observed_by === f.claimed_by) e.push(`observed_by "${f.observed_by}" is the claimer; the observer must be someone else`);
    if (!f.finished_at) e.push('done needs finished_at (set by orch set done)');
  }
  if (status === 'blocked' && !f.blocked_reason) e.push('blocked needs blocked_reason (--reason)');
  return e;
}

// Every front-matter field the tool writes, and the events that write it. Replaying a task's events in metrics.jsonl
// gives the value each field must hold, so a hand edit of the task file alone is refused: a task with no event is a
// todo with every field empty. Events are append-only and read as they are; a backfill event sets the fields it names
// (one that an older event did not carry).
// repo and agent are set when the task is loaded and are free text until it is claimed; the claim event records them,
// and from then on the file must hold what the latest claim recorded (or a later backfill naming them). A reopen
// frees them with the rest of the claim.
const CLAIMED_AS = ['repo', 'agent'];
const STATE = ['status', 'discovered_from', 'not_before', 'claimed_by', 'branch', 'pr', 'merge_sha', 'observed_by', 'started_at', 'base_sha', 'finished_at', 'blocked_reason'];
// What blocked -> todo clears: a reopened task starts a new cycle, and nothing of the old claim stands in for the next.
const REOPEN = ['blocked_reason', 'claimed_by', 'branch', 'pr', 'started_at', 'base_sha'];
// The base_sha of a claim on an origin with no commits (orch claim, baseOf); validate takes it only from a claim event
// that recorded empty_origin, and the merge check finds nothing in it.
const EMPTY = '0'.repeat(40);
function replay(events) {
  const s = Object.fromEntries(STATE.map((k) => [k, '']));
  s.status = 'todo';
  s.held = false;
  s.empty_origin = false;
  const str = (v) => (typeof v === 'string' ? v : '');
  for (const e of events) {
    if (e.event === 'backfill') { for (const k of [...STATE, ...CLAIMED_AS]) if (k in e) s[k] = str(e[k]); if ('base_sha' in e) s.empty_origin = false; continue; }
    if (e.event === 'not_before') { s.not_before = str(e.to); continue; }
    if (e.event !== 'status') continue;
    if (e.to === 'proposed') s.discovered_from = str(e.discovered_from);
    if (e.from === 'todo' && e.to === 'in_progress') Object.assign(s, { claimed_by: str(e.by), branch: str(e.branch), base_sha: str(e.base_sha), empty_origin: e.empty_origin === true, started_at: str(e.at), repo: str(e.repo), agent: str(e.agent), held: true });
    if (e.to === 'in_review') s.pr = str(e.pr);
    if (e.to === 'merged') s.merge_sha = str(e.merge_sha);
    if (e.to === 'done') Object.assign(s, { observed_by: str(e.observed_by), finished_at: str(e.at) });
    if (e.to === 'dropped') s.finished_at = str(e.at);
    if (e.to === 'blocked') s.blocked_reason = str(e.reason);
    if (e.to === 'todo' && e.from === 'blocked') { for (const k of REOPEN) s[k] = ''; s.held = false; s.empty_origin = false; }
    s.status = str(e.to);
  }
  return s;
}

// pending: the event a command is about to append, so the file it is about to write is checked against the log it
// will leave.
function validateTask(plan, t, pending = null) {
  const out = [];
  const add = (reason) => out.push([t.rel, reason]);
  const f = t.fm;
  if (plan.events) {
    const want = replay([...plan.events.filter((e) => e && e.task === f.id), ...(pending ? [pending] : [])]);
    for (const k of [...STATE, ...(want.held ? CLAIMED_AS : [])]) if ((f[k] ?? '') !== want[k]) add(`${k} is ${JSON.stringify(f[k] ?? '')}; the events in metrics.jsonl give ${JSON.stringify(want[k])}`);
    if (f.base_sha === EMPTY && !want.empty_origin) add('base_sha is forty zeros, the empty-origin base, but no claim event recorded empty_origin');
  }
  out.push(...checkKeys(t.rel, t.doc.fields, template('task').keys));
  if (!ID_RE.test(f.id || '')) add(`id "${f.id}" is not an id (letters, digits, "." and "-")`);
  const slug = t.file.startsWith(`${f.id}-`) ? t.file.slice(f.id.length + 1, -3) : null;
  if (!slug || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)) add(`file name must be <id>-<slug>.md with a lower-case slug (id ${f.id})`);
  if (!f.title) add('title is empty');
  if (f.plan !== plan.id) add(`plan is "${f.plan}", the folder is ${plan.id}`);
  if (f.feature !== t.feature.fm.id) add(`feature is "${f.feature}", the folder's FEATURE.md is ${t.feature.fm.id}`);
  if (!plan.releases.includes(f.release)) add(`release "${f.release}" is not one of PLAN.md's releases (${plan.releases.join(', ')})`);
  if (!STATUSES.includes(f.status)) add(`status "${f.status}" is not one of ${STATUSES.join(', ')}`);
  if (!AGENTS.includes(f.agent)) add(`agent "${f.agent}" is not one of ${AGENTS.join(', ')}`);
  if (!SIZES.includes(f.size)) add(`size "${f.size}" is not one of ${SIZES.join(', ')}`);
  for (const k of ['depends_on', 'zones']) if (!Array.isArray(f[k])) add(`${k} must be a list ([a, b] or [])`);
  if (Array.isArray(f.zones)) for (const z of f.zones) if (!ZONES.includes(z)) add(`zone "${z}" is not one of Z1..Z8`);
  if (!REPO_RE.test(f.repo || '')) add(`repo "${f.repo}" is not a repository name, "umbrella" or "none"`);
  if (f.repo === 'none' && !['owner', 'orchestrator'].includes(f.agent)) add(`repo none is for owner and orchestrator tasks; agent ${f.agent} needs a repository`);
  if (f.repo === 'none') for (const k of ['branch', 'pr', 'base_sha', 'merge_sha']) if (f[k]) add(`repo none carries no ${k}; it is ${JSON.stringify(f[k])}`);
  if (f.not_before && !/^\d{4}-\d{2}-\d{2}$/.test(f.not_before)) add(`not_before "${f.not_before}" is not YYYY-MM-DD`);
  if (f.merge_sha && !/^[0-9a-f]{7,40}$/.test(f.merge_sha)) add(`merge_sha "${f.merge_sha}" is not a hex SHA`);
  if (f.base_sha && !/^[0-9a-f]{40}$/.test(f.base_sha)) add(`base_sha "${f.base_sha}" is not a full hex SHA`);
  out.push(...checkHeadings(t.rel, t.doc.body, [`# ${f.id} ${f.title}`, ...template('task').headings.slice(1)]));
  const n = words(withoutLog(serialize(t.doc)));
  if (n > BUDGET.task) add(`${n} words without the Execution log, over the ${BUDGET.task}-word budget`);
  acLines(t).forEach((a, i) => {
    if (!a.ok) add(`acceptance line ${i + 1} does not read "- [ ] ACn … — evidence: …": ${a.line}`);
    else if (a.ticked && !a.evidence) add(`${a.label} is ticked without evidence`);
  });
  if (STATUSES.includes(f.status)) for (const r of stateChecks(t, f.status)) add(r);
  return out;
}

function findCycles(plan) {
  const cycles = [];
  const state = new Map();
  const seen = new Set();
  const visit = (id, stack) => {
    state.set(id, 1);
    stack.push(id);
    const t = plan.byId.get(id);
    for (const d of (t && Array.isArray(t.fm.depends_on) ? t.fm.depends_on : [])) {
      if (!plan.byId.has(d)) continue;
      if (state.get(d) === 1) {
        const cyc = [...stack.slice(stack.indexOf(d)), d];
        const key = [...cyc.slice(0, -1)].sort().join(',');
        if (!seen.has(key)) { seen.add(key); cycles.push(cyc); }
      } else if (!state.get(d)) visit(d, stack);
    }
    stack.pop();
    state.set(id, 2);
  };
  for (const id of [...plan.byId.keys()].sort(natural)) if (!state.get(id)) visit(id, []);
  return cycles;
}

function validatePlan(plan) {
  const out = [...plan.errors];
  const prel = plan.rel(plan.planAbs);
  if (plan.planDoc) {
    out.push(...checkKeys(prel, plan.planDoc.fields, template('plan').keys));
    if (plan.pm.id !== plan.id) out.push([prel, `id is "${plan.pm.id}", the folder is ${plan.id}`]);
    if (!plan.releases.length) out.push([prel, 'releases must be a non-empty list']);
    if (new Set(plan.releases).size !== plan.releases.length) out.push([prel, 'releases lists a release twice']);
    if (plan.pm.source_sha256 && !/^[0-9a-f]{64}$/.test(plan.pm.source_sha256)) out.push([prel, 'source_sha256 is not a sha256']);
    out.push(...checkHeadings(prel, plan.planDoc.body, [`# ${plan.pm.title}`, ...template('plan').headings.slice(1)], 2));
  }
  const ctx = (abs, budget) => {
    if (!hasFile(path.dirname(abs), path.basename(abs))) return out.push([plan.rel(abs), 'missing']);
    const n = words(read(abs));
    if (n > budget) out.push([plan.rel(abs), `${n} words, over the ${budget}-word budget`]);
  };
  ctx(path.join(plan.dir, 'CONTEXT.md'), BUDGET.planContext);
  const fids = new Map();
  for (const ft of plan.features) {
    const r = plan.rel(ft.abs);
    out.push(...checkKeys(r, ft.doc.fields, template('feature').keys));
    if (!ID_RE.test(ft.fm.id || '')) out.push([r, `id "${ft.fm.id}" is not an id`]);
    if (fids.has(ft.fm.id)) out.push([r, `feature id ${ft.fm.id} is also ${fids.get(ft.fm.id)}`]);
    fids.set(ft.fm.id, r);
    if (ft.fm.plan !== plan.id) out.push([r, `plan is "${ft.fm.plan}", the folder is ${plan.id}`]);
    if (!Array.isArray(ft.fm.releases)) out.push([r, 'releases must be a list']);
    else for (const rel of ft.fm.releases) if (!plan.releases.includes(rel)) out.push([r, `release "${rel}" is not one of PLAN.md's releases`]);
    out.push(...checkHeadings(r, ft.doc.body, [`# ${ft.fm.id} ${ft.fm.title}`, ...template('feature').headings.slice(1)]));
    ctx(path.join(ft.dir, 'CONTEXT.md'), BUDGET.featureContext);
  }
  const ids = new Map();
  for (const t of plan.tasks) {
    if (ids.has(t.fm.id)) out.push([t.rel, `id ${t.fm.id} is also ${ids.get(t.fm.id)}`]);
    else ids.set(t.fm.id, t.rel);
    out.push(...validateTask(plan, t));
    for (const d of Array.isArray(t.fm.depends_on) ? t.fm.depends_on : []) if (!plan.byId.has(d)) out.push([t.rel, `depends_on ${d}, which does not exist`]);
    if (t.fm.next && !plan.byId.has(t.fm.next)) out.push([t.rel, `next ${t.fm.next}, which does not exist`]);
    if (t.fm.discovered_from && !plan.byId.has(t.fm.discovered_from)) out.push([t.rel, `discovered_from ${t.fm.discovered_from}, which does not exist`]);
  }
  for (const c of findCycles(plan)) out.push([plan.rel(plan.dir), `dependency cycle: ${c.join(' -> ')}`]);
  for (const repo of new Set(plan.tasks.map((t) => t.fm.repo))) {
    const busy = inProgressIn(plan, repo);
    if (busy.length > 1) out.push([plan.rel(plan.dir), `${busy.map((t) => t.fm.id).join(' and ')} are in_progress in repo ${repo}; one builder per repository`]);
  }
  const builders = buildersInProgress(plan);
  if (builders.length > BUILDERS) out.push([plan.rel(plan.dir), overCap(builders)]);
  return out;
}

// ---------- the frontier ----------
function natural(a, b) {
  const ta = String(a).match(/\d+|\D+/g) || [];
  const tb = String(b).match(/\d+|\D+/g) || [];
  for (let i = 0; i < Math.max(ta.length, tb.length); i++) {
    if (ta[i] === undefined) return -1;
    if (tb[i] === undefined) return 1;
    const na = /^\d/.test(ta[i]);
    const nb = /^\d/.test(tb[i]);
    if (na && nb && Number(ta[i]) !== Number(tb[i])) return Number(ta[i]) - Number(tb[i]);
    if (ta[i] !== tb[i]) return ta[i] < tb[i] ? -1 : 1;
  }
  return 0;
}
function depthOf(plan) {
  const memo = new Map();
  const depth = (id, stack = new Set()) => {
    if (memo.has(id)) return memo.get(id);
    if (stack.has(id)) return 0;
    stack.add(id);
    const t = plan.byId.get(id);
    const deps = t && Array.isArray(t.fm.depends_on) ? t.fm.depends_on.filter((d) => plan.byId.has(d)) : [];
    const v = deps.length ? 1 + Math.max(...deps.map((d) => depth(d, stack))) : 0;
    stack.delete(id);
    memo.set(id, v);
    return v;
  };
  return depth;
}
// A dependency is satisfied when it is done, or when it is in_review or merged on a branch of the same
// repository: the task then stacks on that branch (claim --branch <it>). `stacked` lists those dependencies. A todo
// task that is not ready says why (the first reason), and, for resume, what holds it: `unmet`, every dependency not
// satisfied, or `held`, the not_before date when that alone keeps it off the frontier.
const STACKABLE = ['in_review', 'merged'];
function readiness(plan, t, at) {
  if (t.fm.status !== 'todo') return { why: `status is ${t.fm.status}` };
  const stacked = [];
  const unmet = [];
  const first = (dt) => `depends on ${dt.fm.id}, which is ${dt.fm.status}`;
  for (const d of Array.isArray(t.fm.depends_on) ? t.fm.depends_on : []) {
    const dt = plan.byId.get(d);
    if (!dt) return { why: unmet.length ? first(unmet[0]) : `depends on ${d}, which does not exist` };
    if (dt.fm.status === 'done') continue;
    if (STACKABLE.includes(dt.fm.status) && dt.fm.branch && dt.fm.repo === t.fm.repo) { stacked.push(dt); continue; }
    unmet.push(dt);
  }
  if (unmet.length) return { why: first(unmet[0]), unmet };
  const branches = [...new Set(stacked.map((dt) => dt.fm.branch))];
  if (branches.length > 1) return { why: `depends on ${stacked.map((dt) => `${dt.fm.id} on ${dt.fm.branch}`).join(' and ')}; a task stacks on one branch, wait for done`, unmet: stacked };
  if (t.fm.not_before && t.fm.not_before > iso(at).slice(0, 10)) return { why: `not before ${t.fm.not_before}`, held: t.fm.not_before };
  return { why: null, stacked, stack: branches[0] || null };
}
// Every task once, each with its readiness, in frontier order (release, dependency depth, id). The frontier is the
// ready ones; resume's held and waiting lists are read from the same readiness, never from a copy of its rule.
function triage(plan, at) {
  const depth = depthOf(plan);
  const rel = (t) => { const i = plan.releases.indexOf(t.fm.release); return i < 0 ? Infinity : i; };
  return plan.tasks
    .filter((t) => plan.byId.get(t.fm.id) === t)
    .map((t) => ({ t, r: readiness(plan, t, at) }))
    .sort((a, b) => rel(a.t) - rel(b.t) || depth(a.t.fm.id) - depth(b.t.fm.id) || natural(a.t.fm.id, b.t.fm.id));
}
function frontier(plan, at) {
  return triage(plan, at).filter(({ r }) => r.why === null).map(({ t, r }) => ({ t, stack: r.stack }));
}
const readyLine = ({ t, stack }) => [t.fm.id, t.fm.release, t.fm.feature, t.fm.repo, t.fm.agent, t.fm.size, t.fm.title, ...(stack ? [`(stack on ${stack})`] : [])].join('\t');

// ---------- input bounds ----------
function oneLine(name, v) {
  if (typeof v !== 'string' || !v.trim()) refuse(`--${name}`, 'must not be empty');
  if (/[\n\r]/.test(v)) refuse(`--${name}`, 'must be one line');
  return v.trim();
}
function slugify(title) {
  const s = title.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  return s.split('-').slice(0, 6).join('-') || 'task';
}

// ---------- scaffolding a task file from the template ----------
function scaffold(plan, values) {
  const tpl = template('task');
  const doc = parseDoc(tpl.text, tpl.abs);
  const blank = { discovered_from: '', not_before: '', next: '', claimed_by: '', branch: '', pr: '', merge_sha: '', observed_by: '', started_at: '', base_sha: '', finished_at: '', blocked_reason: '' };
  for (const [k] of doc.fields) if (k in blank) setField(doc, k, blank[k]);
  for (const [k, v] of Object.entries(values)) setField(doc, k, v);
  const lines = doc.body.split('\n');
  const h1 = lines.findIndex((l) => /^# /.test(l));
  lines[h1] = `# ${values.id} ${values.title}`;
  doc.body = lines.join('\n');
  const feature = plan.features.find((ft) => ft.fm.id === values.feature);
  const tdir = path.join(feature.dir, 'tasks');
  fs.mkdirSync(tdir, { recursive: true });
  const abs = path.join(tdir, `${values.id}-${slugify(values.title)}.md`);
  if (exists(abs)) refuse(plan.rel(abs), 'already exists');
  writeAtomic(abs, serialize(doc));
  return abs;
}
function checkNewFields(plan, v) {
  const e = [];
  if (!plan.features.some((ft) => ft.fm.id === v.feature)) e.push([`--feature ${v.feature}`, `no feature with that id in plan ${plan.id}`]);
  if (!plan.releases.includes(v.release)) e.push([`--release ${v.release}`, `not one of PLAN.md's releases (${plan.releases.join(', ')})`]);
  if (!AGENTS.includes(v.agent)) e.push([`--agent ${v.agent}`, `not one of ${AGENTS.join(', ')}`]);
  if (!REPO_RE.test(v.repo)) e.push([`--repo ${v.repo}`, 'not a repository name, "umbrella" or "none"']);
  if (v.repo === 'none' && !['owner', 'orchestrator'].includes(v.agent)) e.push([`--repo none`, `agent ${v.agent} needs a repository`]);
  refuseAll(e);
}

// ---------- git: the repository's origin; is a SHA on it, and newer than the claim? ----------
function git(args) {
  const env = { ...process.env };
  for (const k of ['GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GIT_OBJECT_DIRECTORY', 'GIT_PREFIX']) delete env[k];
  return spawnSync('git', args, { encoding: 'utf8', env });
}
// Fetches the task's checkout and names origin's default branch (origin/HEAD, else main). claim and the merge
// check both come through here, so the base and the merge are read from the same branch.
function originOf(plan, t, purpose) {
  const repo = t.fm.repo;
  const dir = repo === 'umbrella' ? plan.root : path.join(plan.root, repo);
  const shown = repo === 'umbrella' ? 'the umbrella root' : `${repo}/`;
  if (!exists(path.join(dir, '.git'))) refuse(t.fm.id, `repo ${repo} is not checked out at ${shown}; cannot ${purpose}`);
  if (git(['-C', dir, 'remote', 'get-url', 'origin']).status !== 0) refuse(t.fm.id, `repo ${repo} has no origin remote at ${shown}; cannot ${purpose}`);
  const f = git(['-C', dir, 'fetch', '-q', 'origin']);
  if (f.status !== 0) refuse(t.fm.id, `git fetch origin failed in ${shown}: ${(f.stderr || '').trim().split('\n')[0]}`);
  const h = git(['-C', dir, 'symbolic-ref', '-q', 'refs/remotes/origin/HEAD']);
  const def = h.status === 0 && h.stdout.trim() ? h.stdout.trim().replace(/^refs\/remotes\/origin\//, '') : 'main';
  return { dir, shown, def };
}
// The tip of origin's default branch at claim: the work merged for this claim is not in it yet. An origin with no
// refs at all (a repository not yet pushed to) gives EMPTY, "no commit is in base"; one with branches but not the
// default is refused, since EMPTY turns off the predates check.
function baseOf(plan, t) {
  const { dir, shown, def } = originOf(plan, t, "read the tip of its origin's default branch");
  const r = git(['-C', dir, 'rev-parse', '-q', '--verify', `refs/remotes/origin/${def}^{commit}`]);
  if (r.status === 0 && r.stdout.trim()) return r.stdout.trim();
  const refs = git(['-C', dir, 'for-each-ref', '--format=%(refname)', 'refs/remotes/origin/']);
  if (refs.status === 0 && !refs.stdout.trim()) return EMPTY;
  refuse(t.fm.id, `origin/${def} does not resolve in ${shown}`);
}
function checkOnRemote(plan, t, sha) {
  const { dir, shown, def } = originOf(plan, t, `check ${sha} on its origin`);
  const b = git(['-C', dir, 'branch', '-r', '--contains', sha]);
  if (b.status !== 0) refuse(t.fm.id, `${sha} is not a commit known in ${shown}`);
  const a = git(['-C', dir, 'merge-base', '--is-ancestor', sha, `origin/${def}`]);
  if (a.status !== 0 && a.status !== 1) refuse(t.fm.id, `cannot compare ${sha} with origin/${def} in ${shown}: ${(a.stderr || '').trim().split('\n')[0]}`);
  if (a.status === 1) {
    const on = b.stdout.split('\n').map((s) => s.trim()).filter((s) => s.startsWith('origin/') && !s.includes(' -> '));
    refuse(t.fm.id, `${sha} is not on origin/${def}, the default branch, in ${shown}; it is on ${on.length ? on.join(', ') : 'no branch of origin'}`);
  }
  // A SHA already in base_sha (or equal to it) was on the default branch before the claim: older work, not this task's.
  // Only ancestry is asked, so a squash or rebase merge (a new commit on top of the base) is accepted.
  const base = t.fm.base_sha;
  if (base === EMPTY) return;
  const p = git(['-C', dir, 'merge-base', '--is-ancestor', sha, base]);
  if (p.status === 0) refuse(t.fm.id, `${sha} is already in base_sha ${base}, origin/${def}'s tip when ${t.fm.id} was claimed; the merge predates the work`);
  if (p.status !== 1) refuse(t.fm.id, `cannot compare ${sha} with base_sha ${base} in ${shown}: ${(p.stderr || '').trim().split('\n')[0]}`);
}

// ---------- commands ----------
function cmdValidate(root, o) {
  const plans = selectPlans(root, o.plan, true);
  if (!plans.length) { console.log('orch: valid: no plan folder under orchestration/'); return; }
  const errors = [];
  const lines = [];
  for (const id of plans) {
    const plan = loadPlan(root, id);
    const e = validatePlan(plan);
    errors.push(...e);
    lines.push(`orch: ${plans.length > 1 ? `${id}: ` : ''}valid: ${plan.tasks.length} tasks, ${plan.features.length} features`);
  }
  refuseAll(errors);
  for (const l of lines) console.log(l);
}

function cmdReady(root, o) {
  const plan = openPlan(root, o.plan);
  for (const t of frontier(plan, now())) console.log(readyLine(t));
}

// One builder per repository: the in_progress tasks of `repo` (repo none is no repository). Claim and a review round's
// set <id> in_progress refuse on it, before the task moves; validate reports a plan that breaks it.
function inProgressIn(plan, repo) {
  if (repo === 'none') return [];
  return plan.tasks.filter((x) => x.fm.status === 'in_progress' && x.fm.repo === repo);
}
const oneBuilder = (busy, repo) => `${busy.fm.id} is already in_progress in repo ${repo}; one builder per repository`;
// At most BUILDERS builder tasks (any task with a repository, builder or cheap) in_progress at once, across
// repositories; repo none (owner and orchestrator steps) builds nothing and is not counted (umbrella #71). One count
// for claim, a review round's set <id> in_progress and validate: the first two refuse a task that would pass it,
// before the task moves; validate reports a plan over it.
const BUILDERS = 2;
const buildersInProgress = (plan) => plan.tasks.filter((x) => x.fm.status === 'in_progress' && x.fm.repo !== 'none');
// The builder tasks in_progress when they leave no room for `t`; null when it fits.
const capFull = (plan, t) => {
  const busy = t.fm.repo === 'none' ? [] : buildersInProgress(plan);
  return busy.length >= BUILDERS ? busy : null;
};
// Called with BUILDERS or more tasks, so always with two or more.
const overCap = (busy) => {
  const ids = busy.map((x) => x.fm.id);
  return `at most ${BUILDERS} builder tasks in_progress at once (repo none is not counted); ${ids.slice(0, -1).join(', ')} and ${ids.at(-1)} are`;
};

function cmdClaim(root, o, [id]) {
  if (!id) refuse('claim', 'usage: claim <id> --by <name> --branch <branch>');
  const plan = openPlan(root, o.plan);
  const t = taskById(plan, id);
  refuseAll(validateTask(plan, t));
  const by = oneLine('by', o.by);
  const at = now();
  const ready = readiness(plan, t, at);
  if (ready.why) refuse(id, `not in the ready frontier: ${ready.why}`);
  const full = capFull(plan, t);
  if (full) refuse(id, overCap(full));
  const [same] = inProgressIn(plan, t.fm.repo);
  if (same) refuse(id, oneBuilder(same, t.fm.repo));
  if (t.fm.repo === 'none' && o.branch !== undefined) refuse('--branch', 'repo none carries no branch');
  if (t.fm.repo !== 'none') {
    const br = oneLine('branch', o.branch);
    if (/\s/.test(br)) refuse('--branch', 'must not contain spaces');
    setField(t.doc, 'branch', br);
  }
  for (const dt of ready.stacked) {
    if (o.branch !== dt.fm.branch) refuse(id, `${dt.fm.id} is ${dt.fm.status} on ${dt.fm.branch}; claim with --branch ${dt.fm.branch} to stack on its PR, or wait for done`);
  }
  // After every offline refusal: the fetch is paid only by a claim that will be written.
  if (t.fm.repo !== 'none') setField(t.doc, 'base_sha', baseOf(plan, t));
  setField(t.doc, 'status', 'in_progress');
  setField(t.doc, 'claimed_by', by);
  setField(t.doc, 'started_at', iso(at));
  t.fm = fmObject(t.doc);
  const ev = { at: iso(at), task: id, event: 'status', from: 'todo', to: 'in_progress', by, repo: t.fm.repo, agent: t.fm.agent, branch: t.fm.branch || undefined, base_sha: t.fm.base_sha || undefined, empty_origin: t.fm.base_sha === EMPTY || undefined, stacked_on: ready.stack || undefined };
  refuseAll(validateTask(plan, t, ev));
  saveTask(t);
  appendEvent(plan, ev);
}

function cmdNotBefore(root, o, [id, date]) {
  if (!id || !date) refuse('not-before', 'usage: not-before <id> <YYYY-MM-DD|->');
  const plan = openPlan(root, o.plan);
  const t = taskById(plan, id);
  refuseAll(validateTask(plan, t));
  if (!['todo', 'blocked'].includes(t.fm.status)) refuse(id, `not-before is for todo and blocked tasks; ${id} is ${t.fm.status}`);
  const day = date === '-' ? '' : date;
  const parsed = new Date(`${day}T00:00:00Z`);
  if (day && (Number.isNaN(parsed.getTime()) || parsed.toISOString().slice(0, 10) !== day)) refuse('not-before', `"${date}" is not a date YYYY-MM-DD or "-"`);
  const from = t.fm.not_before || '';
  setField(t.doc, 'not_before', day);
  t.fm = fmObject(t.doc);
  const ev = { at: iso(now()), task: id, event: 'not_before', from, to: day };
  refuseAll(validateTask(plan, t, ev));
  saveTask(t);
  appendEvent(plan, ev);
}

function transitionAllowed(from, to) {
  if (to === 'in_review') return from === 'in_progress';
  if (to === 'in_progress') return from === 'in_review';
  if (to === 'merged') return from === 'in_review';
  if (to === 'done') return from === 'merged';
  if (to === 'blocked') return BLOCKABLE.includes(from);
  if (to === 'todo') return from === 'blocked' || from === 'proposed';
  if (to === 'dropped') return from !== 'done' && from !== 'dropped';
  return false;
}

function cmdSet(root, o, [id, to]) {
  if (!id || !to) refuse('set', 'usage: set <id> <status> [--pr url] [--merge-sha sha] [--observed-by name] [--reason text] [--by name]');
  const plan = openPlan(root, o.plan);
  const t = taskById(plan, id);
  // Every check validate makes, on the file as it is, before anything moves: a hand edit is never carried through.
  refuseAll(validateTask(plan, t));
  const from = t.fm.status;
  if (!STATUSES.includes(to)) refuse(id, `"${to}" is not a status (${STATUSES.join(', ')})`);
  if (from === 'todo' && to === 'in_progress') refuse(id, 'todo -> in_progress goes through orch claim');
  if (!transitionAllowed(from, to)) refuse(id, `${from} -> ${to} is not a transition (see orch help)`);
  const at = now();
  const val = (k) => (o[k] === undefined ? undefined : oneLine(k, o[k]));
  const by = val('by');
  const reason = val('reason');
  const ev = { at: iso(at), task: id, event: 'status', from, to, by };
  // Every entry into in_review, merged and done writes its field from the flag, or clears it, so a value left by an
  // earlier round never stands in for the flag; stateChecks then refuses the missing one.
  if (to === 'in_review') {
    const pr = o.pr === undefined ? '' : val('pr');
    setField(t.doc, 'pr', pr);
    if (pr) ev.pr = pr;
  }
  if (to === 'in_progress' && !reason) refuse(id, 'in_review -> in_progress is a review round and needs --reason (the findings)');
  if (to === 'in_progress') {
    const [busy] = inProgressIn(plan, t.fm.repo);
    if (busy) refuse(id, `${oneBuilder(busy, t.fm.repo)}: set ${busy.fm.id} blocked --reason, or wait for its in_review`);
    const full = capFull(plan, t);
    if (full) refuse(id, `${overCap(full)}: set one blocked --reason, or wait for its in_review`);
  }
  if (to === 'merged') {
    const sha = o['merge-sha'] === undefined ? '' : val('merge-sha');
    if (sha && !/^[0-9a-f]{7,40}$/.test(sha)) refuse('--merge-sha', `${sha} is not a hex SHA`);
    setField(t.doc, 'merge_sha', sha);
    if (sha) ev.merge_sha = sha;
  }
  if (to === 'done') {
    const observer = o['observed-by'] === undefined ? '' : val('observed-by');
    setField(t.doc, 'observed_by', observer);
    if (observer) ev.observed_by = observer;
    setField(t.doc, 'finished_at', iso(at));
  }
  if (to === 'blocked' && reason) setField(t.doc, 'blocked_reason', reason);
  if (to === 'todo' && from === 'blocked') for (const k of REOPEN) setField(t.doc, k, '');
  if (to === 'todo' && from === 'proposed' && by !== 'owner') refuse(id, 'proposed -> todo needs an owner\'s ruling (--by owner)');
  if (to === 'dropped') {
    if (!reason) refuse(id, 'dropped needs --reason (the order\'s words)');
    setField(t.doc, 'finished_at', iso(at));
    appendToLog(t, 'deviations', `${iso(at)} Dropped${by ? ` by ${by}` : ''}: ${reason}`);
  }
  setField(t.doc, 'status', to);
  t.fm = fmObject(t.doc);
  if (reason) ev.reason = reason;
  refuseAll(validateTask(plan, t, ev));
  // validate holds merge_sha and base_sha to the log but cannot see origin: merged and done fetch and check the SHA,
  // which refuses one off the default branch or already in base_sha even when it was forged into both files.
  if (['merged', 'done'].includes(to) && t.fm.repo !== 'none') checkOnRemote(plan, t, t.fm.merge_sha);
  saveTask(t);
  appendEvent(plan, ev);
}

function appendToLog(t, key, paragraph) {
  const lines = t.doc.body.split('\n');
  const hs = headingsOf(t.doc.body);
  const i = hs.findIndex((h) => h.level === 3 && h.text === LOG[key]);
  if (i < 0) refuse(t.rel, `no "### ${LOG[key]}" heading under Execution log`);
  const start = hs[i].line + 1;
  const end = i + 1 < hs.length ? hs[i + 1].line : lines.length;
  let k = end;
  while (k > start && lines[k - 1].trim() === '') k--;
  lines.splice(k, end - k, ...(k > start ? [''] : []), ...paragraph.split('\n'), '');
  t.doc.body = lines.join('\n');
}

function cmdLog(root, o, [id]) {
  if (!id) refuse('log', 'usage: log <id> --section <built|why|deviations|gates|review|observed> (--text "…" | --file path)');
  const plan = openPlan(root, o.plan);
  const t = taskById(plan, id);
  if (!(o.section in LOG)) refuse('--section', `must be one of ${Object.keys(LOG).join(', ')}`);
  if ((o.text === undefined) === (o.file === undefined)) refuse(id, 'pass exactly one of --text and --file');
  // Open until done: the acceptance is observed after the merge, so Observed is written in merged.
  if (!['in_progress', 'in_review', 'merged'].includes(t.fm.status)) refuse(id, `the log is written only in_progress, in_review or merged; the task is ${t.fm.status}`);
  const text = (o.text !== undefined ? o.text : read(path.resolve(o.file))).replace(/\s+$/, '');
  if (!text.trim()) refuse(id, 'the log text is empty');
  if (text.split('\n').some((l) => /^#{1,6} /.test(l) || l === '---')) refuse(id, 'the log text must not contain a heading or a --- line');
  if (words(text) > BUDGET.logEntry) refuse(id, `the log text is ${words(text)} words; one entry is at most ${BUDGET.logEntry}`);
  const at = iso(now());
  appendToLog(t, o.section, `${at} ${text}`);
  saveTask(t);
  appendEvent(plan, { at, task: id, event: 'log', section: o.section });
}

function cmdBrief(root, o, [id]) {
  if (!id) refuse('brief', 'usage: brief <id>');
  const plan = openPlan(root, o.plan);
  const t = taskById(plan, id);
  const parts = [];
  const part = (abs, text) => parts.push(`<!-- orch brief: ${plan.rel(abs)} -->\n${text.replace(/\s+$/, '')}\n`);
  const spawned = ['builder', 'cheap'].includes(t.fm.agent);
  if (spawned) {
    const aabs = path.join(root, '.claude', 'agents', `${t.fm.agent}.md`);
    if (!exists(aabs)) refuse(plan.rel(aabs), `missing; the brief for agent ${t.fm.agent} starts with its definition`);
    // The agent definition's front matter (model, effort, tools) is for the harness, not the prompt: cut, never parsed.
    const atext = read(aabs);
    const close = atext.startsWith('---\n') ? atext.indexOf('\n---\n', 3) : -1;
    part(aabs, close < 0 ? atext : atext.slice(close + 5).replace(/^\n+/, ''));
    for (const abs of [path.join(plan.dir, 'CONTEXT.md'), path.join(t.feature.dir, 'CONTEXT.md')]) {
      if (!hasFile(path.dirname(abs), path.basename(abs))) refuse(plan.rel(abs), 'missing');
      part(abs, read(abs));
    }
  }
  part(t.abs, withoutLog(read(t.abs)));
  if (spawned) {
    const p = listPlans(root).length > 1 ? ` --plan ${plan.id}` : '';
    const orch = `cd ${root} && node ${TOOL}`;
    parts.push([
      '## Write-back',
      '- Record your work only through the tool, one paragraph per call:',
      `  ${orch} log ${id}${p} --section <built|why|deviations|gates|review|observed> --text "…"`,
      '- Never edit the front matter or the status; the orchestrator alone moves the task.',
      '- Anything outside Outputs is a new task, never a line in the PR:',
      `  ${orch} discover ${id}${p} --title "…"`,
      '- Report what you tested and what you observed, separately.',
      '',
    ].join('\n'));
  }
  const brief = parts.join('\n');
  const n = words(brief);
  if (n > BUDGET.brief) refuse(id, `the brief is ${n} words, over the ${BUDGET.brief}-word budget`);
  process.stdout.write(brief);
  process.stderr.write(`orch: brief ${id}: ${n} words\n`);
}

function nextProposedId(plan) {
  let n = 0;
  for (const id of plan.byId.keys()) { const m = /^P-(\d+)$/.exec(id); if (m) n = Math.max(n, Number(m[1])); }
  return `P-${n + 1}`;
}

function cmdDiscover(root, o, [from]) {
  if (!from) refuse('discover', 'usage: discover <from-id> --title "…" [--feature id] [--release id] [--repo name] [--agent name]');
  const plan = openPlan(root, o.plan);
  const src = taskById(plan, from);
  const v = {
    id: nextProposedId(plan), title: oneLine('title', o.title), plan: plan.id,
    feature: o.feature ?? src.fm.feature, release: o.release ?? src.fm.release, status: 'proposed',
    depends_on: [], discovered_from: from, repo: o.repo ?? src.fm.repo, agent: o.agent ?? src.fm.agent, size: 'M', zones: [],
  };
  checkNewFields(plan, v);
  const abs = scaffold(plan, v);
  appendEvent(plan, { at: iso(now()), task: v.id, event: 'status', from: null, to: 'proposed', discovered_from: from });
  console.log(plan.rel(abs));
}

function cmdNew(root, o, [id]) {
  if (!id) refuse('new', 'usage: new <id> --feature id --release id --title "…" [--repo name] [--agent builder] [--size M] [--zones Z2] [--depends-on a,b] [--next id]');
  const plan = openPlan(root, o.plan);
  if (!ID_RE.test(id)) refuse(id, 'not an id (letters, digits, "." and "-")');
  if (plan.byId.has(id)) refuse(id, `already exists: ${plan.byId.get(id).rel}`);
  for (const k of ['feature', 'release', 'title']) if (o[k] === undefined) refuse(`--${k}`, 'is required');
  const agent = o.agent ?? 'builder';
  const list = (s) => (s === undefined ? [] : s.split(',').map((x) => x.trim()).filter(Boolean));
  const v = {
    id, title: oneLine('title', o.title), plan: plan.id, feature: o.feature, release: o.release, status: 'todo',
    depends_on: list(o['depends-on']), repo: o.repo ?? (['owner', 'orchestrator'].includes(agent) ? 'none' : ''), agent,
    size: o.size ?? 'M', zones: list(o.zones), next: o.next ?? '',
  };
  if (!v.repo) refuse('--repo', `is required for agent ${agent}`);
  checkNewFields(plan, v);
  const e = [];
  if (!SIZES.includes(v.size)) e.push([`--size ${v.size}`, `not one of ${SIZES.join(', ')}`]);
  for (const z of v.zones) if (!ZONES.includes(z)) e.push([`--zones ${z}`, 'not one of Z1..Z8']);
  for (const d of v.depends_on) if (!plan.byId.has(d)) e.push([`--depends-on ${d}`, 'no such task']);
  if (v.next && !plan.byId.has(v.next)) e.push([`--next ${v.next}`, 'no such task']);
  refuseAll(e);
  console.log(plan.rel(scaffold(plan, v)));
}

const cell = (s) => String(s ?? '').replace(/\|/g, '\\|');
const OPEN_AGE = ['in_progress', 'in_review', 'merged', 'blocked'];
function indexText(plan, at, by) {
  const out = [`Generated by \`orch ${by}\` at ${iso(at)}. Do not edit by hand.`, '', `# Index: ${plan.pm.title || plan.id}`, '', '## Ready now'];
  const ready = frontier(plan, at);
  out.push(...(ready.length ? ready.map(({ t, stack }) => `- ${t.fm.id} (${t.fm.release}, ${t.fm.feature}, ${t.fm.repo}, ${t.fm.agent}) ${t.fm.title}${stack ? ` (stack on ${stack})` : ''}`) : ['- none']));
  const ordered = [...plan.tasks].sort((a, b) => natural(a.fm.id, b.fm.id));
  for (const r of plan.releases) {
    const rows = ordered.filter((t) => t.fm.release === r && t.fm.status !== 'proposed');
    out.push('', `## ${r}`, '', '| id | title | feature | status | agent | claimed_by | pr | age |', '|---|---|---|---|---|---|---|---|');
    for (const t of rows) {
      const f = t.fm;
      out.push(`| ${cell(f.id)} | ${cell(f.title)} | ${cell(f.feature)} | ${f.status} | ${f.agent} | ${cell(f.claimed_by)} | ${cell(f.pr)} | ${OPEN_AGE.includes(f.status) ? age(f.started_at, at) : ''} |`);
    }
  }
  out.push('', '## Proposed', '', '| id | title | feature | release | discovered_from |', '|---|---|---|---|---|');
  for (const t of ordered.filter((x) => x.fm.status === 'proposed')) out.push(`| ${cell(t.fm.id)} | ${cell(t.fm.title)} | ${cell(t.fm.feature)} | ${cell(t.fm.release)} | ${cell(t.fm.discovered_from)} |`);
  out.push('', '## Blocked', '', '| id | title | release | blocked_reason |', '|---|---|---|---|');
  for (const t of ordered.filter((x) => x.fm.status === 'blocked')) out.push(`| ${cell(t.fm.id)} | ${cell(t.fm.title)} | ${cell(t.fm.release)} | ${cell(t.fm.blocked_reason)} |`);
  return `${out.join('\n')}\n`;
}
const STATUS_STYLE = {
  todo: 'fill:#f2f2f2,stroke:#888', in_progress: 'fill:#ffe08a,stroke:#b58900', in_review: 'fill:#ffc98a,stroke:#cb4b16',
  merged: 'fill:#c6e2ff,stroke:#268bd2', done: 'fill:#b7e4c7,stroke:#2d6a4f', blocked: 'fill:#f4a6a6,stroke:#dc322f',
  dropped: 'fill:#dddddd,stroke:#999,stroke-dasharray:4', proposed: 'fill:#e6dcff,stroke:#6c71c4',
};
function graphText(plan, at, by) {
  const ordered = [...plan.tasks].sort((a, b) => natural(a.fm.id, b.fm.id));
  const node = new Map();
  const used = new Set();
  for (const t of ordered) {
    let n = t.fm.id.replace(/[^A-Za-z0-9_]/g, '_');
    while (used.has(n)) n += '_';
    used.add(n);
    node.set(t.fm.id, n);
  }
  const label = (s) => s.replace(/#/g, '#35;').replace(/"/g, '#quot;').replace(/</g, '#lt;').replace(/>/g, '#gt;');
  const out = [`Generated by \`orch ${by}\` at ${iso(at)}. Do not edit by hand.`, '', `# Graph: ${plan.pm.title || plan.id}`, '', '```mermaid', 'flowchart LR'];
  for (const t of ordered) out.push(`  ${node.get(t.fm.id)}["${label(`${t.fm.id} ${t.fm.title}`)}"]`);
  for (const t of ordered) for (const d of Array.isArray(t.fm.depends_on) ? t.fm.depends_on : []) if (node.has(d)) out.push(`  ${node.get(d)} --> ${node.get(t.fm.id)}`);
  for (const s of STATUSES) out.push(`  classDef ${s} ${STATUS_STYLE[s]}`);
  for (const s of STATUSES) {
    const ids = ordered.filter((t) => t.fm.status === s).map((t) => node.get(t.fm.id));
    if (ids.length) out.push(`  class ${ids.join(',')} ${s}`);
  }
  out.push('```', '');
  return out.join('\n');
}
function cmdIndex(root, o) {
  const plan = openPlan(root, o.plan);
  const at = now();
  const ia = path.join(plan.dir, 'INDEX.md');
  const ga = path.join(plan.dir, 'GRAPH.md');
  writeAtomic(ia, indexText(plan, at, 'index'));
  writeAtomic(ga, graphText(plan, at, 'index'));
  console.log(plan.rel(ia));
  console.log(plan.rel(ga));
}
function cmdGraph(root, o) {
  const plan = openPlan(root, o.plan);
  const ga = path.join(plan.dir, 'GRAPH.md');
  writeAtomic(ga, graphText(plan, now(), 'graph'));
  console.log(plan.rel(ga));
}

function percentile(xs, p) {
  if (!xs.length) return null;
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.max(0, Math.ceil((p / 100) * s.length) - 1)];
}
function computeMetrics(plan, release, at) {
  const tasks = plan.tasks.filter((t) => !release || t.fm.release === release);
  const ids = new Set(tasks.map((t) => t.fm.id));
  const all = readEvents(plan);
  const evs = all.filter((e) => ids.has(e.task));
  const st = (s) => tasks.filter((t) => s.includes(t.fm.status)).length;
  const status = evs.filter((e) => e.event === 'status');
  const byTask = (id) => status.filter((e) => e.task === id);
  const cycles = [];
  const rounds = [];
  let blockedH = 0;
  for (const t of tasks) {
    const es = byTask(t.fm.id);
    const claim = es.find((e) => e.to === 'in_progress' && e.from === 'todo');
    const done = es.filter((e) => e.to === 'done').pop();
    if (claim && done) cycles.push(hoursBetween(claim.at, done.at));
    if (es.some((e) => e.to === 'merged')) rounds.push(es.filter((e) => e.from === 'in_review' && e.to === 'in_progress').length);
    es.forEach((e, i) => {
      if (e.to !== 'blocked') return;
      const leave = es.slice(i + 1).find((x) => x.from === 'blocked');
      blockedH += hoursBetween(e.at, leave ? leave.at : iso(at));
    });
  }
  const doneAt = (id) => { const d = status.filter((e) => e.task === id && e.to === 'done').pop() || all.filter((e) => e.task === id && e.to === 'done').pop(); return d ? d.at : null; };
  const start = all.length ? all[0].at : null;
  let ownerWait = 0;
  let ownerN = 0;
  for (const t of tasks.filter((x) => x.fm.agent === 'owner')) {
    const deps = Array.isArray(t.fm.depends_on) ? t.fm.depends_on : [];
    const depDone = deps.map(doneAt);
    if (depDone.some((d) => !d)) continue;
    const marks = [...depDone, start, t.fm.not_before ? `${t.fm.not_before}T00:00:00Z` : null].filter(Boolean);
    if (!marks.length) continue;
    const entry = marks.reduce((a, b) => (new Date(a) > new Date(b) ? a : b));
    const end = doneAt(t.fm.id) || iso(at);
    if (new Date(end) > new Date(entry)) { ownerWait += hoursBetween(entry, end); ownerN++; }
  }
  const h = (x) => (x === null ? '-' : x.toFixed(1));
  return [
    ['tasks planned', st(STATUSES.filter((s) => s !== 'proposed'))],
    ['in progress (in_progress, in_review, merged)', st(['in_progress', 'in_review', 'merged'])],
    ['blocked', st(['blocked'])],
    ['done', st(['done'])],
    ['dropped', st(['dropped'])],
    ['proposed', st(['proposed'])],
    ['cycle time claim->done p50 (h)', h(percentile(cycles, 50))],
    ['cycle time claim->done p95 (h)', h(percentile(cycles, 95))],
    ['review rounds per merged task (mean)', rounds.length ? (rounds.reduce((a, b) => a + b, 0) / rounds.length).toFixed(2) : '-'],
    ['review rounds, max', rounds.length ? Math.max(...rounds) : '-'],
    ['gate events (orch log --section gates)', evs.filter((e) => e.event === 'log' && e.section === 'gates').length],
    ['hours blocked', blockedH.toFixed(1)],
    [`owner-zone wait (h, ${ownerN} owner tasks on the frontier)`, ownerWait.toFixed(1)],
  ];
}
function cmdMetrics(root, o) {
  const plan = openPlan(root, o.plan);
  if (o.release && !plan.releases.includes(o.release)) refuse(`--release ${o.release}`, `not one of PLAN.md's releases (${plan.releases.join(', ')})`);
  const at = now();
  const rows = computeMetrics(plan, o.release, at);
  if (o.md) {
    const abs = path.join(plan.dir, 'METRICS.md');
    const md = [`Generated by \`orch metrics\` at ${iso(at)}. Do not edit by hand.`, '', `# Metrics: ${plan.pm.title || plan.id}${o.release ? `, ${o.release}` : ''}`, '', '| measure | value |', '|---|---|', ...rows.map(([k, v]) => `| ${k} | ${v} |`), ''];
    writeAtomic(abs, md.join('\n'));
    console.log(plan.rel(abs));
    return;
  }
  const w = Math.max(...rows.map(([k]) => k.length));
  for (const [k, v] of rows) console.log(`${k.padEnd(w)}  ${v}`);
}

function cmdResume(root, o) {
  const plan = openPlan(root, o.plan);
  const at = now();
  const events = readEvents(plan);
  const lastEvent = new Map();
  // A backfill corrects the record; it is not work on the task, so it does not make a stale task fresh.
  const mergedAt = new Map();
  for (const e of events) {
    if (e.event !== 'backfill') lastEvent.set(e.task, e.at);
    if (e.event === 'status' && e.to === 'merged') mergedAt.set(e.task, e.at);
  }
  let hash = 'unreadable';
  try { hash = crypto.createHash('sha256').update(fs.readFileSync(plan.pm.source)).digest('hex') === plan.pm.source_sha256 ? 'matches' : 'differs'; } catch { /* stays unreadable */ }
  const problems = validatePlan(plan).length;
  const out = [
    `plan ${plan.id}; source_sha256 ${plan.pm.source_sha256 || '(unset)'}; the source now ${hash}`,
    problems ? `validate: ${problems} problems; run orch validate` : 'validate: ok',
  ];
  // Each list takes its title line and its rows, "none" when it has none. CAP is each list's floor: it always gets
  // min(its rows, CAP) lines, and the lines short lists leave go, in list order, to the lists over their cap. 3 lines
  // outside the lists, six titles and the caps' sum (31) make RESUME_LINES (40), so the floors always fit. A list
  // still cut ends with "... and N more", that line counted in its share.
  const CAP = { flight: 6, merged: 5, ready: 6, held: 3, waiting: 6, blocked: 5 };
  const lists = [];
  const section = (title, rows, key) => lists.push({ title, rows, key });
  const flight = plan.tasks.filter((t) => ['in_progress', 'in_review'].includes(t.fm.status)).sort((a, b) => natural(a.fm.id, b.fm.id));
  section('in flight', flight.map((t) => {
    const last = lastEvent.get(t.fm.id) || t.fm.started_at;
    const idle = last ? hoursBetween(last, iso(at)) : Infinity;
    return `${t.fm.id} ${t.fm.status} by ${t.fm.claimed_by || '-'} branch ${t.fm.branch || '-'} pr ${t.fm.pr || '-'} age ${age(t.fm.started_at, at) || '-'}${idle > STALE_HOURS ? ` stale (no event for ${age(last, at) || 'ever'})` : ''}`;
  }), 'flight');
  // A merged task waits on its observer, who is anyone but its claimer (validate refuses observed_by = claimed_by).
  const merged = plan.tasks.filter((t) => t.fm.status === 'merged').sort((a, b) => natural(a.fm.id, b.fm.id));
  section('merged', merged.map((t) => {
    const m = mergedAt.get(t.fm.id);
    return `${t.fm.id} pr ${t.fm.pr || '-'} merge ${t.fm.merge_sha || '-'} by ${t.fm.claimed_by || '-'} merged ${m ? `${age(m, at)} ago` : '(no merge event)'}; observer: not ${t.fm.claimed_by || '-'}`;
  }), 'merged');
  // ready, held and waiting are one triage, read from readiness: held is a todo task kept off the frontier by its
  // not_before alone; waiting is the next frontier, a todo task whose every unmet dependency is in flight, merged,
  // blocked or held, so it moves up when they do.
  const tri = triage(plan, at);
  const rOf = new Map(tri.map(({ t, r }) => [t.fm.id, r]));
  const ready = frontier(plan, at);
  section(`ready (${ready.length})`, ready.map(readyLine), 'ready');
  section('held', tri.filter(({ r }) => r.held).map(({ t, r }) => `${t.fm.id} not before ${r.held}`), 'held');
  const state = (dt) => (rOf.get(dt.fm.id)?.held ? `held until ${rOf.get(dt.fm.id).held}` : dt.fm.status);
  const settled = (dt) => ['in_progress', 'in_review', 'merged', 'blocked'].includes(dt.fm.status) || Boolean(rOf.get(dt.fm.id)?.held);
  const waiting = tri.filter(({ r }) => r.unmet && r.unmet.every(settled));
  section('waiting', waiting.map(({ t, r }) => `${t.fm.id} waits on ${r.unmet.map((dt) => `${dt.fm.id} (${state(dt)})`).join(', ')}`), 'waiting');
  const blocked = plan.tasks.filter((t) => t.fm.status === 'blocked');
  section('blocked', blocked.map((t) => `${t.fm.id} ${t.fm.blocked_reason}`), 'blocked');
  const need = (l) => Math.max(1, l.rows.length);
  const share = lists.map((l) => Math.min(need(l), CAP[l.key]));
  let spare = RESUME_LINES - out.length - 1 - lists.length - share.reduce((a, b) => a + b, 0);
  lists.forEach((l, i) => { const more = Math.min(spare, need(l) - share[i]); share[i] += more; spare -= more; });
  lists.forEach((l, i) => {
    out.push(`${l.title}:`);
    if (!l.rows.length) out.push('  none');
    const shown = l.rows.length > share[i] ? share[i] - 1 : l.rows.length;
    out.push(...l.rows.slice(0, shown).map((r) => `  ${r}`));
    if (l.rows.length > shown) out.push(`  ... and ${l.rows.length - shown} more`);
  });
  out.push(`proposed: ${plan.tasks.filter((t) => t.fm.status === 'proposed').length}`);
  console.log(out.slice(0, RESUME_LINES).join('\n'));
}

const COMMANDS = {
  validate: { run: cmdValidate, usage: 'validate [--plan id]', what: 'check every PLAN, FEATURE, CONTEXT and task file against the templates and the graph, and every field the tool writes against the task\'s events in metrics.jsonl; every plan when --plan is absent' },
  ready: { run: cmdReady, usage: 'ready [--plan id]', what: 'the frontier: todo, every dependency done (or in_review/merged on one branch of the same repo: "(stack on <branch>)"), not_before passed; by release, depth, id' },
  claim: { run: cmdClaim, usage: 'claim <id> --by <name> --branch <branch>', what: `todo -> in_progress for a frontier task, recording origin's default-branch tip as base_sha (forty zeros, empty_origin, when origin has no refs; refused without an origin remote); a stacked task takes its dependency's branch; at most ${BUILDERS} builder tasks in_progress (repo none is not counted), one per repository; --branch refused for repo none`, opts: ['by', 'branch'] },
  'not-before': { run: cmdNotBefore, usage: 'not-before <id> <YYYY-MM-DD|->', what: 'set or clear ("-") not_before on a todo or blocked task; ready holds the task until that day' },
  set: { run: cmdSet, usage: 'set <id> <status> [--pr url] [--merge-sha sha] [--observed-by name] [--reason text] [--by name]', what: `in_progress->in_review (--pr, every time), in_review->in_progress (--reason; refused while another task of its repository, or ${BUILDERS} builder tasks, are in_progress), in_review->merged (--merge-sha every time, on the default branch of origin and not already in base_sha), merged->done (--observed-by; the SHA re-checked on origin), todo/in_progress/in_review->blocked (--reason), blocked->todo (clears the claim), proposed->todo (--by owner), any->dropped (--reason)`, opts: ['pr', 'merge-sha', 'observed-by', 'reason', 'by'] },
  brief: { run: cmdBrief, usage: 'brief <id>', what: `the agent's prompt: agent definition, plan CONTEXT, feature CONTEXT, the task without its log, the write-back block; refused over ${BUDGET.brief} words` },
  log: { run: cmdLog, usage: 'log <id> --section <built|why|deviations|gates|review|observed> (--text "…" | --file path)', what: `append a timestamped paragraph to the Execution log (in_progress, in_review or merged; at most ${BUDGET.logEntry} words)`, opts: ['section', 'text', 'file'] },
  discover: { run: cmdDiscover, usage: 'discover <from-id> --title "…" [--feature id] [--release id] [--repo name] [--agent name]', what: 'create a proposed task P-<n> from the template; ready never offers it; set todo needs --by owner', opts: ['title', 'feature', 'release', 'repo', 'agent'] },
  new: { run: cmdNew, usage: 'new <id> --feature id --release id --title "…" [--repo name] [--agent builder] [--size M] [--zones Z2] [--depends-on a,b] [--next id]', what: 'scaffold a todo task from the template; the body placeholders stay for a person to fill', opts: ['feature', 'release', 'title', 'repo', 'agent', 'size', 'zones', 'depends-on', 'next'] },
  index: { run: cmdIndex, usage: 'index [--plan id]', what: 'write INDEX.md (ready now, one table per release, proposed, blocked) and GRAPH.md' },
  graph: { run: cmdGraph, usage: 'graph [--plan id]', what: 'write GRAPH.md only: the mermaid dependency graph coloured by status' },
  metrics: { run: cmdMetrics, usage: 'metrics [--plan id] [--release id] [--md]', what: 'counts, cycle time p50/p95, review rounds, gate events, hours blocked, owner-zone wait; --md writes METRICS.md', opts: ['release', 'md'] },
  resume: { run: cmdResume, usage: 'resume [--plan id]', what: `the state in at most ${RESUME_LINES} lines: in-flight tasks (stale after ${STALE_HOURS} h without an event); merged tasks waiting on an observer (not the claimer); the frontier; held (not_before ahead); waiting (every unmet dependency in flight, merged, blocked or held, each named with its state); blocked; proposed` },
  help: { run: cmdHelp, usage: 'help', what: 'this text' },
};
function cmdHelp() {
  const lines = ['usage: node orchestration/bin/orch.mjs <command> [args]   (from anywhere inside the umbrella)', ''];
  for (const c of Object.values(COMMANDS)) lines.push(`  ${c.usage}`, `      ${c.what}`);
  lines.push('', 'Every command takes --plan <id> when orchestration/ holds several plans.',
    'claim, set and not-before run every validate check on the task\'s file before they act.',
    'A refusal exits 1 with lines "orch: <path or id>: <reason>"; an internal error exits 2.',
    'ORCH_NOW=<ISO-8601> overrides the clock (tests use it).');
  console.log(lines.join('\n'));
}

function main(argv) {
  const [name, ...rest] = argv;
  const c = COMMANDS[name || 'help'];
  if (!c) refuse(name, 'unknown command; run orch help');
  const options = { plan: { type: 'string' } };
  for (const k of c.opts || []) options[k] = { type: k === 'md' ? 'boolean' : 'string' };
  let parsed;
  try { parsed = parseArgs({ args: rest, options, allowPositionals: true, strict: true }); } catch (e) { refuse(name, e.message); }
  if (c.run === cmdHelp) return cmdHelp();
  c.run(findRoot(), parsed.values, parsed.positionals);
}

try {
  main(process.argv.slice(2));
} catch (e) {
  if (e instanceof Refusal) {
    for (const [s, r] of e.items) process.stderr.write(`orch: ${s}: ${r}\n`);
    process.exit(1);
  }
  process.stderr.write(`orch: internal error: ${e && e.stack ? e.stack : e}\n`);
  process.exit(2);
}
