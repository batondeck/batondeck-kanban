// The workspace screens: Agents (its MCP sessions, Connect MCP), Analytics and Reports over a seeded
// board, the Activity log's search, and Notifications (a mention made over MCP by another account,
// opened and marked read).
import { McpClient, seedProject, seedTask, type SeededBoard } from './support/mcp.js';
import { runState } from './support/state.js';
import { atRoute, expect, test } from './support/test.js';

test.describe('workspace', () => {
  let b: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Workspace', { members: { worker: 'worker' } });
    await seedTask(b, 'Rotate the signing key', { labels: ['ops'], priority: 'high' });
    await seedTask(b, 'Draft the incident review', { labels: ['ops'] });
    await seedTask(b, 'Benchmark the claim race', { labels: ['perf'] });
  });

  test('Agents lists the MCP sessions and opens Connect MCP', async ({ page }) => {
    await page.goto('/agents');
    await expect(page.getByRole('heading', { name: 'Agents' })).toBeVisible();
    // The seeding above connected over MCP as the admin, so the session log holds that client.
    await expect(page.getByText('e2e-admin').first()).toBeVisible();
    await atRoute(page, '/agents', 'sessions');

    await page.getByRole('button', { name: '+ Connect MCP' }).click();
    const dialog = page.getByRole('dialog', { name: 'Connect an MCP client' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(`${runState().baseURL}/mcp`).first()).toBeVisible();
    await dialog.getByRole('tab', { name: 'Cursor' }).click();
    await expect(dialog.getByRole('tabpanel', { name: 'Cursor' })).toBeVisible();
    await atRoute(page, '/agents', 'connect MCP dialog');
    await dialog.getByRole('button', { name: 'Close' }).click();
    await expect(dialog).toBeHidden();
  });

  test('Analytics and Reports count the picked project’s board', async ({ page }) => {
    await page.goto('/analytics');
    await page.getByLabel('Project', { exact: true }).selectOption({ label: b.projectName });
    await expect(page.getByRole('heading', { name: 'Workspace Telemetry' })).toBeVisible();
    await expect(page.locator('.chip', { hasText: b.boardName })).toBeVisible();
    const totalTasks = page.locator('.card').filter({ has: page.locator('.muted', { hasText: /^Total Tasks$/ }) });
    await expect(totalTasks.locator('.mono')).toHaveText('3');
    await atRoute(page, '/analytics', 'operations');

    await page.getByRole('link', { name: 'Reports' }).first().click();
    await expect(page.getByRole('heading', { name: 'Reports' })).toBeVisible();
    const tasksTile = page.locator('.tile').filter({ has: page.locator('.tile-k', { hasText: /^Tasks$/ }) });
    await expect(tasksTile.locator('.tile-v')).toHaveText('3');
    await atRoute(page, '/reports', 'three tasks');
  });

  test('Activity searches the tickets', async ({ page }) => {
    await page.goto('/activity');
    await page.getByLabel('Project', { exact: true }).selectOption({ label: b.projectName });
    await expect(page.getByRole('heading', { name: 'Workspace Log' })).toBeVisible();
    await atRoute(page, '/activity', 'feed');
    await page.getByLabel('Search tickets').fill('signing key');
    await expect(page.getByText('Rotate the signing key')).toBeVisible();
    await expect(page.getByText('Benchmark the claim race')).toBeHidden();
    await atRoute(page, '/activity', 'search results');
  });

  test('a mention made over MCP is a notification; it opens the task and is marked read', async ({ page }) => {
    const admin = runState().accounts.admin;
    const task = await seedTask(b, 'Review the rollout plan');
    await new McpClient('worker').call('add_comment', {
      projectId: b.projectId,
      taskId: task.id,
      body: `@${admin.login} can you review this before Friday?`,
    });

    await page.goto('/notifications');
    await expect(page.getByRole('heading', { name: 'Notifications' })).toBeVisible();
    const item = page.locator('button.card').filter({ hasText: task.id });
    await expect(item).toBeVisible();
    await expect(item.getByText('new')).toBeVisible();
    await expect(page.locator('.nav-badge')).toHaveText('1');
    await atRoute(page, '/notifications', 'one unread mention');

    await item.click();
    const drawer = page.getByRole('dialog', { name: `Task ${task.id}` });
    await expect(drawer).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'opened from the notification');
    await expect(page.locator('.nav-badge')).toHaveCount(0);
    await drawer.getByRole('button', { name: 'Close' }).click();
    await expect(drawer).toBeHidden();

    // A second mention, cleared with "Mark all read".
    await new McpClient('worker').call('add_comment', { projectId: b.projectId, taskId: task.id, body: `@${admin.login} ping` });
    await page.getByRole('link', { name: 'Notifications' }).click();
    await expect(page.locator('.nav-badge')).toHaveText('1');
    await page.getByRole('button', { name: 'Mark all read' }).click();
    await expect(page.locator('.nav-badge')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Mark all read' })).toBeDisabled();
    await atRoute(page, '/notifications', 'all read');
  });
});
