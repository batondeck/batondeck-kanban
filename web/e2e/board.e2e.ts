// The board: create and open a task, edit its fields, move it between columns (drag and drop on the
// board, the status control on the ticket page), comment, upload an attachment; search and filters;
// and a change made over MCP, outside the browser, arriving on the open board over the live stream.
import type { Locator, Page } from '@playwright/test';
import { McpClient, seedProject, seedTask, type SeededBoard } from './support/mcp.js';
import { atRoute, expect, test } from './support/test.js';

const column = (page: Page, name: string): Locator =>
  page.locator('.columns > .column').filter({ has: page.locator('header > span', { hasText: new RegExp(`^${name}$`) }) });

const card = (scope: Page | Locator, title: string): Locator => scope.locator('.card[data-task-id]').filter({ hasText: title });

/** A field of the ticket's Details panel: the control right after its caption. */
const field = (page: Page, caption: string): Locator =>
  page.getByRole('complementary').locator('span.muted', { hasText: new RegExp(`^${caption}$`) }).locator('xpath=following-sibling::*[1]');

/** A pointer drag the way a person does it: press, travel past dnd-kit's 6 px activation, drop. */
async function drag(page: Page, from: Locator, to: Locator): Promise<void> {
  const a = await from.boundingBox();
  const b = await to.boundingBox();
  if (!a || !b) throw new Error('drag: an element has no box');
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  await page.mouse.move(a.x + a.width / 2 + 12, a.y + a.height / 2 + 4, { steps: 4 });
  await page.mouse.move(b.x + b.width / 2, b.y + 80, { steps: 24 });
  await page.mouse.up();
}

async function openBoard(page: Page, b: SeededBoard): Promise<void> {
  await page.goto(`/projects/${b.projectId}/boards/${b.boardId}`);
  await expect(page.getByRole('heading', { name: b.boardName })).toBeVisible();
  await expect(page.locator('.columns > .column')).toHaveCount(5);
}

test.describe('board', () => {
  let b: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Board flows');
  });

  test('create a task, open it, edit its fields, comment, attach a file, move it between columns', async ({ page }) => {
    const mcp = new McpClient('admin');
    await openBoard(page, b);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'empty board');

    // Create.
    await page.getByRole('button', { name: '+ New task' }).click();
    await page.getByLabel('Task title').fill('Write the release notes');
    await page.getByLabel('priority').selectOption('high');
    await page.getByLabel('Labels (comma-separated)').fill('docs, release');
    await page.getByRole('button', { name: 'Add task' }).click();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    const created = card(column(page, 'To do'), 'Write the release notes');
    // The optimistic card animates out as the stored one replaces it: one card once that is done.
    await expect(created).toHaveCount(1);
    await expect(created).toBeVisible();
    await expect(created.locator('.label')).toHaveText(['docs', 'release']);
    await expect(created).toHaveAttribute('data-task-id', /^T-\d+$/);
    const taskId = (await created.getAttribute('data-task-id'))!;
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'task created');

    // Open it in the drawer and edit the title and the description.
    await created.click();
    const drawer = page.getByRole('dialog', { name: `Task ${taskId}` });
    await expect(drawer.getByRole('heading', { name: 'Write the release notes' })).toBeVisible();
    await drawer.getByRole('button', { name: 'Edit', exact: true }).click();
    await drawer.locator('header input').fill('Write the 0.1.0 release notes');
    const description = drawer.locator('textarea').first();
    await description.fill('Cover the **web UI** and the MCP tools.');
    // The Description panel's Save, beside its textarea (the Memory panel has a Save of its own).
    await description.locator('xpath=following-sibling::button[1]').click();
    await expect(drawer.getByRole('heading', { name: 'Write the 0.1.0 release notes' })).toBeVisible();
    await expect(drawer.locator('.md strong')).toHaveText('web UI');

    // A comment.
    await drawer.getByPlaceholder('Write a comment…', { exact: false }).fill('Drafting this now.');
    await drawer.getByRole('button', { name: 'Comment', exact: true }).click();
    await expect(drawer.getByText('Drafting this now.')).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'drawer: edited and commented');

    // The server holds what the drawer shows.
    const fetched = await mcp.call<{ task: { title: string; description: string; priority: string; labels: string[] } }>('get_task', {
      projectId: b.projectId,
      taskId,
    });
    expect(fetched.task.title).toBe('Write the 0.1.0 release notes');
    expect(fetched.task.description).toBe('Cover the **web UI** and the MCP tools.');
    expect(fetched.task.priority).toBe('high');
    const comments = await mcp.call<{ comments: Array<{ body: string }> }>('list_comments', { projectId: b.projectId, taskId });
    expect(comments.comments.map((c) => c.body)).toContain('Drafting this now.');
    await drawer.getByRole('button', { name: 'Close' }).click();
    await expect(drawer).toBeHidden();

    // Drag it from To do to In progress. A moved card animates out of its old lane while it enters
    // the new one, so each step waits for the one card before it acts on it.
    const theCard = page.locator(`.card[data-task-id="${taskId}"]`);
    await expect(theCard).toHaveCount(1);
    await drag(page, theCard, column(page, 'In progress'));
    await expect(card(column(page, 'In progress'), 'Write the 0.1.0 release notes')).toBeVisible();
    await expect(card(column(page, 'In progress'), 'Write the 0.1.0 release notes')).not.toHaveAttribute('aria-busy', 'true');
    await expect
      .poll(async () => (await mcp.call<{ task: { status: string } }>('get_task', { projectId: b.projectId, taskId })).task.status)
      .toBe('IN_PROGRESS');
    await expect(theCard).toHaveCount(1);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'dragged to In progress');

    // The full ticket page: fields, an attachment, and a status move.
    await theCard.click();
    await page.getByRole('dialog', { name: `Task ${taskId}` }).getByRole('link', { name: 'Open full page' }).click();
    await expect(page.getByRole('heading', { name: 'Write the 0.1.0 release notes' })).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'ticket page');

    await field(page, 'Priority').selectOption('urgent');
    await expect
      .poll(async () => (await mcp.call<{ task: { priority: string } }>('get_task', { projectId: b.projectId, taskId })).task.priority)
      .toBe('urgent');
    const labelInput = field(page, 'Labels').locator('input');
    await labelInput.fill('web');
    await labelInput.press('Enter');
    await expect
      .poll(async () => (await mcp.call<{ task: { labels: string[] } }>('get_task', { projectId: b.projectId, taskId })).task.labels)
      .toEqual(['docs', 'release', 'web']);

    // Upload through the file input behind "Drop a file here, or click to browse".
    const png = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
      'base64',
    );
    const chooser = page.waitForEvent('filechooser');
    await page.getByRole('button', { name: /Drop a file/ }).click();
    await (await chooser).setFiles({ name: 'release-diagram.png', mimeType: 'image/png', buffer: png });
    await expect(page.getByText(/^image · /)).toBeVisible();
    await expect
      .poll(async () => (await mcp.call<{ attachments: Array<{ mimeType: string }> }>('list_attachments', { projectId: b.projectId, taskId })).attachments.length)
      .toBe(1);

    // Status: In progress -> Review on the ticket's own control.
    await page.getByLabel('Status').selectOption('REVIEW');
    await expect(page.getByLabel('Status')).toHaveValue('REVIEW');
    await expect
      .poll(async () => (await mcp.call<{ task: { status: string } }>('get_task', { projectId: b.projectId, taskId })).task.status)
      .toBe('REVIEW');
    // The screen scrolls inside its own container, which a full-page capture does not unroll.
    await page.getByLabel('Status').scrollIntoViewIfNeeded();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'fields edited, file attached, moved to Review');

    await page.getByRole('link', { name: 'Back to board' }).click();
    await expect(card(column(page, 'Review'), 'Write the 0.1.0 release notes')).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'back on the board, in Review');
  });

  test('search and filters narrow the board', async ({ page }) => {
    await seedTask(b, 'Fix the login redirect', { labels: ['auth'], priority: 'urgent' });
    await seedTask(b, 'Tune the claim race', { labels: ['perf'] });
    await seedTask(b, 'Document the DSN forms', { labels: ['docs'] });
    await openBoard(page, b);
    await expect(card(page, 'Fix the login redirect')).toBeVisible();
    await expect(card(page, 'Tune the claim race')).toBeVisible();

    await page.getByLabel('Filter by label').selectOption('perf');
    await expect(card(page, 'Tune the claim race')).toBeVisible();
    await expect(card(page, 'Fix the login redirect')).toBeHidden();
    await expect(card(page, 'Document the DSN forms')).toBeHidden();
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'filtered by label perf');
    await page.getByRole('button', { name: 'Clear', exact: true }).click();
    await expect(card(page, 'Fix the login redirect')).toBeVisible();

    await page.getByLabel('Filter by status').selectOption('TODO');
    await expect(card(page, 'Fix the login redirect')).toBeVisible();
    await expect(page.locator('.card[data-task-id]').filter({ hasText: 'release notes' })).toHaveCount(0);
    await page.getByRole('button', { name: 'Clear', exact: true }).click();

    await page.getByLabel('Search tasks').fill('login redirect');
    await expect(page.getByText(/^Search results \(whole project\): /)).toBeVisible();
    const results = page.locator('.section .row');
    await expect(results.filter({ hasText: 'Fix the login redirect' })).toBeVisible();
    await expect(results.filter({ hasText: 'Tune the claim race' })).toHaveCount(0);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'search results');
    await results.filter({ hasText: 'Fix the login redirect' }).click();
    await expect(page.getByRole('dialog', { name: /^Task T-/ }).getByRole('heading', { name: 'Fix the login redirect' })).toBeVisible();
  });

  test('a change made over MCP arrives on the open board without a reload', async ({ page }) => {
    const mcp = new McpClient('admin');
    await openBoard(page, b);
    let navigations = 0;
    page.on('framenavigated', (f) => {
      if (f === page.mainFrame()) navigations++;
    });

    const { task } = await mcp.call<{ task: { id: string; version: number } }>('create_task', {
      projectId: b.projectId,
      boardId: b.boardId,
      title: 'Filed by an agent over MCP',
      labels: ['live'],
    });
    const live = card(column(page, 'To do'), 'Filed by an agent over MCP');
    await expect(live).toBeVisible();
    await expect(live).toHaveAttribute('data-task-id', task.id);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'task created over MCP appears live');

    await mcp.call('update_task', { projectId: b.projectId, taskId: task.id, version: task.version, patch: { title: 'Renamed by the agent' } });
    await expect(page.locator(`.card[data-task-id="${task.id}"] .title`)).toHaveText('Renamed by the agent');

    const after = await mcp.call<{ task: { version: number } }>('get_task', { projectId: b.projectId, taskId: task.id });
    await mcp.call('move_task', { projectId: b.projectId, taskId: task.id, version: after.task.version, toStatus: 'IN_PROGRESS' });
    await expect(card(column(page, 'In progress'), 'Renamed by the agent')).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'renamed and moved over MCP, live');
    expect(navigations, 'the page was not reloaded').toBe(0);
  });
});
