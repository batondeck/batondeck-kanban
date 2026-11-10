// On-call, as the workspace admin: Setup opens on the source list, a Grafana integration is
// created with a generated key (shown once), a test event is sent through it, and the screen's tiles
// and delivery health count it. On-call works on the sidebar's selected project, and its own picker
// moves the sidebar's (P-434); on "All projects" it asks for a project and stores nothing (P-442).
import { McpClient, seedProject, type SeededBoard } from './support/mcp.js';
import { atRoute, expect, test } from './support/test.js';

test.describe('on-call', () => {
  let b: SeededBoard;
  let other: SeededBoard;
  test.beforeAll(async () => {
    b = await seedProject('Oncall');
    // A second project, so On-call shows its picker and there is a selection to follow.
    other = await seedProject('Oncall other');
  });

  test('On-call follows the sidebar project, and its picker moves the sidebar', async ({ page }) => {
    await page.goto('/oncall');
    await expect(page.getByRole('heading', { name: 'On-call' })).toBeVisible();
    const sidebar = page.getByRole('combobox', { name: 'Project', exact: true });
    const picker = page.getByLabel('On-call project');
    const prompt = page.getByText('Pick a project to configure', { exact: true });
    const integrationsTile = page.locator('.tile-k', { hasText: /^Integrations$/ });
    // A fresh context starts on "All projects": the picker shows no project chosen, the screen asks
    // for one, Setup is closed, and nothing is stored.
    await expect(sidebar).toHaveValue('');
    await expect(picker).toHaveValue('');
    await expect(prompt).toBeVisible();
    await expect(integrationsTile).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'On-call setup' })).toBeDisabled();
    expect(await page.evaluate(() => localStorage.getItem('bd.projectId') ?? '')).toBe('');
    await atRoute(page, '/oncall', 'All projects: pick a project to configure');
    await page.getByRole('link', { name: 'Projects', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await expect(sidebar).toHaveValue('');
    await page.goto('/oncall');
    await expect(prompt).toBeVisible();

    await sidebar.selectOption({ label: b.projectName });
    await expect(picker).toHaveValue(b.projectId);
    await expect(prompt).toHaveCount(0);
    await expect(integrationsTile).toBeVisible();
    await atRoute(page, '/oncall', 'follows the sidebar project');

    await picker.selectOption({ label: other.projectName });
    await expect(sidebar).toHaveValue(other.projectId);
    expect(await page.evaluate(() => localStorage.getItem('bd.projectId'))).toBe(other.projectId);
    await atRoute(page, '/oncall', 'its picker moved the sidebar');
  });

  test('create an integration in Setup, send a test event, and see it counted', async ({ page }) => {
    const mcp = new McpClient('admin');
    await page.goto('/oncall');
    await expect(page.getByRole('heading', { name: 'On-call' })).toBeVisible();
    const tile = (label: string) => page.locator('.tile').filter({ has: page.locator('.tile-k', { hasText: new RegExp(`^${label}$`) }) }).locator('.tile-v');
    // On-call has a project picker of its own; on "All projects" it asks for one before showing anything.
    await expect(page.getByText('Pick a project to configure', { exact: true })).toBeVisible();
    await page.getByLabel('On-call project').selectOption({ label: b.projectName });
    await expect(tile('Integrations')).toBeVisible();
    await expect(tile('Integrations')).toHaveText('0');
    await atRoute(page, '/oncall', 'no integrations');

    await page.getByRole('button', { name: 'On-call setup' }).click();
    const setup = page.getByRole('dialog', { name: 'On-call setup' });
    await expect(setup.getByRole('heading', { name: 'On-call setup' })).toBeVisible();
    // With nothing configured, the source list is what Setup opens on.
    await setup.getByLabel('Filter sources').fill('graf');
    await setup.locator('.source-picker').getByRole('button', { name: /Grafana/ }).click();
    await setup.getByLabel('Name', { exact: true }).fill('prod-grafana');
    await setup.getByLabel('Target board id', { exact: true }).fill(b.boardId);
    await setup.getByRole('button', { name: 'Generate' }).click();
    const key = await setup.getByLabel('API key').inputValue();
    expect(key.length).toBeGreaterThan(16);
    await atRoute(page, '/oncall', 'setup: Grafana form filled');
    await setup.getByRole('button', { name: 'Create', exact: true }).click();

    // The key is shown once, with the endpoint to paste into Grafana.
    const shown = setup.locator('.record').filter({ hasText: 'Your API key — shown once' });
    await expect(shown.locator('code').first()).toHaveText(key);
    // The full URL to paste into the sender, ending with the integration's own path.
    await expect(shown.getByText(/Endpoint URL:/)).toBeVisible();
    await expect(shown.locator('code').nth(1)).toHaveText(/^https?:\/\/[^/]+\/hooks\/I-[^/]+$/);
    await atRoute(page, '/oncall', 'setup: key shown once');
    await shown.getByRole('button', { name: 'I have stored it' }).click();
    await expect(setup.getByText('Your API key — shown once')).toHaveCount(0);
    await expect(setup.locator('.int-row').filter({ hasText: 'prod-grafana' })).toBeVisible();

    const { integrations } = await mcp.call<{ integrations: Array<{ id: string; name: string; source: string; enabled: boolean }> }>(
      'list_integrations',
      { projectId: b.projectId },
    );
    expect(integrations.map((i) => [i.name, i.source, i.enabled])).toEqual([['prod-grafana', 'grafana', true]]);

    // A test event through the new integration lands in its delivery log.
    await setup.getByRole('button', { name: 'Send test event' }).click();
    await expect
      .poll(async () => (await mcp.call<{ deliveries: unknown[] }>('list_deliveries', { projectId: b.projectId, integrationId: integrations[0]!.id })).deliveries.length)
      .toBeGreaterThan(0);

    await setup.getByRole('button', { name: 'Close setup' }).click();
    await expect(setup).toBeHidden();
    await expect(tile('Integrations')).toHaveText('1');
    await expect(tile('Enabled')).toHaveText('1');
    await expect(tile('Sources')).toHaveText('1');
    const health = page.locator('.record').filter({ hasText: 'Delivery health' }).locator('tbody tr');
    await expect(health).toHaveCount(1);
    await expect(health).toContainText('prod-grafana');
    await atRoute(page, '/oncall', 'one integration, delivery health');
  });
});
