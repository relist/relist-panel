import { execFileSync } from 'node:child_process';
import { expect, test, type Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 10: Change version (ADR-0047). Postgres 16 → 18 moves the
// data to a new volume and keeps the roles, a managed Full user's SET role
// and a restricted database's ACL; a database with adminpack (in 16's
// contrib, gone from 18) fails the restore and leaves 16 as it was; MariaDB
// 11.4 → 11.8 and Redis 7.4 → 8 change the image in place.

function dockerEnv() {
  const env = sandbox() as unknown as Record<string, string | undefined>;
  return {
    ...process.env,
    DOCKER_HOST: env.dockerHost,
    DOCKER_CONFIG: env.dockerConfig,
    ...(env.dockerTlsVerify ? { DOCKER_TLS_VERIFY: env.dockerTlsVerify, DOCKER_CERT_PATH: env.dockerCertPath } : {}),
    NO_PROXY: `${process.env.NO_PROXY ? `${process.env.NO_PROXY},` : ''}127.0.0.1,localhost`,
  };
}

// A password never goes on a command line: the client run names the
// variable and the docker CLI copies it from its own environment.
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

function psql(id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'PGPASSWORD', '-e', 'PGCONNECT_TIMEOUT=10', 'postgres:18',
      'psql', '-h', `relist-db-${id}`, '-U', user, '-d', database, '-At', '-v', 'ON_ERROR_STOP=1', '-c', sql],
    secret: { PGPASSWORD: password },
  };
}

function volumes(): string[] {
  return docker('volume', 'ls', '--format', '{{.Name}}').split('\n');
}

type Creds = { user: string; password: string };
type Change = { id: number; status: string; error: string; previousData: string; toVersion: string };
type DB = { job: string; status: string; lastError: string; version: string; settingsOutdated: boolean };

async function createRunning(panel: Panel, name: string, engine: string, version: string): Promise<number> {
  const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine, version });
  await expect
    .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 300_000 })
    .toBe('running');
  return id;
}

async function creds(panel: Panel, id: number, user?: string, database?: string): Promise<Creds> {
  const query = user ? `?user=${encodeURIComponent(user)}&database=${encodeURIComponent(database ?? '')}` : '';
  return panel.json<Creds>('GET', `/api/databases/${id}/credentials${query}`);
}

/** Waits for the job to end; the Database is running again either way. */
async function settled(panel: Panel, id: number): Promise<DB> {
  await expect
    .poll(async () => {
      const d = await panel.json<DB>('GET', `/api/databases/${id}`);
      return d.job === '' && d.status === 'running';
    }, { timeout: 600_000, intervals: [2_000] })
    .toBe(true);
  return panel.json<DB>('GET', `/api/databases/${id}`);
}

async function lastChange(panel: Panel, id: number): Promise<Change> {
  const list = await panel.json<Change[]>('GET', `/api/databases/${id}/version-changes`);
  return list[0];
}

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
}

async function removeDatabase(panel: Panel, id: number, name: string) {
  await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
}

test.describe('change version', () => {
  test.describe.configure({ mode: 'serial', timeout: 20 * 60_000 });

  test('postgres 16 → 18 keeps data, roles and a managed user', async ({ page }) => {
    const name = `e2e-cv-pg-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '16');
    docker('pull', '-q', 'postgres:18');

    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app', createOwner: true });
    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'secret', createOwner: true });
    const owner = await creds(panel, id, 'app', 'app');
    docker(psql(id, 'app', owner.password, 'app', "CREATE TABLE t (v text); INSERT INTO t VALUES ('kept')"));
    await panel.json('POST', `/api/databases/${id}/users`, { name: 'full_u' });
    await panel.json('PUT', `/api/databases/${id}/users/full_u/access`, { access: { app: 'full', secret: 'none' } });
    const full = await creds(panel, id, 'full_u', 'app');
    const su = await creds(panel, id);
    const appOwner = docker(psql(id, 'postgres', su.password, 'postgres', "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'app'"));
    expect(docker(psql(id, 'full_u', full.password, 'app', 'SELECT current_user'))).toBe(appOwner);

    await signIn(page);
    await page.goto(`/databases/${id}`);
    await page.getByRole('button', { name: 'Change version' }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText(/moves the data to PostgreSQL 18 on a new volume/)).toBeVisible();
    await dialog.getByRole('button', { name: 'Change to 18' }).click();
    // The dialog closes once the job has started; polling earlier can see
    // the Database idle on 16 before the request lands.
    await expect(dialog).toBeHidden();
    const after = await settled(panel, id);
    expect(after.version).toBe('18');
    expect(after.lastError).toBe('');
    const change = await lastChange(panel, id);
    expect(change.status).toBe('succeeded');
    expect(change.previousData).toBe(`relist-db-${id}`);

    expect(docker(psql(id, 'postgres', su.password, 'postgres', 'SHOW server_version_num')).slice(0, 2)).toBe('18');
    expect(docker(psql(id, 'app', owner.password, 'app', 'SELECT v FROM t'))).toBe('kept');
    // The Full user still acts as the owner: its SET role came across.
    expect(docker(psql(id, 'full_u', full.password, 'app', 'SELECT current_user'))).toBe(appOwner);
    // The restricted database still refuses a user with no grant on it.
    expect(tryDocker(psql(id, 'full_u', full.password, 'secret', 'SELECT 1'))).toBeUndefined();

    await page.reload();
    await page.getByRole('tab', { name: 'Settings' }).click();
    await expect(page.getByText('16 → 18')).toBeVisible();
    await expect(page.getByRole('cell', { name: `relist-db-${id} Delete` })).toBeVisible();
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Delete previous data' }).click();
    await expect(page.getByRole('cell', { name: `relist-db-${id} Delete` })).toBeHidden();
    const vols = volumes();
    expect(vols).not.toContain(`relist-db-${id}`);
    expect(vols).toContain(`relist-db-${id}-v${change.id}`);

    await removeDatabase(panel, id, name);
    expect(volumes()).not.toContain(`relist-db-${id}-v${change.id}`);
  });

  test('a failed restore leaves postgres 16 as it was', async () => {
    const name = `e2e-cv-fail-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '16');
    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'legacy', createOwner: true });
    const su = await creds(panel, id);
    docker(psql(id, 'postgres', su.password, 'legacy', "CREATE EXTENSION adminpack; CREATE TABLE t (v text); INSERT INTO t VALUES ('still here')"));

    await panel.json('POST', `/api/databases/${id}/change-version`, { version: '18' });
    const after = await settled(panel, id);
    expect(after.version).toBe('16');
    const change = await lastChange(panel, id);
    expect(change.status).toBe('failed');
    expect(change.error).toMatch(/^Restore legacy: /);
    expect(docker(psql(id, 'postgres', su.password, 'legacy', 'SELECT v FROM t'))).toBe('still here');
    expect(docker(psql(id, 'postgres', su.password, 'postgres', 'SHOW server_version_num')).slice(0, 2)).toBe('16');
    expect(volumes()).not.toContain(`relist-db-${id}-v${change.id}`);

    await removeDatabase(panel, id, name);
  });

  // A role can keep raw text in a list setting (SET … FROM CURRENT) that
  // pg_dumpall cannot write back: its roles dump would not restore. Change
  // version refuses it before anything changes, naming the fix.
  test('a role setting pg_dumpall cannot copy is refused up front', async () => {
    const name = `e2e-cv-guc-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '16');
    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'shop', createOwner: true });
    const owner = await creds(panel, id, 'shop', 'shop');
    docker(psql(id, 'shop', owner.password, 'shop',
      "SELECT set_config('local_preload_libraries', 'a,', false); ALTER ROLE CURRENT_USER SET local_preload_libraries FROM CURRENT"));

    const refused = await panel.raw('POST', `/api/databases/${id}/change-version`, { version: '18' });
    expect(refused.status).toBe(422);
    expect((JSON.parse(refused.body) as { error: string }).error).toContain('ALTER ROLE "shop" RESET local_preload_libraries');
    expect(await panel.json<Change[]>('GET', `/api/databases/${id}/version-changes`)).toEqual([]);
    const db = await panel.json<DB>('GET', `/api/databases/${id}`);
    expect(db.job).toBe('');
    expect(db.version).toBe('16');

    await removeDatabase(panel, id, name);
  });

  test('mariadb 11.4 → 11.8 and redis 7.4 → 8 change in place', async () => {
    const panel = await Panel.signIn();

    const mname = `e2e-cv-maria-${runId()}`;
    const mid = await createRunning(panel, mname, 'mariadb', '11.4');
    await panel.json('POST', `/api/databases/${mid}/logical-databases`, { name: 'app' });
    const root = await creds(panel, mid);
    const maria = (sql: string): Run => ({
      args: ['run', '--rm', '--network', 'relist-network', '-e', 'MYSQL_PWD', 'mariadb:11.8',
        'mariadb', '-h', `relist-db-${mid}`, '-u', 'root', '-N', '-D', 'app', '-e', sql],
      secret: { MYSQL_PWD: root.password },
    });
    docker(maria('CREATE TABLE t (a int); INSERT INTO t VALUES (5)'));
    await panel.json('POST', `/api/databases/${mid}/change-version`, { version: '11.8', backupFirst: true });
    const m = await settled(panel, mid);
    expect(m.version).toBe('11.8');
    expect(m.lastError).toBe('');
    expect(m.settingsOutdated).toBe(false);
    expect((await lastChange(panel, mid)).status).toBe('succeeded');
    expect(docker(maria('SELECT a FROM t'))).toBe('5');
    await removeDatabase(panel, mid, mname);

    const rname = `e2e-cv-redis-${runId()}`;
    const rid = await createRunning(panel, rname, 'redis', '7.4');
    const pw = (await creds(panel, rid)).password;
    const redis = (...command: string[]): Run => ({
      args: ['run', '--rm', '--network', 'relist-network', '-e', 'REDISCLI_AUTH', 'redis:8', 'redis-cli', '-h', `relist-db-${rid}`, '--no-auth-warning', ...command],
      secret: { REDISCLI_AUTH: pw },
    });
    docker(redis('SET', 'k', 'v'));
    docker(redis('SAVE'));
    await panel.json('POST', `/api/databases/${rid}/change-version`, { version: '8' });
    const r = await settled(panel, rid);
    expect(r.version).toBe('8');
    expect(docker(redis('GET', 'k'))).toBe('v');
    await removeDatabase(panel, rid, rname);
  });
});
