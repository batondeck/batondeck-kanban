// Roles: a viewer and a worker of the same project, each signed in as themselves. Each sees what
// its tier allows and is refused the rest: the screen says so, with a closed control and its reason
// (P-432: "+ New task", Comment and "+ Add Member" join Status and Claim), or with the on-call
// refusal. A closed action is also read back from the server (nothing was written), so "nothing
// happened on screen" cannot pass for "refused"; the on-call refusal is checked on screen.
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

  test('the board and the task are read-only: status, claim, new task and comment are closed', async ({ page }) => {
    const task = await seedTask(b, 'Audit the retention policy');
    const before = await taskCount();

    await page.goto(`/projects/${b.projectId}/boards/${b.boardId}`);
    await expect(page.getByRole('heading', { name: b.boardName })).toBeVisible();
    await expect(page.locator(`.card[data-task-id="${task.id}"]`)).toBeVisible();

    // "+ New task" is closed and says why (create_task is tier master); its `c` shortcut opens nothing.
    const newTask = page.getByRole('button', { name: '+ New task' });
    await expect(newTask).toBeDisabled();
    await expect(page.getByText('Only a master can create tasks.')).toBeVisible();
    await expect(newTask).toHaveAccessibleDescription('Only a master can create tasks.');
    await page.locator('body').press('c');
    await expect(page.getByLabel('Task title')).toHaveCount(0);
    expect(await taskCount()).toBe(before);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'viewer: new task closed');

    // The task page: the status control is closed and says why, and there is no Claim.
    await page.goto(taskPath(task.id));
    await expect(page.getByRole('heading', { name: 'Audit the retention policy' })).toBeVisible();
    await expect(page.getByLabel('Status')).toBeDisabled();
    await expect(page.getByText('Only a master can change the status.')).toBeVisible();
    await expect(page.getByText('A viewer cannot claim tasks.')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Claim to work' })).toHaveCount(0);
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: status closed, no claim');

    // The composer and Comment are closed and say why (add_comment is tier write); nothing is posted.
    const commentsBefore = await commentBodies(task.id);
    await expect(page.getByPlaceholder('Write a comment…', { exact: false })).toBeDisabled();
    const comment = page.getByRole('button', { name: 'Comment', exact: true });
    await expect(comment).toBeDisabled();
    await expect(page.getByText('A viewer cannot comment.')).toBeVisible();
    await expect(comment).toHaveAccessibleDescription('A viewer cannot comment.');
    expect(await commentBodies(task.id)).toEqual(commentsBefore);
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: comment closed');
  });

  test('Settings has no Users panel, and members, columns and the review step are closed with their reason', async ({ page }) => {
    const worker = runState().accounts.worker;
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: 'Board Configuration' })).toBeVisible();
    await page.getByLabel('Project to configure').selectOption({ label: b.projectName });
    await expect(page.locator('#cli-tokens')).toBeVisible();
    await expect(page.locator('#users')).toHaveCount(0);

    // add_member and remove_member are tier admin, the columns write and structural, the review step admin.
    const addMember = page.getByRole('button', { name: '+ Add Member' });
    await expect(addMember).toBeDisabled();
    await expect(page.getByText('Only a project admin can add or remove members.')).toBeVisible();
    await expect(addMember).toHaveAccessibleDescription('Only a project admin can add or remove members.');
    await expect(page.getByLabel('New member login')).toBeDisabled();
    await expect(page.getByRole('button', { name: '+ Add Column' })).toBeDisabled();
    await expect(page.getByText('A viewer cannot change columns.')).toBeVisible();
    await expect(page.getByRole('switch', { name: 'Require review step' })).toBeDisabled();
    await expect(page.getByText('Only a project admin can change workflow settings.')).toBeVisible();
    const { project } = await new McpClient('admin').call<{ project: { members: Record<string, string> } }>('get_project', {
      projectId: b.projectId,
    });
    expect(project.members[worker.id]).toBe('worker');
    await atRoute(page, '/settings', 'viewer: no Users panel, members and columns closed');
  });
});

test.describe('as a worker', () => {
  test.use({ role: 'worker' });

  test('On-call is refused', async ({ page }) => {
    await oncallRefused(page, 'worker');
  });

  test('"+ New task" follows the role in each project: closed where a worker, open where a master', async ({ page }) => {
    // The same account is a master of a second project: the gate is the project's role, not the account's.
    const led = await seedProject('Led', { members: { worker: 'master' } });

    await page.goto(`/projects/${b.projectId}/boards/${b.boardId}`);
    await expect(page.getByRole('heading', { name: b.boardName })).toBeVisible();
    await expect(page.getByRole('button', { name: '+ New task' })).toBeDisabled();
    await expect(page.getByText('Only a master can create tasks.')).toBeVisible();

    await page.goto(`/projects/${led.projectId}/boards/${led.boardId}`);
    await expect(page.getByRole('heading', { name: led.boardName })).toBeVisible();
    const newTask = page.getByRole('button', { name: '+ New task' });
    await expect(newTask).toBeEnabled();
    await expect(page.getByText('Only a master can create tasks.')).toHaveCount(0);
    await newTask.click();
    await page.getByLabel('Task title').fill('Filed by a master');
    await page.getByRole('button', { name: 'Add task' }).click();
    // Stored first, then on screen. The optimistic card ("T-…") animates out as the stored one
    // replaces it, so a count taken before the stored card arrives can read 1 and then 2: wait for the
    // stored card by its id, then for the optimistic one to have gone.
    let storedId = '';
    await expect
      .poll(async () => {
        const r = await new McpClient('admin').call<{ tasks: Array<{ id: string; title: string }> }>('list_tasks', { projectId: led.projectId, boardId: led.boardId });
        storedId = r.tasks.find((t) => t.title === 'Filed by a master')?.id ?? '';
        return storedId;
      })
      .not.toBe('');
    await expect(page.locator(`.card[data-task-id="${storedId}"]`)).toBeVisible();
    await expect(page.locator('.card[data-task-id]').filter({ hasText: 'Filed by a master' })).toHaveCount(1);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'master: new task created');
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
    // The Review panel names the worker who completed it, never their account id (P-431).
    const worker = runState().accounts.worker;
    const completed = page.getByText(/· completed by /);
    await expect(completed).toContainText(`completed by ${worker.name}`);
    await expect(completed).not.toContainText(worker.id.slice(0, 6));
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'worker: completed, in Review');
  });
});
