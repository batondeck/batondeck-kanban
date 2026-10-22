/**
 * The composer can only OPEN an agent if `parseAgentFile` is a true inverse of `renderAgentFile`.
 * If it is not, editing a file silently drops whatever the parser failed to understand — the author
 * saves back a version with fields deleted and nothing says so. So the property under test is
 * identity, not "parses without throwing".
 */
import { describe, expect, it } from 'vitest';
import {
  parseAgentFile,
  renderAgentFile,
  qualifySkillRefs,
  type AgentConfig,
} from '../agent-frontmatter.js';

const PLUGIN = 'acme';

/** render → parse → render must be byte-identical for every config the builder can produce. */
function roundTrips(cfg: AgentConfig, body: string): void {
  const first = renderAgentFile(cfg, PLUGIN, body);
  const parsed = parseAgentFile(first);
  expect('error' in parsed ? parsed.error : '').toBe('');
  if ('error' in parsed) return;
  const again = renderAgentFile(
    { ...parsed.cfg, ...(parsed.skills.length > 0 ? { skills: parsed.skills } : {}) },
    PLUGIN,
    parsed.body,
  );
  expect(again).toBe(first);
}

describe('agent frontmatter round-trip', () => {
  it('the minimum agent survives', () => {
    roundTrips({ name: 'reviewer', description: 'Reviews things.' }, 'You review.');
  });

  it('every field the builder writes survives', () => {
    roundTrips(
      {
        name: 'release-auditor',
        description: 'Audits main before a release.',
        tools: ['Bash', 'Read', 'Grep'],
        disallowedTools: ['Write'],
        model: 'opus',
        skills: ['deploy', 'acme:audit'],
        memory: 'project',
        effort: 'high',
        maxTurns: 12,
        color: 'red',
      },
      '# Role\n\nYou audit.\n\n## When\n\n- before a release',
    );
  });

  it('a description YAML would reinterpret survives quoting', () => {
    // The three shapes yamlScalar exists for: colon-space, a TRAILING colon (which shipped agents
    // that failed to load), and a boolean-looking word.
    roundTrips({ name: 'a', description: 'Use this: carefully' }, 'x');
    roundTrips({ name: 'b', description: 'Use this for the following:' }, 'x');
    roundTrips({ name: 'c', description: 'true' }, 'x');
    roundTrips({ name: 'd', description: 'Has, a comma, and [brackets]' }, 'x');
  });

  it('skills come back QUALIFIED, which is what is on disk', () => {
    const out = parseAgentFile(
      renderAgentFile({ name: 'a', description: 'd', skills: ['deploy'] }, PLUGIN, 'x'),
    );
    expect('error' in out).toBe(false);
    if ('error' in out) return;
    expect(out.skills).toEqual([`${PLUGIN}:deploy`]);
    // …and re-qualifying an already-qualified ref is a no-op, so saving does not double the prefix.
    expect(qualifySkillRefs(out.skills, PLUGIN)).toEqual([`${PLUGIN}:deploy`]);
  });

  it('REFUSES what it does not understand rather than dropping it', () => {
    // A block list is valid YAML and NOT what this builder writes. Silently ignoring it would delete
    // the author's tools on the next save, which is the failure this refusal exists to prevent.
    const block = '---\nname: a\ndescription: d\ntools:\n  - Bash\n  - Read\n---\nbody\n';
    const out = parseAgentFile(block);
    expect('error' in out).toBe(true);

    expect('error' in parseAgentFile('no frontmatter at all')).toBe(true);
    expect('error' in parseAgentFile('---\ndescription: d\n---\nb')).toBe(true); // no name
  });
});
