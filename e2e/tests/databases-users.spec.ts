import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 7: logical databases, users and the superuser password
// reset, each checked with a real client on relist-network (a one-off
// `docker run --network relist-network <image> <client>` against
// relist-db-<id>). Postgres 18 checks the grants (Read-only, default
// privileges, no cross-database connect, ACL kept by a replace-restore);
// MySQL 8.4 and MariaDB 11.8 check that Read-only on app_1 does not reach
// appx1 (the `_` wildcard); then the superuser reset on Postgres, MySQL and
// Redis checks the old password is refused, the new one works, the data is
// still there, and neither password reaches the Logs tab.

function dockerEnv() {
  const env = sandbox();
  return { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig };
}

// A password never goes on a command line: a client run names the variable
// (`-e PGPASSWORD`, no value) and the docker CLI copies it from the
// environment it is started with.
type Run = { args: string[]; secret?: Record<string, string> };

function docker(first: string | Run, ...rest: string[]): string {
  const run: Run = typeof first === 'string' ? { args: [first, ...rest] } : first;
  return execFileSync('docker', run.args, {
    encoding: 'utf8',
    env: { ...dockerEnv(), ...run.secret },
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

/** A client run that is allowed to fail: the output, or undefined when it exited non-zero. */
function tryDocker(first: string | Run, ...rest: string[]): string | undefined {
  try {
    return docker(first, ...rest);
  } catch {
    return undefined;
  }
}

const PG = 'postgres:18';

function psqlArgs(id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'PGPASSWORD', '-e', 'PGCONNECT_TIMEOUT=10', PG,
      'psql', '-h', `relist-db-${id}`, '-U', user, '-d', database, '-At', '-c', sql],
    secret: { PGPASSWORD: password },
  };
}

function mysqlArgs(image: string, client: string, id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'MYSQL_PWD', image,
      client, '-h', `relist-db-${id}`, '-u', user, '-N', ...(database ? ['-D', database] : []), '-e', sql],
    secret: { MYSQL_PWD: password },
  };
}

// REDISCLI_AUTH is the AUTH password redis-cli reads from its environment.
function redisArgs(id: number, password: string, ...command: string[]): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'REDISCLI_AUTH', 'redis:8', 'redis-cli', '-h', `relist-db-${id}`, '--no-auth-warning', ...command],
    secret: { REDISCLI_AUTH: password },
  };
}

async function createRunning(panel: Panel, name: string, engine: string, version: string): Promise<number> {
  const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine, version });
  await expect
    .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 240_000 })
    .toBe('running');
  return id;
}

type Creds = { user: string; password: string; url: string };

async function creds(panel: Panel, id: number, user?: string, database?: string): Promise<Creds> {
  const query = user ? `?user=${encodeURIComponent(user)}&database=${encodeURIComponent(database ?? '')}` : '';
  return panel.json<Creds>('GET', `/api/databases/${id}/credentials${query}`);
}

async function removeDatabase(panel: Panel, id: number, name: string) {
  await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
}

/** Waits for the Database's job to end and its status to be running again. */
async function settled(panel: Panel, id: number) {
  await expect
    .poll(
      async () => {
        const d = await panel.json<{ job: string; status: string; lastError: string }>('GET', `/api/databases/${id}`);
        return d.job === '' && d.status === 'running' ? d.lastError : 'busy';
      },
      { timeout: 240_000 },
    )
    .toBe('');
}

test.describe('database users', () => {
  test.describe.configure({ mode: 'serial', timeout: 10 * 60_000 });

  test('Postgres: owners, Read-only, default privileges, no cross-database connect, ACL kept by a restore', async () => {
    const name = `e2e-du-pg-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '18');
    docker('pull', '-q', PG);

    // 1. Two databases, each with an owning user of the same name.
    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app1', createOwner: true });
    await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app2', createOwner: true });
    const app1 = await creds(panel, id, 'app1', 'app1');
    const app2 = await creds(panel, id, 'app2', 'app2');
    expect(app1.url).toContain('@relist-db-');

    // 2. The owner writes in its database.
    docker(psqlArgs(id, 'app1', app1.password, 'app1', 'CREATE TABLE t (a int); INSERT INTO t VALUES (1)'));
    expect(docker(psqlArgs(id, 'app1', app1.password, 'app1', 'SELECT count(*) FROM t'))).toBe('1');

    // 3. A Read-only user on app1.
    await panel.json('POST', `/api/databases/${id}/users`, { name: 'reader' });
    await panel.json('PUT', `/api/databases/${id}/users/reader/access`, { access: { app1: 'read', app2: 'none' } });
    const reader = await creds(panel, id, 'reader', 'app1');
    expect(docker(psqlArgs(id, 'reader', reader.password, 'app1', 'SELECT count(*) FROM t'))).toBe('1');
    expect(tryDocker(psqlArgs(id, 'reader', reader.password, 'app1', 'INSERT INTO t VALUES (2)'))).toBeUndefined();
    // Default privileges: a table the owner creates later is readable too.
    docker(psqlArgs(id, 'app1', app1.password, 'app1', 'CREATE TABLE t2 (a int); INSERT INTO t2 VALUES (7)'));
    expect(docker(psqlArgs(id, 'reader', reader.password, 'app1', 'SELECT a FROM t2'))).toBe('7');
    expect(tryDocker(psqlArgs(id, 'reader', reader.password, 'app2', 'SELECT 1'))).toBeUndefined();

    // 4. One app's user cannot connect to another app's database.
    expect(tryDocker(psqlArgs(id, 'app1', app1.password, 'app2', 'SELECT 1'))).toBeUndefined();
    expect(docker(psqlArgs(id, 'app2', app2.password, 'app2', 'SELECT 1'))).toBe('1');

    // 5. A user that owns a database cannot be deleted; reader can.
    const refused = await panel.raw('DELETE', `/api/databases/${id}/users/app1`);
    expect(refused.status).toBe(409);
    expect(refused.body).toContain('owns databases: app1');
    const deleted = await panel.raw('DELETE', `/api/databases/${id}/users/reader`);
    expect(deleted.status).toBe(204);
    expect(tryDocker(psqlArgs(id, 'reader', reader.password, 'app1', 'SELECT 1'))).toBeUndefined();

    // 6. Restore app1's Backup over app1: it stays restricted.
    await panel.json('POST', `/api/databases/${id}/backups`, { databases: ['app1'] });
    let backupId = 0;
    await expect
      .poll(async () => {
        const list = await panel.json<{ id: number; database: string; status: string }[]>('GET', `/api/databases/${id}/backups`);
        const done = list.find((b) => b.database === 'app1' && b.status === 'succeeded');
        backupId = done?.id ?? 0;
        return backupId > 0;
      }, { timeout: 120_000 })
      .toBe(true);
    await panel.json('POST', `/api/databases/${id}/restores`, { backupId, target: 'app1', confirm: name });
    await expect
      .poll(async () => (await panel.json<{ status: string }[]>('GET', `/api/databases/${id}/restores`))[0]?.status, { timeout: 120_000 })
      .toBe('succeeded');
    expect(tryDocker(psqlArgs(id, 'app2', app2.password, 'app1', 'SELECT 1'))).toBeUndefined();
    expect(docker(psqlArgs(id, 'app1', app1.password, 'app1', 'SELECT count(*) FROM t'))).toBe('1');
    const dbs = await panel.json<{ databases: { name: string; anyUserCanConnect: boolean }[] }>('GET', `/api/databases/${id}/logical-databases/details`);
    expect(dbs.databases.find((d) => d.name === 'app1')?.anyUserCanConnect).toBe(false);

    await removeDatabase(panel, id, name);
  });

  for (const engine of [
    { name: 'mysql', version: '8.4', image: 'mysql:8.4', client: 'mysql' },
    { name: 'mariadb', version: '11.8', image: 'mariadb:11.8', client: 'mariadb' },
  ]) {
    test(`${engine.name}: Read-only on app_1 does not reach appx1`, async () => {
      const name = `e2e-du-${engine.name}-${runId()}`;
      const panel = await Panel.signIn();
      const id = await createRunning(panel, name, engine.name, engine.version);
      docker('pull', '-q', engine.image);
      const run = (user: string, password: string, sql: string) =>
        docker(mysqlArgs(engine.image, engine.client, id, user, password, '', sql));
      const attempt = (user: string, password: string, sql: string) =>
        tryDocker(mysqlArgs(engine.image, engine.client, id, user, password, '', sql));

      await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app_1', createOwner: true });
      await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'appx1', createOwner: true });
      const owner1 = await creds(panel, id, 'app_1', 'app_1');
      const owner2 = await creds(panel, id, 'appx1', 'appx1');
      run('app_1', owner1.password, 'CREATE TABLE app_1.t (a int); INSERT INTO app_1.t VALUES (1)');
      run('appx1', owner2.password, 'CREATE TABLE appx1.t (a int); INSERT INTO appx1.t VALUES (2)');

      await panel.json('POST', `/api/databases/${id}/users`, { name: 'reader' });
      await panel.json('PUT', `/api/databases/${id}/users/reader/access`, { access: { app_1: 'read', appx1: 'none' } });
      const reader = await creds(panel, id, 'reader', 'app_1');
      expect(run('reader', reader.password, 'SELECT a FROM app_1.t')).toBe('1');
      expect(attempt('reader', reader.password, 'SELECT a FROM appx1.t')).toBeUndefined();
      expect(attempt('reader', reader.password, 'INSERT INTO app_1.t VALUES (9)')).toBeUndefined();

      // Full lets it write.
      await panel.json('PUT', `/api/databases/${id}/users/reader/access`, { access: { app_1: 'full', appx1: 'none' } });
      run('reader', reader.password, 'INSERT INTO app_1.t VALUES (9)');
      expect(run('reader', reader.password, 'SELECT count(*) FROM app_1.t')).toBe('2');
      expect(attempt('reader', reader.password, 'SELECT a FROM appx1.t')).toBeUndefined();

      await removeDatabase(panel, id, name);
    });
  }

  test('superuser reset: Postgres', async () => {
    const name = `e2e-du-spg-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '18');
    docker('pull', '-q', PG);
    const before = await creds(panel, id);
    docker(psqlArgs(id, 'postgres', before.password, 'postgres', 'CREATE TABLE keep (a int); INSERT INTO keep VALUES (42)'));

    expect((await panel.raw('POST', `/api/databases/${id}/reset-password`, { confirm: 'wrong' })).status).toBe(400);
    expect((await panel.raw('POST', `/api/databases/${id}/reset-password`, { confirm: name })).status).toBe(202);
    await settled(panel, id);

    const after = await creds(panel, id);
    expect(after.password).not.toBe(before.password);
    expect(tryDocker(psqlArgs(id, 'postgres', before.password, 'postgres', 'SELECT 1'))).toBeUndefined();
    expect(docker(psqlArgs(id, 'postgres', after.password, 'postgres', 'SELECT a FROM keep'))).toBe('42');
    await expectClean(panel, id, before.password, after.password);
    await removeDatabase(panel, id, name);
  });

  test('superuser reset: MySQL', async () => {
    const name = `e2e-du-smy-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'mysql', '8.4');
    docker('pull', '-q', 'mysql:8.4');
    const sql = (password: string, text: string) => mysqlArgs('mysql:8.4', 'mysql', id, 'root', password, '', text);
    const before = await creds(panel, id);
    docker(sql(before.password, 'CREATE DATABASE keepdb; CREATE TABLE keepdb.keep (a int); INSERT INTO keepdb.keep VALUES (42)'));

    expect((await panel.raw('POST', `/api/databases/${id}/reset-password`, { confirm: name })).status).toBe(202);
    await settled(panel, id);

    const after = await creds(panel, id);
    expect(after.password).not.toBe(before.password);
    expect(tryDocker(sql(before.password, 'SELECT 1'))).toBeUndefined();
    expect(docker(sql(after.password, 'SELECT a FROM keepdb.keep'))).toBe('42');
    await expectClean(panel, id, before.password, after.password);
    await removeDatabase(panel, id, name);
  });

  test('superuser reset: Redis', async () => {
    const name = `e2e-du-sre-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'redis', '8');
    docker('pull', '-q', 'redis:8');
    const before = await creds(panel, id);
    expect(docker(redisArgs(id, before.password, 'SET', 'keep', '42'))).toBe('OK');
    docker(redisArgs(id, before.password, 'SAVE'));

    expect((await panel.raw('POST', `/api/databases/${id}/reset-password`, { confirm: name })).status).toBe(202);
    await settled(panel, id);

    const after = await creds(panel, id);
    expect(after.password).not.toBe(before.password);
    expect(tryDocker(redisArgs(id, before.password, 'PING')) ?? '').not.toContain('PONG');
    expect(docker(redisArgs(id, after.password, 'GET', 'keep'))).toBe('42');
    await expectClean(panel, id, before.password, after.password);
    await removeDatabase(panel, id, name);
  });
});

/** No warning on the Database, and neither password in its Logs. */
async function expectClean(panel: Panel, id: number, oldPassword: string, newPassword: string) {
  const d = await panel.json<{ warnings: string[]; lastError: string }>('GET', `/api/databases/${id}`);
  expect(d.warnings).toEqual([]);
  expect(d.lastError).toBe('');
  const { logs } = await panel.json<{ logs: string }>('GET', `/api/databases/${id}/logs?tail=2000`);
  expect(logs).not.toContain(oldPassword);
  expect(logs).not.toContain(newPassword);
}
