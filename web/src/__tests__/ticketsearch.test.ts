/** Ticket-search query parsing/filtering + smart-link app detection. */
import { describe, it, expect } from 'vitest';
import { parseTicketQuery, matchesFilters, matchesText, hasFilters, composeResults, descriptionSnippet, looksLikeTaskId } from '../ticketSearch.js';
import { appForUrl, normalizeLink } from '../AppLink.js';
import { toolByAgent, displayAgentName } from '../agentStatus.js';
import { UI_DEFAULTS, hydrateUiSettings, uiSettings } from '../settings.js';
import type { AgentSession, Task } from '../types.js';

const task = (over: Partial<Task> = {}): Task =>
  ({
    id: 'T-1',
    projectId: 'p',
    boardId: 'b',
    columnId: 'c',
    title: 'Fix auth flow',
    description: 'OAuth callback bug',
    status: 'IN_PROGRESS',
    priority: 'high',
    assignee: 'claude-pixpoc-main',
    labels: ['web', 'auth'],
    requiredCapabilities: [],
    estimate: null,
    dueAt: null,
    customFields: {},
    parentTaskId: null,
    order: 'a0',
    blockedBy: [],
    blocks: [],
    contextSummary: '',
    version: 1,
    claim: null,
    createdBy: 'me',
    updatedAt: '2026-07-20T00:00:00Z',
    ...over,
  }) as Task;

describe('parseTicketQuery', () => {
  it('splits filters from free text', () => {
    const q = parseTicketQuery('status:in-progress priority:high,urgent label:web fix auth');
    expect(q.status).toEqual(['IN_PROGRESS']);
    expect(q.priority).toEqual(['high', 'urgent']);
    expect(q.label).toEqual(['web']);
    expect(q.text).toBe('fix auth');
    expect(hasFilters(q)).toBe(true);
  });

  it('normalizes status aliases; an unknown status is KEPT so it matches nothing (not select-all)', () => {
    expect(parseTicketQuery('status:wip').status).toEqual(['IN_PROGRESS']);
    expect(parseTicketQuery('status:todo').status).toEqual(['TODO']);
    // No cancelled state (ADR-0001: a cancelled task is DONE with the `cancelled` label), so the old
    // alias is an unknown status now, kept as typed so it matches nothing.
    expect(parseTicketQuery('status:canceled').status).toEqual(['CANCELED']);
    expect(parseTicketQuery('status:bogus').status).toEqual(['BOGUS']);
    expect(matchesFilters(task(), parseTicketQuery('status:bogus'))).toBe(false);
  });

  it('substitutes assignee:me with the caller identity', () => {
    expect(parseTicketQuery('assignee:me', 'Alice@Example.com').assignee).toEqual(['alice@example.com']);
    expect(parseTicketQuery('assignee:me').assignee).toEqual(['me']); // no identity → literal
  });

  it('treats a plain query as pure text', () => {
    const q = parseTicketQuery('drawer revamp');
    expect(q.text).toBe('drawer revamp');
    expect(hasFilters(q)).toBe(false);
  });
});

describe('composeResults', () => {
  const a = task({ id: 'T-1', updatedAt: '2026-07-01T00:00:00Z' });
  const b = task({ id: 'T-2', title: 'Other work', status: 'DONE', updatedAt: '2026-07-02T00:00:00Z' });

  it('filter-only mode sorts the local list by recency and applies filters', () => {
    const q = parseTicketQuery('status:done');
    expect(composeResults(q, undefined, [a, b], 50).map((t) => t.id)).toEqual(['T-2']);
    const all = composeResults(parseTicketQuery('status:done,in_progress'), undefined, [a, b], 50);
    expect(all.map((t) => t.id)).toEqual(['T-2', 'T-1']); // newest first
  });

  it('free-text mode uses server results, augmented with local id matches for T-… queries', () => {
    expect(looksLikeTaskId('T-1')).toBe(true);
    expect(looksLikeTaskId('fix auth')).toBe(false);
    // Server search doesn't index ids → T-2 comes from the local list; no dupes when both know it.
    expect(composeResults(parseTicketQuery('T-2'), [], [a, b], 50).map((t) => t.id)).toEqual(['T-2']);
    expect(composeResults(parseTicketQuery('T-2'), [b], [a, b], 50).map((t) => t.id)).toEqual(['T-2']);
  });

  it('caps results', () => {
    expect(composeResults(parseTicketQuery('status:done,in_progress'), undefined, [a, b], 1)).toHaveLength(1);
  });
});

describe('descriptionSnippet', () => {
  it('strips markdown syntax, keeps link text, collapses whitespace', () => {
    expect(descriptionSnippet('## Fix\n- **auth** `flow` per [docs](https://x.com)')).toBe('Fix - auth flow per docs');
    expect(descriptionSnippet('plain text')).toBe('plain text');
  });

  it('truncates long text with an ellipsis at the cap', () => {
    const s = descriptionSnippet('word '.repeat(50), 110);
    expect(s.length).toBeLessThanOrEqual(111);
    expect(s.endsWith('…')).toBe(true);
  });
});

describe('matchesFilters', () => {
  it('filters by status, priority, assignee substring, and labels', () => {
    const t = task();
    expect(matchesFilters(t, parseTicketQuery('status:in_progress'))).toBe(true);
    expect(matchesFilters(t, parseTicketQuery('status:done'))).toBe(false);
    expect(matchesFilters(t, parseTicketQuery('priority:high'))).toBe(true);
    expect(matchesFilters(t, parseTicketQuery('assignee:claude'))).toBe(true);
    expect(matchesFilters(t, parseTicketQuery('assignee:cursor'))).toBe(false);
    expect(matchesFilters(t, parseTicketQuery('label:web label:auth'))).toBe(true);
    expect(matchesFilters(t, parseTicketQuery('label:infra'))).toBe(false);
  });

  it('matches the lease-holding agent when there is no assignee', () => {
    const t = task({ assignee: null, claim: { owner: 'sa@x', agent: 'worthy-gayal', leaseId: 'l', expiresAt: '2099-01-01T00:00:00Z' } });
    expect(matchesFilters(t, parseTicketQuery('assignee:worthy'))).toBe(true);
  });
});

describe('matchesText', () => {
  it('requires every word somewhere in id/title/description/labels', () => {
    const t = task();
    expect(matchesText(t, 'fix oauth')).toBe(true);
    expect(matchesText(t, 'T-1')).toBe(true);
    expect(matchesText(t, 'fix nothing')).toBe(false);
    expect(matchesText(t, '')).toBe(true);
  });
});

describe('appForUrl', () => {
  it('recognizes known artifact apps', () => {
    expect(appForUrl('https://lucid.app/lucidchart/abc/edit')?.name).toBe('Lucidchart');
    expect(appForUrl('https://docs.google.com/document/d/1/edit')?.name).toBe('Google Docs');
    expect(appForUrl('https://docs.google.com/spreadsheets/d/1/edit')?.name).toBe('Google Sheets');
    expect(appForUrl('https://docs.google.com/presentation/d/1/edit')?.name).toBe('Google Slides');
    expect(appForUrl('https://www.figma.com/file/xyz')?.name).toBe('Figma');
    expect(appForUrl('https://acme.atlassian.net/wiki/spaces/X')?.name).toBe('Confluence');
    expect(appForUrl('https://acme.atlassian.net/browse/PROJ-1')?.name).toBe('Jira');
    expect(appForUrl('https://github.com/tech-sumit/batondeck')?.name).toBe('GitHub');
  });

  it('falls back to the hostname for unknown apps and rejects non-URLs', () => {
    expect(appForUrl('https://internal.example.com/spec')?.name).toBe('internal.example.com');
    expect(appForUrl('not a url')).toBeNull();
    expect(appForUrl('ftp://example.com/x')).toBeNull();
    expect(appForUrl('javascript:alert(1)')).toBeNull();
  });

  it('rejects userinfo spoofs and shows a spoof-safe display path', () => {
    expect(appForUrl('https://docs.google.com@evil.com/x')).toBeNull(); // would render as docs.google, link to evil
    expect(appForUrl('https://user:pw@figma.com/f')).toBeNull();
    expect(appForUrl('https://www.figma.com/file/xyz?tricky=https://docs.google.com')?.display).toBe('figma.com/file/xyz');
  });
});

describe('normalizeLink', () => {
  it('accepts full URLs, prefixes https:// for bare domains, rejects prose', () => {
    expect(normalizeLink('https://lucid.app/x')).toBe('https://lucid.app/x');
    expect(normalizeLink('lucid.app/x')).toBe('https://lucid.app/x');
    expect(normalizeLink('  figma.com/file/1 ')).toBe('https://figma.com/file/1');
    expect(normalizeLink('see the drive folder')).toBeNull();
    expect(normalizeLink('javascript:alert(1)')).toBeNull();
    expect(normalizeLink('')).toBeNull();
  });
});

describe('ui settings', () => {
  it('merges overrides over defaults and resets cleanly', () => {
    expect(uiSettings()).toEqual(UI_DEFAULTS);
    const merged = hydrateUiSettings({ searchK: 10, linkFavicons: false });
    expect(merged.searchK).toBe(10);
    expect(merged.linkFavicons).toBe(false);
    expect(merged.taskPageLimit).toBe(UI_DEFAULTS.taskPageLimit);
    expect(uiSettings().searchK).toBe(10); // module store hydrated
    hydrateUiSettings(undefined);
    expect(uiSettings()).toEqual(UI_DEFAULTS);
  });
});

describe('toolByAgent', () => {
  const sess = (over: Partial<AgentSession>): AgentSession =>
    ({
      sessionKey: 'k',
      agentId: 'a',
      agentName: 'worthy-gayal',
      identityId: 'me@x.com',
      kind: 'oauth_user',
      clientName: 'claude-code',
      clientVersion: '2.1.212',
      firstSeenAt: '2026-07-01T00:00:00Z',
      lastSeenAt: '2026-07-20T00:00:00Z',
      sessionCount: 1,
      requestCount: 1,
      toolCounts: {},
      ...over,
    }) as AgentSession;

  it('maps an agent name to its client tool, newest session winning', () => {
    const m = toolByAgent([
      sess({ clientName: 'cursor-vscode', lastSeenAt: '2026-07-01T00:00:00Z' }),
      sess({ clientName: 'claude-code', lastSeenAt: '2026-07-20T00:00:00Z' }),
    ]);
    expect(m.get('worthy-gayal')).toBe('claude');
  });

  it('skips sessions without a client name', () => {
    const m = toolByAgent([sess({ clientName: undefined })]);
    expect(m.size).toBe(0);
  });
});

describe('displayAgentName', () => {
  it('passes real names through', () => {
    expect(displayAgentName('worthy-gayal', 'claude-code')).toBe('worthy-gayal');
  });

  it('falls back to the client for unexpanded placeholders', () => {
    expect(displayAgentName('${BATONDECK_AGENT}', 'claude-code')).toBe('claude-code');
    expect(displayAgentName('{{agent}}', 'cursor-vscode')).toBe('cursor-vscode');
    expect(displayAgentName('%AGENT%')).toBe('agent');
  });

  it('treats blank/undefined as a placeholder', () => {
    expect(displayAgentName('   ')).toBe('agent');
    expect(displayAgentName(undefined, 'claude-code')).toBe('claude-code');
  });
});
