import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { sandbox } from '../lib/sandbox.js';

// Opening the panel shows Relist-chan's boot splash until the first data is
// in, instead of an empty shell that reads as lost data. It says what it is
// waiting for, goes away by itself, and never comes back on later reloads.

const splash = (page: Page) => page.getByRole('status', { name: 'Loading' });

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
}

/** Holds `/api/projects` until the returned function is called. */
async function holdProjects(page: Page): Promise<() => void> {
  let release!: () => void;
  const released = new Promise<void>((resolve) => { release = resolve; });
  await page.route((u) => u.pathname === '/api/projects', async (route) => {
    await released;
    await route.continue();
  });
  return release;
}

test('the splash covers the panel until the Project tree is in, then goes', async ({ page }) => {
  await signIn(page);
  await expect(splash(page)).toHaveCount(0);

  const release = await holdProjects(page);
  await page.reload();
  await expect(splash(page)).toBeVisible();
  await expect(splash(page)).toContainText('Loading projects');
  // The shell is mounted underneath but out of reach.
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeAttached();

  release();
  await expect(splash(page)).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await page.getByRole('menuitem', { name: /\bProjects\b/ }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/projects');
});

test('navigating within the shell never shows the splash', async ({ page }) => {
  await signIn(page);
  await expect(splash(page)).toHaveCount(0);
  await page.getByRole('menuitem', { name: /\bProjects\b/ }).click();
  await page.getByRole('menuitem', { name: /\bOverview\b/ }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
  await expect(splash(page)).toHaveCount(0);
});

test('a signed-out visitor goes straight to sign-in without the splash staying', async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, baseURL: sandbox().panelUrl });
  const page = await context.newPage();
  await page.goto('/projects');
  await page.waitForURL((u) => u.pathname.startsWith('/signin'));
  await expect(splash(page)).toHaveCount(0);
  await context.close();
});
