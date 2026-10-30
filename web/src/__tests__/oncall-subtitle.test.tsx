/**
 * THE ON-CALL SUBTITLE PROMISES ONLY WHAT INGEST DOES (P-342).
 *
 * It said incidents are "worked to a PR". P-310 removed the integration `policy` input that carried
 * `openPrForReview`, and nothing in the binary asks an agent to open one: a new incident is a TODO
 * ticket assigned by the integration's routing (`oncall`'s on-call agent, `upstream`'s mapped
 * assignee) or, with no assignee, broadcast to the project (`internal/service/oncall.go`
 * `incidentAssignee`). The rendered header is asserted, not the source text.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup } from '@testing-library/react';
import type { ReactNode } from 'react';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

describe('the /oncall header', () => {
  it('says what routing does and promises no PR', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ projects: [] }) }) as Response),
    );
    const { default: Oncall } = await import('../screens/Oncall.js');
    render(<Oncall />, { wrapper });
    const header = (await screen.findByRole('heading', { name: 'On-call' })).closest('.topbar');
    expect(header).not.toBeNull();
    expect(header!.textContent).toContain(
      "Telemetry webhooks become incident tickets, assigned by each integration's routing or broadcast to the project's agents.",
    );
    expect(header!.textContent, 'nothing asks an agent to open a PR').not.toMatch(/\bPRs?\b|pull request/i);
  });
});
