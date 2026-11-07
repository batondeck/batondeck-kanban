// Settings, as the workspace admin: a column added to the board, a member added and removed, the
// review step switched off, a CLI token minted (shown once) and revoked, and a local user added
// with its initial password shown once. Each change is read back from the server over MCP.
import type { Locator, Page } from '@playwright/test';
import { McpClient, seedProject, type SeededBoard } from './support/mcp.js';
import { runState } from './support/state.js';
import { atRoute, expect, test } from './support/test.js';

/** A Settings section: the `<section>` whose heading (h3 or panel title) is `title`. */
const section = (page: Page, title: string): Locator =>
  page.locator('section').filter({ has: page.getByText(title, { exact: true }) }).last();

test.describe('settings', () => {
  let b: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Settings');
  });

  test('columns, members, workflow, CLI tokens and users', async ({ page }) => {
    const mcp = new McpClient('admin');
    const viewer = runState().accounts.viewer;
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    // Settings has a project picker of its own in the top bar (it has no accessible name).
    await page.locator('.topbar select').selectOption({ label: b.projectName });
    await expect(page.getByLabel('Board to configure')).toHaveValue(b.boardId);
    await atRoute(page, '/settings', 'project picked');

    // Columns: a sixth column, mapped to Review.
    const columns = section(page, 'Columns');
    await columns.getByPlaceholder('New column name').fill('QA');
    await columns.locator('select').last().selectOption('REVIEW');
    await columns.getByRole('button', { name: '+ Add Column' }).click();
    await expect
      .poll(() => columns.locator('input.input:not([placeholder])').evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value)))
      .toContain('QA');
    await expect
      .poll(async () => {
        const r = await mcp.call<{ board: { columns: Array<{ name: string; status: string }> } }>('get_board', {
          projectId: b.projectId,
          boardId: b.boardId,
        });
        return r.board.columns.map((c) => `${c.name}:${c.status}`);
      })
      .toContain('QA:REVIEW');

    // Members: add the viewer account as a viewer, then remove it.
    const members = section(page, 'Members');
    await members.getByPlaceholder('teammate@example.com').fill(viewer.login);
    await members.locator('select').selectOption('viewer');
    await members.getByRole('button', { name: '+ Add Member' }).click();
    const row = members.locator('tbody tr').filter({ hasText: viewer.id });
    await expect(row).toBeVisible();
    await expect(row.locator('.role-badge')).toHaveText('viewer');
    await expect(row).toContainText(viewer.name);
    await atRoute(page, '/settings', 'column and member added');
    await row.getByRole('button', { name: 'Remove' }).click();
    await expect(row).toHaveCount(0);
    await expect
      .poll(async () => Object.keys((await mcp.call<{ project: { members: Record<string, string> } }>('get_project', { projectId: b.projectId })).project.members))
      .not.toContain(viewer.id);

    // Workflow: switch the review step off. The switch has no accessible name of its own; it is the
    // one in the "Require review step" panel.
    const review = page.locator('.record').filter({ hasText: 'Require review step' }).getByRole('switch');
    await expect(review).toHaveAttribute('aria-checked', 'true');
    await review.click();
    await expect(review).toHaveAttribute('aria-checked', 'false');
    await expect
      .poll(async () => (await mcp.call<{ project: { settings: { reviewEnabled: boolean } } }>('get_project', { projectId: b.projectId })).project.settings.reviewEnabled)
      .toBe(false);

    // CLI tokens: mint one (its value is shown once), close the reveal, revoke it.
    const tokens = page.locator('#cli-tokens');
    await tokens.scrollIntoViewIfNeeded();
    await tokens.getByLabel('Token label').fill('e2e laptop');
    await tokens.getByRole('button', { name: 'Mint token' }).click();
    await expect(tokens.getByLabel('Your new CLI token')).toHaveValue(/^bd_/);
    await expect(tokens.getByText('Copy this now — it will not be shown again.')).toBeVisible();
    await atRoute(page, '/settings', 'CLI token minted, shown once');
    await tokens.getByRole('button', { name: 'Done' }).click();
    await expect(tokens.getByLabel('Your new CLI token')).toHaveCount(0);
    const tokenRow = tokens.locator('tbody tr').filter({ hasText: 'e2e laptop' });
    await expect(tokenRow.locator('td').nth(1)).toHaveText('live');
    await tokenRow.getByRole('button', { name: 'Revoke' }).click();
    await expect(tokenRow.locator('td').nth(1)).toHaveText('revoked');
    await expect(tokenRow.getByRole('button', { name: 'Revoke' })).toHaveCount(0);

    // Users (workspace admin only): add a local account; its initial password is shown once.
    const login = `newcomer-${Date.now().toString(36)}@e2e.test`;
    const users = page.locator('#users');
    await users.scrollIntoViewIfNeeded();
    await users.getByLabel('New user login').fill(login);
    await users.getByLabel('New user display name').fill('Nia Newcomer');
    await users.getByRole('button', { name: 'Add user' }).click();
    const shown = users.getByRole('status');
    await expect(shown).toContainText(`Initial password for ${login}, shown once.`);
    await expect(shown.locator('code')).toHaveText(/\S{12,}/);
    await expect(users.getByRole('table', { name: 'Users' }).locator('tr').filter({ hasText: login })).toContainText('Nia Newcomer');
    await atRoute(page, '/settings', 'token revoked, user added');
    await shown.getByRole('button', { name: 'Done' }).click();
    await expect(shown).toHaveCount(0);
  });
});
