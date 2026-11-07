// Roles: a viewer and a worker of the same project, each signed in as themselves. Each sees what
// its tier allows and is refused the rest: the screen either says so (a closed control with its
// reason, the on-call refusal) or shows the server's refusal when the action is tried. A refusal of an
// action is also read back from the server (nothing was written), so "nothing happened on screen"
// cannot pass for "refused"; the on-call refusal is the screen's own and is checked on screen.
import type { Page } from '@playwright/test';
import { McpClient, seedProject, seedTask, type SeededBoard } from './support/mcp.js';
import { runState } from './support/state.js';
import { atRoute, expect, test } from './support/test.js';

let b: SeededBoard;
test.beforeAll(async () => {
  b = await seedProject('Roles', { members: { viewer: 'viewer', worker: 'worker' } });
});

const taskPath = (id: string): string => `/projects/${b.projectId}/boards/${b.boardId}/tasks/${id}`;

async function taskCount(): Promise<number> {
  const r = await new McpClient('admin').call<{ tasks: unknown[] }>('list_tasks', { projectId: b.projectId, boardId: b.boardId });
  return r.tasks.length;
}

async function commentBodies(taskId: string): Promise<string[]> {
  const r = await new McpClient('admin').call<{ comments: Array<{ body: string }> }>('list_comments', { projectId: b.projectId, taskId });
  return r.comments.map((c) => c.body);
}

/** On-call is a workspace-admin screen: no nav link, and the route itself says it is refused. */
async function oncallRefused(page: Page, who: string): Promise<void> {
  await page.goto('/projects');
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Notifications' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'On-call' })).toHaveCount(0);
  await page.goto('/oncall');
  await expect(page.getByTestId('permission-refused')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Not available to this account' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'On-call setup' })).toHaveCount(0);
  await atRoute(page, '/oncall', `refused to a ${who}`);
}

test.describe('as a viewer', () => {
  test.use({ role: 'viewer' });

  test('On-call is refused', async ({ page }) => {
    await oncallRefused(page, 'viewer');
  });

  test('the board and the task are read-only: status, claim, new task and comment are refused', async ({ page }) => {
    const task = await seedTask(b, 'Audit the retention policy');
    const before = await taskCount();

    await page.goto(`/projects/${b.projectId}/boards/${b.boardId}`);
    await expect(page.getByRole('heading', { name: b.boardName })).toBeVisible();
    await expect(page.locator(`.card[data-task-id="${task.id}"]`)).toBeVisible();

    // The board offers "+ New task"; the server refuses the viewer and the card does not stay.
    await page.getByRole('button', { name: '+ New task' }).click();
    await page.getByLabel('Task title').fill('A viewer tries to file this');
    await page.getByRole('button', { name: 'Add task' }).click();
    await expect(page.locator('.toast').first()).toBeVisible();
    await expect(page.locator('.card[data-task-id]').filter({ hasText: 'A viewer tries to file this' })).toHaveCount(0);
    expect(await taskCount()).toBe(before);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'viewer: new task refused');

    // The task page: the status control is closed and says why, and there is no Claim.
    await page.goto(taskPath(task.id));
    await expect(page.getByRole('heading', { name: 'Audit the retention policy' })).toBeVisible();
    await expect(page.getByLabel('Status')).toBeDisabled();
    await expect(page.getByText('Only a master can change the status.')).toBeVisible();
    await expect(page.getByText('A viewer cannot claim tasks.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Claim to work' })).toHaveCount(0);
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: status closed, no claim');

    // A comment is offered and refused by the server, which the composer shows.
    await page.getByPlaceholder('Write a comment…', { exact: false }).fill('A viewer comments');
    await page.getByRole('button', { name: 'Comment', exact: true }).click();
    await expect(page.locator('.error').filter({ hasText: /\S/ }).first()).toBeVisible();
    expect(await commentBodies(task.id)).not.toContain('A viewer comments');
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: comment refused');
  });

  test('Settings has no Users panel, and adding a member is refused', async ({ page }) => {
    const worker = runState().accounts.worker;
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    await page.locator('.topbar select').selectOption({ label: b.projectName });
    await expect(page.locator('#cli-tokens')).toBeVisible();
    await expect(page.locator('#users')).toHaveCount(0);

    await page.getByPlaceholder('teammate@example.com').fill(worker.login);
    await page.getByRole('button', { name: '+ Add Member' }).click();
    await expect(page.locator('p.error')).toBeVisible();
    const { project } = await new McpClient('admin').call<{ project: { members: Record<string, string> } }>('get_project', {
      projectId: b.projectId,
    });
    expect(project.members[worker.id]).toBe('worker');
    await atRoute(page, '/settings', 'viewer: no Users panel, add member refused');
  });
});

test.describe('as a worker', () => {
  test.use({ role: 'worker' });

  test('On-call is refused', async ({ page }) => {
    await oncallRefused(page, 'worker');
  });

  test('a worker claims and completes a task and comments, but cannot move its status', async ({ page }) => {
    const mcp = new McpClient('admin');
    const task = await seedTask(b, 'Patch the base image');
    await page.goto(taskPath(task.id));
    await expect(page.getByRole('heading', { name: 'Patch the base image' })).toBeVisible();
    await expect(page.getByLabel('Status')).toBeDisabled();
    await expect(page.getByText('Only a master can change the status.')).toBeVisible();

    await page.getByRole('button', { name: 'Claim to work' }).click();
    await expect(page.getByRole('button', { name: 'Mark complete' })).toBeVisible();
    await expect
      .poll(async () => (await mcp.call<{ task: { status: string; claim: unknown } }>('get_task', { projectId: b.projectId, taskId: task.id })).task.status)
      .toBe('IN_PROGRESS');
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'worker: claimed');

    await page.getByPlaceholder('Write a comment…', { exact: false }).fill('Base image rebuilt.');
    await page.getByRole('button', { name: 'Comment', exact: true }).click();
    await expect(page.getByText('Base image rebuilt.')).toBeVisible();
    expect(await commentBodies(task.id)).toContain('Base image rebuilt.');

    await page.getByRole('button', { name: 'Mark complete' }).click();
    await expect
      .poll(async () => (await mcp.call<{ task: { status: string } }>('get_task', { projectId: b.projectId, taskId: task.id })).task.status)
      .toBe('REVIEW');
    await expect(page.getByLabel('Status')).toHaveValue('REVIEW');
    await expect(page.getByRole('button', { name: 'Mark complete' })).toHaveCount(0);
    await expect(page.getByLabel('Status')).toBeDisabled();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'worker: completed, in Review');
  });
});
