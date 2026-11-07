// Signing in through the form, a refused password, the two redirect routes, and signing out.
import { atRedirect, atRoute, expect, SIGN_IN, test } from './support/test.js';
import { runState } from './support/state.js';

test.describe('signed out', () => {
  test.use({ role: null });

  test('the sign-in form refuses a wrong password and admits the right one', async ({ page }) => {
    const admin = runState().accounts.admin;
    await page.goto('/');
    await expect(page.getByText('The task board your agents and your team share')).toBeVisible();
    await atRoute(page, SIGN_IN, 'empty form');

    await page.getByLabel('Login').fill(admin.login);
    await page.getByLabel('Password').fill('not-the-password');
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('alert')).toBeVisible();
    await atRoute(page, SIGN_IN, 'wrong password refused');

    await page.getByLabel('Password').fill(admin.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
    await expect(page.locator('.identity-email')).toHaveText(admin.name);
    await atRoute(page, '/projects', 'signed in');
  });
});

test('the root and an unknown in-app path redirect to Projects', async ({ page }) => {
  await atRedirect(page, '/', '/', '/projects');
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
  // A document load of an unknown path is the server's 404, not the app's: only a client route gets
  // the shell.
  const res = await page.request.get('/no-such-screen');
  expect(res.status()).toBe(404);
  await page.goto('/agents');
  await expect(page.getByRole('heading', { name: 'Agents' })).toBeVisible();
  await atRedirect(page, '*', '/no-such-screen', '/projects', { client: true });
  await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();
});

test.describe('sign-out', () => {
  // Its own session, so signing out does not end the admin session the other specs share.
  test.use({ role: null });

  test('signing out returns to the sign-in screen and ends the session', async ({ page }) => {
    const admin = runState().accounts.admin;
    await page.goto('/');
    await page.getByLabel('Login').fill(admin.login);
    await page.getByLabel('Password').fill(admin.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('heading', { name: 'Projects' })).toBeVisible();

    await page.locator('.identity-row').click();
    await expect(page.getByRole('menuitem', { name: 'Sign out' })).toBeVisible();
    await atRoute(page, '/projects', 'account menu open');
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
    await atRoute(page, SIGN_IN, 'after sign-out');

    // The session is gone on the server, not only in this tab: a reload stays signed out.
    await page.reload();
    await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
    const res = await page.request.get('/api/session');
    expect(res.status()).toBe(401);
  });
});
