import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { sandbox } from '../lib/sandbox.js';

// Session hardening (.scratch/session-hardening/spec.md, ADR-0051). A session
// lasts at most 7 days, and the server ends it after 24 idle hours. When the
// server has ended a session, as the 00056 upgrade does for every session, an
// open tab lands on sign-in rather than a broken page.

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
}

test('signing in sets an HttpOnly session cookie that lasts 7 days', async ({ page, context }) => {
  await signIn(page);
  const cookie = (await context.cookies()).find((c) => c.name === 'relist_session');
  expect(cookie).toBeDefined();
  expect(cookie!.httpOnly).toBe(true);
  const lifetime = cookie!.expires - Date.now() / 1000;
  expect(lifetime).toBeGreaterThan(7 * 24 * 3600 - 120);
  expect(lifetime).toBeLessThanOrEqual(7 * 24 * 3600 + 5);
});

test('a tab whose session the server ended goes back to sign-in', async ({ page, context }) => {
  await signIn(page);
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();

  // The browser still holds a cookie, but no session row matches it.
  const cookie = (await context.cookies()).find((c) => c.name === 'relist_session')!;
  await context.addCookies([{ ...cookie, value: 'ended-on-the-server' }]);

  await page.reload();
  await expect(page).toHaveURL((u) => u.pathname.startsWith('/signin'));
  await expect(page.getByRole('button', { name: /sign in/i })).toBeVisible();
});
