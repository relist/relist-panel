import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 2: back up a Postgres from the Backups tab (one file per
// database), download a file, restore it into a new database and over the old
// one after deleting a row, then delete the Database keeping its
// Backups (a tombstone). Redis has no Backups tab and answers 400. The
// postgres:18 and redis:8 images are pulled by the first run.

function docker(...args: string[]): string {
  const env = sandbox();
  return execFileSync('docker', args, {
    encoding: 'utf8',
    env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig },
  }).trim();
}

function psql(container: string, sql: string, database = 'postgres'): string {
  return docker('exec', container, 'psql', '-U', 'postgres', '-At', '-d', database, '-c', sql);
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

async function createRunning(panel: Panel, name: string, engine: string, version: string): Promise<number> {
  const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine, version });
  await expect
    .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, {
      timeout: 240_000,
    })
    .toBe('running');
  return id;
}

test('back up, download, restore and keep the backups of a deleted Postgres', async ({ page }) => {
  const name = `e2e-bk-${runId()}`;
  const panel = await Panel.signIn();
  const id = await createRunning(panel, name, 'postgres', '18');
  const container = `relist-db-${id}`;

  // 1. Some data to lose.
  psql(container, 'CREATE DATABASE shop');
  psql(container, "CREATE TABLE items (name text); INSERT INTO items VALUES ('kept')", 'shop');
  expect(psql(container, 'SELECT name FROM items', 'shop')).toBe('kept');

  // 2. Back up all databases from the Backups tab: one Backup for each.
  await signIn(page);
  await page.goto(`/databases/${id}`);
  await page.getByRole('tab', { name: 'Backups' }).click();
  await page.getByRole('button', { name: 'Back up now' }).click();
  const backupDialog = page.getByRole('dialog');
  await expect(backupDialog.getByText('One file per database.')).toBeVisible();
  await backupDialog.getByRole('button', { name: 'Back up', exact: true }).click();
  const manual = page.getByRole('row').filter({ hasText: 'Manual' });
  const backups = manual.filter({ hasText: 'shop' });
  await expect(backups.getByText('Succeeded')).toBeVisible({ timeout: 120_000 });
  await expect(manual.filter({ hasText: 'postgres' }).getByText('Succeeded')).toBeVisible({ timeout: 120_000 });

  // 3. The download is a gzip file.
  const [first] = await panel.json<{ id: number }[]>('GET', `/api/databases/${id}/backups`);
  expect(first.id).toBeGreaterThan(0);
  // The browser fetches it (Node does not resolve *.localhost).
  const download = await page.evaluate(async (path) => {
    const response = await fetch(path);
    const bytes = new Uint8Array(await response.arrayBuffer());
    return {
      disposition: response.headers.get('content-disposition'),
      magic: [bytes[0], bytes[1]],
      status: response.status,
      type: response.headers.get('content-type'),
    };
  }, `/api/databases/${id}/backups/${first.id}/download`);
  expect(download.status).toBe(200);
  expect(download.type).toBe('application/gzip');
  expect(download.disposition).toMatch(/^attachment; filename=".+\.sql\.gz"$/);
  expect(download.magic).toEqual([0x1f, 0x8b]);

  // 4. Restore shop into a new database, shop_copy: both have the row, and no pre_restore Backup is taken.
  await backups.getByRole('button', { name: 'Restore', exact: true }).click();
  const copyDialog = page.getByRole('dialog');
  const copyOk = copyDialog.getByRole('button', { name: 'Restore', exact: true });
  await expect(copyOk).toBeDisabled();
  await expect(copyDialog.getByText(/^Replaces the database shop\./)).toBeVisible();
  await copyDialog.getByLabel('Restore into a new database').check();
  await copyDialog.getByLabel('New database name').fill('shop_copy');
  await expect(copyDialog.getByText('Creates the database shop_copy.')).toBeVisible();
  await copyDialog.getByLabel(`Type ${name} to confirm`).fill(name);
  await copyOk.click();
  await expect
    .poll(() => psql(container, 'SELECT name FROM items', 'shop_copy'), { timeout: 120_000 })
    .toBe('kept');
  expect(psql(container, 'SELECT name FROM items', 'shop')).toBe('kept');
  await expect(page.getByRole('row').filter({ hasText: 'Before restore' })).toHaveCount(0);

  // 5a. Lose the row, then restore over shop with the typed name. The restore takes a pre_restore Backup first.
  psql(container, 'DELETE FROM items', 'shop');
  expect(psql(container, 'SELECT count(*) FROM items', 'shop')).toBe('0');
  await backups.getByRole('button', { name: 'Restore', exact: true }).click();
  const restoreDialog = page.getByRole('dialog');
  const ok = restoreDialog.getByRole('button', { name: 'Restore', exact: true });
  await expect(ok).toBeDisabled();
  await expect(
    restoreDialog.getByText('Replaces the database shop. It is backed up first. Apps connected to it are disconnected.'),
  ).toBeVisible();
  await restoreDialog.getByLabel(`Type ${name} to confirm`).fill(name);
  await ok.click();
  await expect.poll(() => psql(container, 'SELECT name FROM items', 'shop'), { timeout: 120_000 }).toBe('kept');
  await expect(page.getByRole('row').filter({ hasText: 'Before restore' })).toBeVisible();

  // 5. Delete the Database, keeping its Backups: a tombstone.
  await page.getByRole('tab', { name: 'Settings' }).click();
  await page.getByRole('button', { name: 'Delete database' }).first().click();
  const deleteDialog = page.getByRole('dialog');
  await expect(deleteDialog.getByLabel('Also delete its backups')).not.toBeChecked();
  await deleteDialog.getByLabel(`Type ${name} to confirm`).fill(name);
  await deleteDialog.getByRole('button', { name: 'Delete database' }).click();
  await expect(page).toHaveURL((u) => u.pathname === '/databases');
  const tombstone = `${name} (deleted #${id})`;
  await expect(page.getByRole('link', { name: tombstone })).toBeVisible();
  expect(docker('ps', '-a', '--filter', `name=^${container}$`, '--format', '{{.Names}}')).toBe('');
  expect(docker('volume', 'ls', '--filter', `name=^${container}$`, '--format', '{{.Name}}')).toBe('');

  // 6. The tombstone shows only its Backups, which still download.
  await page.getByRole('link', { name: tombstone }).click();
  await expect(page.getByText('This Database was deleted. Its backups are kept until you delete them.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Back up now' })).toHaveCount(0);
  const kept = await panel.json<{ id: number }[]>('GET', `/api/databases/${id}/backups`);
  expect(kept.length).toBeGreaterThanOrEqual(3);
  const again = await page.evaluate(
    async (path) => (await fetch(path)).status,
    `/api/databases/${id}/backups/${kept[0].id}/download`,
  );
  expect(again).toBe(200);

  // 7. Deleting the last Backup takes the tombstone with it, so a run leaves nothing behind.
  for (const b of kept) {
    await panel.json('DELETE', `/api/databases/${id}/backups/${b.id}`);
  }
  expect((await panel.raw('GET', `/api/databases/${id}`)).status).toBe(404);
});

test('Redis has no Backups tab and answers 400 to a backup', async ({ page }) => {
  const name = `e2e-rd-${runId()}`;
  const panel = await Panel.signIn();
  const id = await createRunning(panel, name, 'redis', '8');
  await signIn(page);
  await page.goto(`/databases/${id}`);
  await expect(page.getByRole('tab', { name: 'Connection' })).toBeVisible();
  await expect(page.getByRole('tab', { name: 'Backups' })).toHaveCount(0);
  const reply = await panel.raw('POST', `/api/databases/${id}/backups`, { all: true });
  expect(reply.status).toBe(400);
  await panel.json('DELETE', `/api/databases/${id}`, { confirm: name });
});
