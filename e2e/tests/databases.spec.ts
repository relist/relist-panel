import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases (ADR-0039): create a Postgres from the UI, wait for it to run,
// reveal its connection URL, rename it without a restart, and delete it with
// its volume. The first run pulls postgres:18 inside the sandbox.

function docker(...args: string[]): string {
  const env = sandbox();
  return execFileSync('docker', args, {
    encoding: 'utf8',
    env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig },
  }).trim();
}

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
  await expect(page.getByRole('menuitem').first()).toBeVisible();
}

test('create, connect, rename and delete a Postgres Database', async ({ page }) => {
  const name = `e2e-pg-${runId()}`;
  const renamed = `${name}-renamed`;
  const panel = await Panel.signIn();
  await signIn(page);

  // 1. Create it from the Databases page.
  await page.getByRole('menuitem', { name: /Databases/ }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/databases');
  await page.getByRole('button', { name: /Create database/ }).click();
  await page.getByLabel('Name').fill(name);
  await page.getByRole('button', { name: 'Create', exact: true }).click();

  // 2. It reaches Running (the image may need pulling first).
  const row = page.getByRole('row').filter({ has: page.getByRole('link', { name, exact: true }) });
  await expect(row.getByText('Running')).toBeVisible({ timeout: 240_000 });
  await row.getByRole('link', { name, exact: true }).click();
  await expect(page).toHaveURL(/\/databases\/\d+$/);
  const id = Number(new URL(page.url()).pathname.split('/').pop());
  const container = `relist-db-${id}`;

  // 3. Reveal the password; the URL has the stable host and parses.
  await page.getByRole('button', { name: /Reveal/ }).click();
  const url = await page.locator('code', { hasText: /^postgres:\/\// }).first().innerText();
  const parsed = new URL(url);
  expect(parsed.hostname).toBe(container);
  expect(parsed.port).toBe('5432');
  expect(parsed.username).toBe('postgres');
  expect(decodeURIComponent(parsed.password)).toMatch(/^[A-Za-z0-9_-]{43}$/);
  await expect(page.getByText('networks: [relist-network]')).toBeVisible();

  // No host port is published.
  expect(docker('port', container)).toBe('');
  const startedAt = docker('inspect', '-f', '{{.State.StartedAt}}', container);

  // 4. Rename: the readable alias moves, the container is not restarted.
  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByLabel('Name', { exact: true }).fill(renamed);
  await page.getByRole('button', { name: 'Rename' }).click();
  await expect(page.getByRole('heading', { name: renamed })).toBeVisible();
  await expect
    .poll(async () => (await panel.json<{ hostReadable: string }>('GET', `/api/databases/${id}`)).hostReadable)
    .toBe(`${renamed}.db.internal`);
  expect(docker('inspect', '-f', '{{.State.StartedAt}}', container)).toBe(startedAt);
  expect(docker('inspect', '-f', '{{json .NetworkSettings.Networks}}', container)).toContain(`${renamed}.db.internal`);

  // 5. Delete it; the container and the volume go.
  await page.getByRole('button', { name: 'Delete database' }).first().click();
  const dialog = page.getByRole('dialog');
  const ok = dialog.getByRole('button', { name: 'Delete database' });
  await expect(ok).toBeDisabled();
  await dialog.getByLabel(`Type ${renamed} to confirm`).fill(renamed);
  await ok.click();
  await expect(page).toHaveURL((u) => u.pathname === '/databases');
  await expect(page.getByRole('link', { name: renamed })).toHaveCount(0);
  expect(docker('ps', '-a', '--filter', `name=^${container}$`, '--format', '{{.Names}}')).toBe('');
  expect(docker('volume', 'ls', '--filter', `name=^${container}$`, '--format', '{{.Name}}')).toBe('');
});
