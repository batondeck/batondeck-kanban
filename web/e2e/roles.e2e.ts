// Roles: a viewer and a worker of the same project, each signed in as themselves. Each sees what
// its tier allows and is refused the rest: the screen says so, with a closed control and its reason
// (P-432: "+ New task", Comment and "+ Add Member" join Status and Claim; P-440: every other write,
// from "+ New board" and "+ assign" to the file drop and the palette), or with the on-call
// refusal. A closed action is also read back from the server (nothing was written), so "nothing
// happened on screen" cannot pass for "refused"; the on-call refusal is checked on screen.
import type { Locator, Page } from '@playwright/test';
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

/**
 * Everything a closed task control could have written, read back as the admin: the task's version
 * (Edit and Details are update_task), its notes and links, memory, attachments, follow-ups, and the
 * project's boards. Equal before and after means nothing was written.
 */
async function written(taskId: string): Promise<unknown> {
  const mcp = new McpClient('admin');
  const at = { projectId: b.projectId, taskId };
  const [task, ctx, mem, att, fus, boards] = await Promise.all([
    mcp.call<{ task: { version: number } }>('get_task', at),
    mcp.call<{ items: Array<{ id: string }> }>('get_task_context', at),
    mcp.call<{ entries: Array<{ key: string }> }>('read_memory', at),
    mcp.call<{ attachments: Array<{ id: string }> }>('list_attachments', at),
    mcp.call<{ followUps: Array<{ id: string }> }>('list_follow_ups', at),
    mcp.call<{ boards: Array<{ id: string }> }>('list_boards', { projectId: b.projectId }),
  ]);
  return {
    version: task.task.version,
    items: ctx.items.map((i) => i.id),
    memory: mem.entries.map((e) => e.key),
    attachments: att.attachments.map((a) => a.id),
    followUps: fus.followUps.map((f) => f.id),
    boards: boards.boards.map((x) => x.id),
  };
}

/** The page title reads on one line (P-446): it was squeezed to a letter's width beside closed reasons. */
async function titleOnOneLine(page: Page): Promise<void> {
  const h1 = page.locator('.topbar-heading h1');
  const [height, line] = await h1.evaluate((e) => {
    const cs = getComputedStyle(e);
    const lh = parseFloat(cs.lineHeight);
    return [e.getBoundingClientRect().height, Number.isFinite(lh) ? lh : 1.4 * parseFloat(cs.fontSize)];
  });
  expect(height, `title "${await h1.innerText()}" is ${height}px tall, one line is ${line}px`).toBeLessThan(line * 1.5);
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

  test('a full header wraps its actions, not the title, and the columns still end at the viewport floor (P-446)', async ({ page }) => {
    // Two boards (so the board switcher shows) and a 1024 px window: the actions do not fit beside the
    // title. Before, the title shrank to a letter's width; now the actions wrap to a second row and
    // the columns, measured from where they start, still end at the bottom of the window.
    const p = await seedProject('Wrap', { members: { viewer: 'viewer' } });
    await new McpClient('admin').call('create_board', { projectId: p.projectId, name: 'Second board' });
    await seedTask(p, 'One task');
    await page.setViewportSize({ width: 1024, height: 768 });
    await page.goto(`/projects/${p.projectId}/boards/${p.boardId}`);
    await expect(page.getByRole('heading', { name: p.boardName })).toBeVisible();
    await expect(page.getByText('A viewer cannot create boards.')).toBeVisible();
    await expect(page.getByRole('combobox', { name: 'Switch board' })).toBeVisible();
    await expect(page.getByText('One task')).toBeVisible();
    await titleOnOneLine(page);
    const cols = await page.locator('.columns').last().boundingBox();
    expect(cols, 'the columns are laid out').not.toBeNull();
    const bottom = cols!.y + cols!.height;
    expect(bottom, `the columns end at ${bottom}px in a 768px window`).toBeLessThanOrEqual(768);
    expect(bottom, `the columns end at ${bottom}px in a 768px window`).toBeGreaterThan(768 - 40);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'viewer at 1024: header wraps, title whole');
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
    // The page scrolls inside its own container, so a full-page shot shows only what is scrolled to.
    await comment.scrollIntoViewIfNeeded();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: comment closed');
  });

  test('every other write is closed with its reason too, and nothing is written (P-440)', async ({ page }) => {
    const task = await seedTask(b, 'Rotate the audit keys');
    const before = await written(task.id);

    // The board: "+ New board", the card's "+ assign" and the sprint lifecycle are closed and say why.
    await page.goto(`/projects/${b.projectId}/boards/${b.boardId}`);
    await expect(page.getByRole('heading', { name: b.boardName })).toBeVisible();
    const card = page.locator(`.card[data-task-id="${task.id}"]`);
    await expect(card).toBeVisible();
    const newBoard = page.getByRole('button', { name: '+ New board' });
    await expect(newBoard).toBeDisabled();
    await expect(page.getByText('A viewer cannot create boards.')).toBeVisible();
    await expect(newBoard).toHaveAccessibleDescription('A viewer cannot create boards.');
    const cardsWhy = 'Only a master can assign, move or reprioritise tasks.';
    await expect(page.getByText(cardsWhy)).toBeVisible();
    await card.hover();
    const assign = card.getByRole('button', { name: 'assign', exact: true });
    await expect(assign).toBeDisabled();
    await expect(assign).toHaveAccessibleDescription(cardsWhy);
    await titleOnOneLine(page);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'viewer: new board and assign closed');

    // The palette lists Set status and Set priority, disabled, each beside its reason; `s` opens no list.
    await page.locator('body').press('j');
    await page.locator('body').press('s');
    // The palette's own list: the board's status filter has `<option>`s of the same names.
    const palette = page.locator('[cmdk-list]');
    const status = palette.getByRole('option', { name: /Set status/ });
    await expect(status).toBeVisible();
    await expect(status).toHaveAttribute('aria-disabled', 'true');
    await expect(status).toHaveAccessibleDescription('Only a master can change the status.');
    const priority = palette.getByRole('option', { name: /Set priority/ });
    await expect(priority).toHaveAttribute('aria-disabled', 'true');
    await expect(priority).toHaveAccessibleDescription('Only a master can change the priority.');
    await expect(palette.getByRole('option', { name: 'IN PROGRESS', exact: true })).toHaveCount(0);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'viewer: palette status and priority closed');
    await page.keyboard.press('Escape');

    // The task page: Edit, Details, a note, a link, memory, the file drop and Follow-ups.
    await page.goto(taskPath(task.id));
    await expect(page.getByRole('heading', { name: 'Rotate the audit keys' })).toBeVisible();
    const closed: Array<[Locator, string]> = [
      [page.getByRole('button', { name: 'Edit', exact: true }), 'Only a master can edit tasks.'],
      [page.getByLabel('Priority', { exact: true }), 'Only a master can edit details.'],
      [page.getByLabel('Assignee', { exact: true }), 'Only a master can edit details.'],
      [page.getByPlaceholder('Add a note…'), 'A viewer cannot add notes.'],
      [page.getByRole('button', { name: 'Attach', exact: true }), 'A viewer cannot attach links.'],
      [page.getByRole('button', { name: 'Save', exact: true }), 'A viewer cannot write memory.'],
      [page.getByRole('button', { name: /Drop a file/ }), 'A viewer cannot attach files.'],
      [page.getByPlaceholder(/Add a follow-up/), 'Only a master can send follow-ups.'],
      [page.getByRole('button', { name: 'Send', exact: true }), 'Only a master can send follow-ups.'],
    ];
    for (const [control, why] of closed) {
      await expect(control).toBeDisabled();
      await expect(control).toHaveAccessibleDescription(why);
      await expect(page.getByText(why, { exact: true }).first()).toBeVisible();
    }
    // A file dropped on the closed zone is ignored.
    const files = await page.evaluateHandle(() => {
      const dt = new DataTransfer();
      dt.items.add(new File(['x'], 'dropped.txt', { type: 'text/plain' }));
      return dt;
    });
    await page.getByRole('button', { name: /Drop a file/ }).dispatchEvent('drop', { dataTransfer: files });
    await page.getByRole('button', { name: /Drop a file/ }).scrollIntoViewIfNeeded();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: note, link, memory and file drop closed');
    await page.getByRole('button', { name: 'Send', exact: true }).scrollIntoViewIfNeeded();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: follow-ups closed');
    expect(await written(task.id)).toEqual(before);
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

// The follow-up's holder is the task's assignee; a viewer may hold one but not ack it (ack_follow_up is
// tier write). Ack is closed with its reason shown beside it and describing it, as every control P-440
// closes, not a hover title only (P-444). The follow-up stays open.
test.describe('as a viewer holding a follow-up', () => {
  test.use({ role: 'viewer' });

  test('Ack is closed, its reason shown and describing it, and the follow-up stays open (P-444)', async ({ page }) => {
    const { accounts } = runState();
    const task = await seedTask(b, 'Answer the auditor');
    const admin = new McpClient('admin');
    await admin.call('update_task', { projectId: b.projectId, taskId: task.id, version: task.version, patch: { assignee: accounts.viewer.id } });
    await admin.call('add_follow_up', { projectId: b.projectId, taskId: task.id, body: 'Cite the key rotation log.' });
    const status = async (): Promise<string[]> =>
      (await admin.call<{ followUps: Array<{ status: string }> }>('list_follow_ups', { projectId: b.projectId, taskId: task.id })).followUps.map((f) => f.status);
    expect(await status()).toEqual(['open']);

    await page.goto(taskPath(task.id));
    await expect(page.getByText('Cite the key rotation log.')).toBeVisible();
    const ack = page.getByRole('button', { name: 'Ack', exact: true });
    const why = 'A viewer cannot acknowledge follow-ups.';
    await expect(ack).toBeDisabled();
    await expect(ack).toHaveAccessibleDescription(why);
    await expect(page.getByText(why)).toBeVisible();
    await ack.click({ force: true });
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'viewer: ack closed, says why');
    expect(await status()).toEqual(['open']);
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

  test('a worker keeps the tier-write controls: a note is stored, while Edit stays closed (P-440)', async ({ page }) => {
    const task = await seedTask(b, 'Note the cache keys');
    await page.goto(taskPath(task.id));
    await expect(page.getByRole('heading', { name: 'Note the cache keys' })).toBeVisible();
    const edit = page.getByRole('button', { name: 'Edit', exact: true });
    await expect(edit).toBeDisabled();
    await expect(edit).toHaveAccessibleDescription('Only a master can edit tasks.');
    await expect(page.getByRole('button', { name: /Drop a file/ })).toBeEnabled();
    await expect(page.getByText('A viewer cannot add notes.')).toHaveCount(0);
    await page.getByPlaceholder('Add a note…').fill('Keys are per tenant.');
    await page.getByPlaceholder('Add a note…').press('Enter');
    await expect
      .poll(async () => {
        const r = await new McpClient('admin').call<{ items: Array<{ body: string }> }>('get_task_context', { projectId: b.projectId, taskId: task.id });
        return r.items.map((i) => i.body);
      })
      .toContain('Keys are per tenant.');
    await expect(page.getByText('Keys are per tenant.')).toBeVisible();
    await atRoute(page, '/projects/:projectId/boards/:boardId/tasks/:taskId', 'worker: note stored, edit closed');
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

// A workspace admin opening, by URL, a project they are not a member of (P-443). `list_projects`
// answers the caller's own projects, so the roster has no row for it and the server refuses every
// read and write there. The controls the board still draws are closed and say why ("not a member"),
// where before they stayed on "Checking your role" for good. Nothing is written.
test.describe('as a workspace admin who is not a member of the project', () => {
  test('the board says "not a member" on every closed control, never "Checking your role", and nothing is written', async ({ page }) => {
    const worker = new McpClient('worker');
    const { projectId } = await worker.call<{ projectId: string }>('create_project', { name: `Not mine ${Date.now().toString(36)}` });
    const { board } = await worker.call<{ board: { id: string } }>('create_board', { projectId, name: 'Worker board' });
    const boards = async (): Promise<string[]> =>
      (await worker.call<{ boards: Array<{ id: string }> }>('list_boards', { projectId })).boards.map((x) => x.id);
    const before = await boards();

    await page.goto(`/projects/${projectId}/boards/${board.id}`);
    const why = (what: string): string => `You are not a member of this project, so you cannot ${what}.`;
    const newBoard = page.getByRole('button', { name: '+ New board' });
    await expect(page.getByText(why('create boards'))).toBeVisible();
    await expect(newBoard).toBeDisabled();
    await expect(newBoard).toHaveAccessibleDescription(why('create boards'));
    await expect(page.getByRole('button', { name: '+ New task' })).toBeDisabled();
    await expect(page.getByText(why('create tasks'))).toBeVisible();
    await expect(page.getByText(why('assign, move or reprioritise tasks'))).toBeVisible();
    await expect(page.getByText(/Checking your role/)).toHaveCount(0);
    await expect(page.locator('[title="Checking your role"]')).toHaveCount(0);
    await newBoard.click({ force: true });
    // The board body names membership too, not "Couldn't load this board." (P-445), and the title
    // stays whole beside the header's two reasons (P-446).
    await expect(page.getByText('You are not a member of this project, so its board cannot be shown. A project admin can add you.')).toBeVisible();
    await expect(page.getByText('Couldn’t load this board.')).toHaveCount(0);
    await titleOnOneLine(page);
    await atRoute(page, '/projects/:projectId/boards/:boardId', 'admin, not a member: closed, says why');
    expect(await boards()).toEqual(before);
  });
});
