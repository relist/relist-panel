import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Recreate and restore (ADR-0050): the container and the volume of a
// Database are removed behind the panel's back, the page says the volume is
// gone, and Recreate brings the Database back on a new volume with the same
// password, its users and their access, from its Backups. Checked with real
// clients on relist-network: the app's user owns its database again and
// reads its row, a Read-only user still only reads, and on MySQL the app's
// user gets its schema grant back.

function dockerEnv() {
  const env = sandbox();
  return { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig };
}

// A password never goes on a command line: the client run names the
// variable, and the docker CLI copies it from the environment.
type Run = { args: string[]; secret?: Record<string, string> };

function docker(first: string | Run, ...rest: string[]): string {
  const run: Run = typeof first === 'string' ? { args: [first, ...rest] } : first;
  return execFileSync('docker', run.args, {
    encoding: 'utf8',
    env: { ...dockerEnv(), ...run.secret },
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function tryDocker(run: Run): string | undefined {
  try {
    return docker(run);
  } catch {
    return undefined;
  }
}

const PG = 'postgres:18';
const MY = 'mysql:8.4';

function psql(id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'PGPASSWORD', '-e', 'PGCONNECT_TIMEOUT=10', PG,
      'psql', '-h', `relist-db-${id}`, '-U', user, '-d', database, '-At', '-c', sql],
    secret: { PGPASSWORD: password },
  };
}

function mysql(id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'MYSQL_PWD', MY,
      'mysql', '-h', `relist-db-${id}`, '-u', user, '-N', '-D', database, '-e', sql],
    secret: { MYSQL_PWD: password },
  };
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

type DatabaseView = { status: string; job: string; lastError: string };

async function view(panel: Panel, id: number): Promise<DatabaseView> {
  return panel.json<DatabaseView>('GET', `/api/databases/${id}`);
}

async function createRunning(panel: Panel, name: string, engine: string, version: string): Promise<number> {
  const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine, version });
  await expect.poll(async () => (await view(panel, id)).status, { timeout: 240_000 }).toBe('running');
  return id;
}

async function password(panel: Panel, id: number, user?: string, database?: string): Promise<string> {
  const query = user ? `?user=${encodeURIComponent(user)}&database=${encodeURIComponent(database ?? '')}` : '';
  return (await panel.json<{ password: string }>('GET', `/api/databases/${id}/credentials${query}`)).password;
}

/** Backs up every database and returns the Backup of `database`. */
async function backUp(panel: Panel, id: number, database: string): Promise<number> {
  await panel.json('POST', `/api/databases/${id}/backups`, { all: true });
  let found = 0;
  await expect
    .poll(
      async () => {
        const list = await panel.json<{ id: number; database: string; status: string }[]>('GET', `/api/databases/${id}/backups`);
        found = list.find((b) => b.database === database && b.status === 'succeeded')?.id ?? 0;
        return found > 0 && list.every((b) => b.status !== 'running');
      },
      { timeout: 120_000 },
    )
    .toBe(true);
  return found;
}

/** Removes the Database's container and volume the way a lost disk or a prune would. */
async function loseVolume(panel: Panel, id: number) {
  docker('rm', '-f', `relist-db-${id}`);
  docker('volume', 'rm', `relist-db-${id}`);
  await expect.poll(async () => (await view(panel, id)).status, { timeout: 30_000 }).toBe('volume_missing');
}

/** Waits for the Recreate job to end, then for the server to be up with no last error. */
async function recreated(panel: Panel, id: number) {
  await expect
    .poll(
      async () => {
        const d = await view(panel, id);
        return d.job === '' && d.status === 'running' ? d.lastError : 'busy';
      },
      { timeout: 300_000 },
    )
    .toBe('');
}

test.describe('recreate after a lost volume', () => {
  test.describe.configure({ mode: 'serial', timeout: 12 * 60_000 });

  test('Postgres: the page recreates it with its password, owner, users and access', async ({ page }) => {
    const name = `e2e-rc-pg-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '18');
    docker('pull', '-q', PG);

    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app', createOwner: true });
    await panel.json('POST', `/api/databases/${id}/users`, { name: 'reader' });
    await panel.json('PUT', `/api/databases/${id}/users/reader/access`, { access: { app: 'read' } });
    const superuser = await password(panel, id);
    const app = await password(panel, id, 'app', 'app');
    const reader = await password(panel, id, 'reader', 'app');
    docker(psql(id, 'app', app, 'app', 'CREATE TABLE t (a int); INSERT INTO t VALUES (1)'));
    await backUp(panel, id, 'app');

    await loseVolume(panel, id);
    await signIn(page);
    await page.goto(`/databases/${id}`);
    await expect(page.getByText('The data volume is gone')).toBeVisible();
    await page.getByRole('button', { name: 'Recreate and restore…' }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByLabel('Restore app')).toBeChecked();
    await dialog.getByLabel(`Type ${name} to confirm`).fill(name);
    await dialog.getByRole('button', { name: 'Recreate', exact: true }).click();
    await recreated(panel, id);

    // The same superuser password, and the app's user owns its database again.
    expect(await password(panel, id)).toBe(superuser);
    expect(docker(psql(id, 'app', app, 'app', 'SELECT count(*) FROM t'))).toBe('1');
    expect(docker(psql(id, 'app', app, 'app', `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'app'`))).toBe('app');
    docker(psql(id, 'app', app, 'app', 'INSERT INTO t VALUES (2)'));
    // Read-only came back with it: it reads, and cannot write.
    expect(docker(psql(id, 'reader', reader, 'app', 'SELECT count(*) FROM t'))).toBe('2');
    expect(tryDocker(psql(id, 'reader', reader, 'app', 'INSERT INTO t VALUES (3)'))).toBeUndefined();
    const restores = await panel.json<{ target: string; status: string }[]>('GET', `/api/databases/${id}/restores`);
    expect(restores.find((r) => r.target === 'app')?.status).toBe('succeeded');

    await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
  });

  test('MySQL: the app user gets its schema grant back', async () => {
    const name = `e2e-rc-my-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'mysql', '8.4');
    docker('pull', '-q', MY);

    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'shop', createOwner: true });
    const shop = await password(panel, id, 'shop', 'shop');
    docker(mysql(id, 'shop', shop, 'shop', 'CREATE TABLE t (a int); INSERT INTO t VALUES (1)'));
    const backupId = await backUp(panel, id, 'shop');

    await loseVolume(panel, id);
    const twice = await panel.raw('POST', `/api/databases/${id}/recreate`, { confirm: name, backupIds: [backupId, backupId] });
    expect(twice.status).toBe(400);
    const started = await panel.raw('POST', `/api/databases/${id}/recreate`, { confirm: name, backupIds: [backupId] });
    expect(started.status).toBe(202);
    await recreated(panel, id);

    expect(docker(mysql(id, 'shop', shop, 'shop', 'SELECT count(*) FROM t'))).toBe('1');
    docker(mysql(id, 'shop', shop, 'shop', 'INSERT INTO t VALUES (2)'));

    await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
  });
});
