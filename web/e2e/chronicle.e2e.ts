// Chronicle: the list, a new hand-written record, editing it into a second version, and its history
// with a comparison and a revert.
import { seedProject, type SeededBoard } from './support/mcp.js';
import { atRoute, expect, test } from './support/test.js';

test.describe('chronicle', () => {
  let b: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Chronicle');
  });

  test('write a record, edit it, compare and revert its versions', async ({ page }) => {
    await page.goto('/chronicle');
    await page.getByLabel('Project', { exact: true }).selectOption({ label: b.projectName });
    await expect(page.getByRole('heading', { name: 'Chronicle' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'New record' })).toBeVisible();
    await atRoute(page, '/chronicle', 'project picked, no records');

    await page.getByRole('link', { name: 'New record' }).click();
    await expect(page.getByRole('heading', { name: 'New record' })).toBeVisible();
    await page.getByLabel('Title').fill('How we deploy');
    await expect(page.locator('#chr-slug')).toHaveValue(/how-we-deploy/);
    const addFirst = page.getByRole('button', { name: 'Add the first block' });
    if (await addFirst.isVisible()) await addFirst.click();
    await page.locator('textarea[aria-label^="Block "]').first().fill('Tag the commit, then `make dist` from a clean clone.');
    await atRoute(page, '/chronicle/new', 'filled in');
    await page.getByRole('button', { name: 'Create record' }).click();

    await expect(page.getByRole('heading', { name: 'How we deploy' })).toBeVisible();
    await expect(page.getByText('from a clean clone.')).toBeVisible();
    await atRoute(page, '/chronicle/:pageId', 'created');
    const pagePath = new URL(page.url()).pathname;

    await page.getByRole('button', { name: 'Edit', exact: true }).click();
    await page.locator('textarea[aria-label^="Block "]').first().fill('Tag the commit, run `make dist` from a clean clone, publish SHA256SUMS.');
    await page.getByRole('button', { name: 'Save version' }).click();
    await expect(page.getByText('publish SHA256SUMS.')).toBeVisible();
    await atRoute(page, '/chronicle/:pageId', 'second version');

    await page.getByRole('link', { name: 'History' }).click();
    await expect(page.getByRole('heading', { name: 'History' })).toBeVisible();
    const rows = page.locator('.line-row');
    await expect(rows).toHaveCount(2);
    await rows.last().getByRole('button', { name: 'Compare with latest' }).click();
    await expect(page.locator('.chr-diff-row.is-changed')).toHaveCount(1);
    await atRoute(page, '/chronicle/:pageId/history', 'first version compared with the latest');

    await rows.last().getByRole('button', { name: 'Revert to this' }).click();
    await expect(rows).toHaveCount(3);
    await page.getByRole('link', { name: 'Back to page' }).click();
    expect(new URL(page.url()).pathname).toBe(pagePath);
    await expect(page.getByText('from a clean clone.')).toBeVisible();
    await expect(page.getByText('publish SHA256SUMS.')).toBeHidden();
    await atRoute(page, '/chronicle/:pageId', 'reverted to the first version');

    await page.getByRole('link', { name: 'Chronicle' }).click();
    await expect(page.getByText('How we deploy')).toBeVisible();
    await atRoute(page, '/chronicle', 'the record listed');
  });
});
