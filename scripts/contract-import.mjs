#!/usr/bin/env node
// contract-import writes contract/tools.json from conductor's docs-site/mcp.json, and with --admin
// contract/admin-tools.json from an admin `tools/list` dump of the same build. Node built-ins only.
//
//   node scripts/contract-import.mjs <conductor>/docs-site/mcp.json [--admin <mcp.admin.json>]
//
// tools.json keeps each tool's name, inputSchema and outputSchema verbatim (sorted by name), the
// prompts' names and arguments and the resource templates' names and URI templates, and records the
// source: its path inside its git repository, that repository's HEAD and the file's sha256. The
// file must be unmodified at HEAD, or the recorded commit would not be the source.
//
// The admin dump is not in git (conductor's spec generator omits admin-only tools), so it is
// accepted only when every tool it shares with mcp.json is byte-identical to mcp.json's: then it
// came from the same build, and admin-tools.json records that build's commit. Only the tools in
// ADMIN_TOOLS are taken from it.
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ADMIN_TOOLS = ['list_audit_log'];
const OUT = join(dirname(fileURLToPath(import.meta.url)), '..', 'contract');

function fail(msg) {
  console.error(`contract-import: ${msg}`);
  process.exit(1);
}

const args = process.argv.slice(2);
const adminAt = args.indexOf('--admin');
const adminPath = adminAt >= 0 ? args[adminAt + 1] : undefined;
if (adminAt >= 0) args.splice(adminAt, 2);
if (args.length !== 1 || (adminAt >= 0 && !adminPath)) {
  fail('usage: contract-import.mjs <mcp.json> [--admin <mcp.admin.json>]');
}
const srcPath = args[0];

const git = (...a) => execFileSync('git', ['-C', dirname(srcPath), ...a], { encoding: 'utf8' }).trim();
let commit, repoPath;
try {
  commit = git('rev-parse', 'HEAD');
  repoPath = git('rev-parse', '--show-prefix') + basename(srcPath);
  if (git('status', '--porcelain', '--', basename(srcPath)) !== '') {
    fail(`${srcPath} differs from HEAD ${commit}; the recorded commit would not be its source`);
  }
} catch (e) {
  fail(`${srcPath} is not in a git repository: ${e.message}`);
}

const bytes = readFileSync(srcPath);
const spec = JSON.parse(bytes.toString('utf8'));
const byName = (a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
const tool = (t) => {
  if (!t.inputSchema || !t.outputSchema) fail(`tool ${t.name} lacks an input or output schema`);
  return { name: t.name, inputSchema: t.inputSchema, outputSchema: t.outputSchema };
};
const source = { repo: 'conductor', path: repoPath, commit, sha256: createHash('sha256').update(bytes).digest('hex') };
const write = (name, value) => writeFileSync(join(OUT, name), JSON.stringify(value, null, 2) + '\n');

write('tools.json', {
  source,
  tools: spec.tools.map(tool).sort(byName),
  prompts: spec.prompts.map((p) => ({ name: p.name, arguments: p.arguments ?? [] })).sort(byName),
  resourceTemplates: spec.resourceTemplates.map((r) => ({ name: r.name, uriTemplate: r.uriTemplate })).sort(byName),
});
console.log(`contract/tools.json: ${spec.tools.length} tools, ${spec.prompts.length} prompts, ${spec.resourceTemplates.length} resource templates, conductor@${commit.slice(0, 8)}`);

if (adminPath) {
  const adminBytes = readFileSync(adminPath);
  const admin = JSON.parse(adminBytes.toString('utf8'));
  const pub = new Map(spec.tools.map((t) => [t.name, JSON.stringify(t)]));
  let shared = 0;
  for (const t of admin.tools) {
    if (!pub.has(t.name)) continue;
    if (pub.get(t.name) !== JSON.stringify(t)) fail(`admin dump's ${t.name} differs from mcp.json's: not the same build`);
    shared++;
  }
  if (shared !== spec.tools.length) fail(`admin dump shares ${shared} of mcp.json's ${spec.tools.length} tools`);
  const picked = ADMIN_TOOLS.map((n) => admin.tools.find((t) => t.name === n) ?? fail(`admin dump lacks ${n}`));
  write('admin-tools.json', {
    source: {
      repo: 'conductor',
      commit,
      method: `admin tools/list of the build whose ${shared} public tools equal ${repoPath} byte for byte`,
      sha256: createHash('sha256').update(adminBytes).digest('hex'),
    },
    tools: picked.map(tool).sort(byName),
  });
  console.log(`contract/admin-tools.json: ${picked.map((t) => t.name).join(', ')} (admin dump shares ${shared} tools with mcp.json, all identical)`);
}
