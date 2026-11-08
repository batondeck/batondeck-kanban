// Settings, as the workspace admin: a column added to the board, a member added and removed, the
// review step switched off, a CLI token minted (shown once) and revoked, and a local user added
// with its initial password shown once. Each change is read back from the server over MCP. Settings
// works on the sidebar's selected project, and its own picker moves the sidebar's (P-434); on "All
// projects" it asks for a project and stores nothing (P-442).
import type { Locator, Page } from '@playwright/test';
import { McpClient, seedProject, type SeededBoard } from './support/mcp.js';
import { runState } from './support/state.js';
import { atRoute, expect, test } from './support/test.js';

/** A Settings section: the `<section>` whose heading (h3 or panel title) is `title`. */
const section = (page: Page, title: string): Locator =>
  page.locator('section').filter({ has: page.getByText(title, { exact: true }) }).last();

test.describe('settings', () => {
  let b: SeededBoard;
  let other: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Settings');
    // A second project, so there is a selection to follow whichever spec runs first.
    other = await seedProject('Settings other');
  });

  test('on "All projects" Settings asks for a project, edits and stores nothing, and leaving it keeps "All projects"', async ({ page }) => {
    const before = await new McpClient('admin').call<{ project: unknown }>('get_project', { projectId: b.projectId });
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    const sidebar = page.getByRole('combobox', { name: 'Project', exact: true });
    const picker = page.getByLabel('Project to configure');
    // A fresh context starts on "All projects": the picker shows no project chosen, and the project
    // sections give way to the prompt. The personal sections stay.
    await expect(sidebar).toHaveValue('');
    await expect(picker).toHaveValue('');
    await expect(picker.locator('option:checked')).toHaveText('Pick a project');
    await expect(page.getByRole('heading', { name: 'Pick a project to configure' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Columns' })).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Members' })).toHaveCount(0);
    await expect(page.getByLabel('Board to configure')).toHaveCount(0);
    await expect(page.locator('#cli-tokens')).toBeVisible();
    // Nothing stored: the selection is still "All projects", in the sidebar and in the browser.
    await expect(sidebar).toHaveValue('');
    expect(await page.evaluate(() => localStorage.getItem('bd.projectId') ?? '')).toBe('');
    await atRoute(page, '/settings', 'All projects: pick a project to configure');

    // Leaving Settings leaves the sidebar where the reader left it.
    await page.getByRole('link', { name: 'Projects', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await expect(sidebar).toHaveValue('');
    expect(await page.evaluate(() => localStorage.getItem('bd.projectId') ?? '')).toBe('');
    // And nothing was edited.
    expect(await new McpClient('admin').call<{ project: unknown }>('get_project', { projectId: b.projectId })).toEqual(before);
  });

  test('Settings follows the sidebar project, and its picker moves the sidebar', async ({ page }) => {
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    const sidebar = page.getByRole('combobox', { name: 'Project', exact: true });
    const picker = page.getByLabel('Project to configure');
    await expect(page.getByRole('heading', { name: 'Pick a project to configure' })).toBeVisible();

    // Sidebar -> Settings.
    await sidebar.selectOption({ label: b.projectName });
    await expect(picker).toHaveValue(b.projectId);
    await expect(page.getByRole('heading', { name: 'Pick a project to configure' })).toHaveCount(0);
    await expect(page.getByLabel('Board to configure')).toHaveValue(b.boardId);
    await atRoute(page, '/settings', 'follows the sidebar project');
    await sidebar.selectOption({ label: other.projectName });
    await expect(picker).toHaveValue(other.projectId);
    await expect(page.getByLabel('Board to configure')).toHaveValue(other.boardId);

    // Settings -> sidebar, from "All projects" too.
    await picker.selectOption({ label: b.projectName });
    await expect(sidebar).toHaveValue(b.projectId);
    await expect(page.getByLabel('Board to configure')).toHaveValue(b.boardId);
    await atRoute(page, '/settings', 'its picker moved the sidebar');
    await sidebar.selectOption({ value: '' });
    await expect(page.getByRole('heading', { name: 'Pick a project to configure' })).toBeVisible();
    await expect(picker).toHaveValue('');
    await picker.selectOption({ label: other.projectName });
    await expect(sidebar).toHaveValue(other.projectId);
    expect(await page.evaluate(() => localStorage.getItem('bd.projectId'))).toBe(other.projectId);
  });

  test('columns, members, workflow, CLI tokens and users', async ({ page }) => {
    const mcp = new McpClient('admin');
    const viewer = runState().accounts.viewer;
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    // Settings' own project picker, in the top bar, is named apart from the sidebar's (P-433).
    await page.getByLabel('Project to configure').selectOption({ label: b.projectName });
    await expect(page.getByLabel('Board to configure')).toHaveValue(b.boardId);
    await expect(page.getByLabel('Name of column To do')).toHaveValue('To do');
    // Every control on the screen has an accessible name, so none has to be reached by structure.
    const controls = page.locator('.topbar, .page').locator('input, select, textarea, button, [role="switch"]');
    expect(await controls.count()).toBeGreaterThan(20);
    for (let i = 0; i < (await controls.count()); i++) await expect(controls.nth(i)).toHaveAccessibleName(/\S/);
    await atRoute(page, '/settings', 'project picked');

    // Columns: a sixth column, mapped to Review.
    await page.getByLabel('New column name').fill('QA');
    await page.getByLabel('New column status').selectOption('REVIEW');
    await page.getByRole('button', { name: '+ Add Column' }).click();
    await expect(page.getByLabel('Name of column QA')).toHaveValue('QA');
    await expect(page.getByLabel('WIP limit of column QA')).toHaveValue('');
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
    await page.getByLabel('New member login').fill(viewer.login);
    await page.getByLabel('New member role').selectOption('viewer');
    await page.getByRole('button', { name: '+ Add Member' }).click();
    const row = members.locator('tbody tr').filter({ hasText: viewer.id });
    await expect(row).toBeVisible();
    await expect(row.locator('.role-badge')).toHaveText('viewer');
    await expect(row).toContainText(viewer.name);
    await atRoute(page, '/settings', 'column and member added');
    await page.getByRole('button', { name: `Remove ${viewer.name}` }).click();
    await expect(row).toHaveCount(0);
    await expect
      .poll(async () => Object.keys((await mcp.call<{ project: { members: Record<string, string> } }>('get_project', { projectId: b.projectId })).project.members))
      .not.toContain(viewer.id);

    // Workflow: switch the review step off. The switch is named by its caption (P-433).
    const review = page.getByRole('switch', { name: 'Require review step' });
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
