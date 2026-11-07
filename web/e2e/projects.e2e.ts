// Projects: create a project from the grid, open it (its first board is made on open), and create
// boards from the Projects screen and from the board's own "+ New board".
import { atRoute, expect, test } from './support/test.js';

test('create a project, open it, and create boards from Projects and from the board', async ({ page }) => {
  const name = `Launch ${Date.now().toString(36)}`;
  await page.goto('/projects');
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
  await page.getByPlaceholder('New project name').fill(name);
  await page.getByRole('button', { name: 'New project' }).click();
  const card = page.locator('.proj-card').filter({ hasText: name });
  await expect(card).toBeVisible();
  await atRoute(page, '/projects', 'project created');

  // Opening a project with no board makes its first one and lands on it.
  await card.locator('.title').click();
  await expect(page.locator('.columns > .column')).toHaveCount(5);
  await expect(page.getByRole('heading', { level: 1 })).not.toHaveText('Board');
  await atRoute(page, '/projects/:projectId/boards/:boardId', 'first board, made on open');
  const firstBoard = new URL(page.url()).pathname;

  // With the project picked in the sidebar, Projects lists its boards and makes new ones.
  await page.getByLabel('Project', { exact: true }).selectOption({ label: name });
  await page.getByRole('link', { name: 'Projects' }).click();
  await expect(page.getByRole('heading', { name })).toBeVisible();
  await page.getByPlaceholder('New board name').fill('Roadmap');
  await page.getByRole('button', { name: 'New board' }).click();
  const roadmap = page.locator('.proj-card').filter({ hasText: 'Roadmap' });
  await expect(roadmap).toBeVisible();
  await expect(page.locator('.proj-card')).toHaveCount(2);
  await atRoute(page, '/projects', 'boards of the picked project');
  await roadmap.locator('.title').click();
  await expect(page.getByRole('heading', { name: 'Roadmap' })).toBeVisible();
  expect(new URL(page.url()).pathname).not.toBe(firstBoard);

  // The board's own "+ New board" creates one in the same project and opens it.
  await page.getByRole('button', { name: '+ New board' }).click();
  await page.getByLabel('New board name').fill('Incidents');
  await page.getByRole('button', { name: 'Create board' }).click();
  await expect(page.getByRole('heading', { name: 'Incidents' })).toBeVisible();
  await expect(page.getByLabel('Switch board')).toBeVisible();
  await expect(page.getByLabel('Switch board').locator('option')).toHaveCount(3);
  await atRoute(page, '/projects/:projectId/boards/:boardId', 'board created from the board');
});
