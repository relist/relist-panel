import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { createMember, Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 8: per-Database access for non-Admins, and Preview
// (ADR-0045). A member with a grant works in one Database as a managed user
// (SQL) or at a level (Redis), never as the superuser; a managed user with
// server-wide powers is refused at grant time and again before every run.
// Preview runs DML in a transaction and rolls it back.

function dockerEnv() {
  const env = sandbox();
  return { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig };
}

function docker(...args: string[]): string {
  return execFileSync('docker', args, { encoding: 'utf8', env: dockerEnv(), stdio: ['ignore', 'pipe', 'pipe'] }).trim();
}

const PG = 'postgres:18';

async function createRunning(panel: Panel, name: string, engine: string, version: string): Promise<number> {
  const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine, version });
  await expect
    .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 240_000 })
    .toBe('running');
  return id;
}

async function removeDatabase(panel: Panel, id: number, name: string) {
  await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
}

type QueryAnswer = {
  preview?: boolean;
  results: { affected: number | null; command: string; rows: unknown[][]; target?: string; verb?: string }[];
  error: null | { message: string; statementIndex: number };
};

function query(panel: Panel, id: number, database: string, sql: string, extra: Record<string, unknown> = {}) {
  return panel.json<QueryAnswer>('POST', `/api/databases/${id}/query`, { database, sql, writes: false, ...extra });
}

const MEMBER_PASSWORD = 'e2e-member-passw0rd!';

test.describe('database access for non-admins', () => {
  test.describe.configure({ mode: 'serial', timeout: 10 * 60_000 });

  test('Postgres: a grantee runs as its managed user, server-wide users are refused, previews roll back', async () => {
    const name = `e2e-da-pg-${runId()}`;
    const other = `e2e-da-pg2-${runId()}`;
    const admin = await Panel.signIn();
    const a = await createRunning(admin, name, 'postgres', '18');
    const b = await createRunning(admin, other, 'postgres', '18');
    docker('pull', '-q', PG);

    // shop with its owner; shop_ro Read-only on it; 10 rows as shop.
    await admin.json('POST', `/api/databases/${a}/logical-databases`, { name: 'shop', createOwner: true });
    await admin.json('POST', `/api/databases/${a}/users`, { name: 'shop_ro' });
    await admin.json('PUT', `/api/databases/${a}/users/shop_ro/access`, { access: { shop: 'read' } });
    const seed = await query(admin, a, 'shop', 'CREATE TABLE t (id int); INSERT INTO t SELECT generate_series(1, 10)', { writes: true });
    expect(seed.error).toBeNull();

    // A superuser role made in the Query panel, then managed by a reset.
    const madeBoss = await query(admin, a, 'postgres', 'CREATE ROLE boss SUPERUSER LOGIN', { writes: true });
    expect(madeBoss.error).toBeNull();
    await admin.json('POST', `/api/databases/${a}/users/boss/reset-password`);

    const email = `alice-${runId()}@example.com`;
    const alice = await createMember(admin, email, MEMBER_PASSWORD);
    const aliceId = (await admin.json<{ id: number; email: string }[]>('GET', '/api/users'))
      .find((row) => row.email === email)!.id;

    // boss has server-wide powers: the grant is refused.
    const refusedGrant = await admin.raw('PUT', `/api/databases/${a}/grants/${aliceId}`, { dbUser: 'boss' });
    expect(refusedGrant.status).toBe(422);
    expect(String(refusedGrant.body)).toContain('server-wide privileges');

    // Alice uses shop as shop_ro.
    expect((await admin.raw('PUT', `/api/databases/${a}/grants/${aliceId}`, { dbUser: 'shop_ro' })).status).toBe(204);

    // Her list holds only A, as shop_ro; B and A's admin routes are closed.
    const mine = await alice.json<{ id: number; user: string; access?: { dbUser?: string } }[]>('GET', '/api/databases');
    expect(mine.map((row) => row.id)).toEqual([a]);
    expect(mine[0].user).toBe('shop_ro');
    expect(mine[0].access?.dbUser).toBe('shop_ro');
    expect((await alice.raw('GET', `/api/databases/${b}`)).status).toBe(404);
    expect((await alice.raw('POST', `/api/databases/${b}/query`, { database: 'postgres', sql: 'SELECT 1', writes: false })).status).toBe(404);
    expect((await alice.raw('GET', `/api/databases/${a}/logs`)).status).toBe(403);

    // Credentials always answer her own user.
    const creds = await alice.json<{ user: string }>('GET', `/api/databases/${a}/credentials?user=postgres`);
    expect(creds.user).toBe('shop_ro');

    // Her runs are shop_ro's: read yes, write and server files no.
    const count = await query(alice, a, 'shop', 'SELECT count(*) FROM t');
    expect(count.results[0].rows[0][0]).toBe('10');
    const write = await query(alice, a, 'shop', 'DELETE FROM t', { writes: true });
    expect(write.error?.message).toContain('permission denied');
    const readFile = await query(alice, a, 'shop', `SELECT pg_read_file('/etc/passwd')`);
    expect(readFile.error?.message).toContain('permission denied');
    const copyProgram = await query(alice, a, 'shop', `COPY (SELECT 1) TO PROGRAM 'id'`, { writes: true });
    expect(copyProgram.error?.message).toContain('permission denied');

    // Privileges change later: the run's own check catches it.
    expect((await query(admin, a, 'postgres', 'ALTER ROLE shop_ro SUPERUSER', { writes: true })).error).toBeNull();
    const nowWide = await query(alice, a, 'shop', 'SELECT 1');
    expect(nowWide.error?.message).toContain('server-wide privileges');
    expect(nowWide.results).toEqual([]);
    expect((await query(admin, a, 'postgres', 'ALTER ROLE shop_ro NOSUPERUSER', { writes: true })).error).toBeNull();

    // A Full member runs under SET role: session_user still catches it.
    await admin.json('POST', `/api/databases/${a}/users`, { name: 'shop_rw' });
    await admin.json('PUT', `/api/databases/${a}/users/shop_rw/access`, { access: { shop: 'full' } });
    expect((await admin.raw('PUT', `/api/databases/${a}/grants/${aliceId}`, { dbUser: 'shop_rw' })).status).toBe(204);
    expect((await query(admin, a, 'postgres', 'ALTER ROLE shop_rw SUPERUSER', { writes: true })).error).toBeNull();
    const fullWide = await query(alice, a, 'shop', `RESET ROLE; SELECT pg_read_file('/etc/passwd')`, { writes: true });
    expect(fullWide.error?.message).toContain('server-wide privileges');
    expect(fullWide.results).toEqual([]);
    expect((await query(admin, a, 'postgres', 'ALTER ROLE shop_rw NOSUPERUSER', { writes: true })).error).toBeNull();
    expect((await admin.raw('PUT', `/api/databases/${a}/grants/${aliceId}`, { dbUser: 'shop_ro' })).status).toBe(204);

    // The audit log names the panel user and the database user.
    const audit = await admin.json<{ items: { action: string; actor: string; detail: { dbUser?: string } }[] }>(
      'GET', '/api/audit?action=database.query');
    const hers = audit.items.find((event) => event.actor.startsWith('alice-') && event.detail.dbUser === 'shop_ro');
    expect(hers).toBeTruthy();

    // Preview, as Admin: reports, then rolls back.
    const preview = await query(admin, a, 'shop', 'DELETE FROM t WHERE id <= 3', { preview: true, writes: true });
    expect(preview.preview).toBe(true);
    expect(preview.error).toBeNull();
    expect(preview.results[0].affected).toBe(3);
    expect(preview.results[0].verb).toBe('delete');
    expect(preview.results[0].target).toBe('t');
    const still = await query(admin, a, 'shop', 'SELECT count(*) FROM t');
    expect(still.results[0].rows[0][0]).toBe('10');
    const ddl = await query(admin, a, 'shop', 'TRUNCATE t', { preview: true, writes: true });
    expect(ddl.error?.statementIndex).toBe(0);
    expect(ddl.error?.message).toContain('starts with TRUNCATE');
    const commit = await query(admin, a, 'shop', 'DELETE FROM t WHERE id = 1; COMMIT', { preview: true, writes: true });
    expect(commit.error?.statementIndex).toBe(1);
    const after = await query(admin, a, 'shop', 'SELECT count(*) FROM t');
    expect(after.results[0].rows[0][0]).toBe('10');

    // Deleting shop_ro asks about Alice, then takes her access with it.
    const inUse = await admin.raw('DELETE', `/api/databases/${a}/users/shop_ro`);
    expect(inUse.status).toBe(409);
    expect(String(inUse.body)).toContain('alice-');
    expect((await admin.raw('DELETE', `/api/databases/${a}/users/shop_ro?confirmGrants=1`)).status).toBe(204);
    expect(await admin.json<unknown[]>('GET', `/api/databases/${a}/grants`)).toEqual([]);
    expect((await alice.raw('GET', `/api/databases/${a}`)).status).toBe(404);

    await removeDatabase(admin, a, name);
    await removeDatabase(admin, b, other);
  });

  test('MySQL: INTO OUTFILE is refused, FILE makes the user server-wide, MyISAM blocks Preview', async () => {
    const name = `e2e-da-my-${runId()}`;
    const admin = await Panel.signIn();
    const id = await createRunning(admin, name, 'mysql', '8.4');

    await admin.json('POST', `/api/databases/${id}/logical-databases`, { name: 'app', createOwner: true });
    const alice = await createMember(admin, `amy-${runId()}@example.com`, MEMBER_PASSWORD);
    const aliceId = (await admin.json<{ id: number; email: string }[]>('GET', '/api/users'))
      .find((row) => row.email.startsWith('amy-'))!.id;
    expect((await admin.raw('PUT', `/api/databases/${id}/grants/${aliceId}`, { dbUser: 'app' })).status).toBe(204);

    // The server itself refuses the file write: app holds no FILE privilege.
    const outfile = await query(alice, id, 'app', `SELECT 1 INTO OUTFILE '/tmp/x'`, { writes: true });
    expect(outfile.error?.message).toMatch(/access denied|secure.file.priv/i);

    // FILE makes it server-wide: the next run is refused before anything runs.
    expect((await query(admin, id, '', `GRANT FILE ON *.* TO 'app'@'%'`, { writes: true })).error).toBeNull();
    const wide = await query(alice, id, 'app', 'SELECT 1');
    expect(wide.error?.message).toContain('server-wide privileges');
    expect((await query(admin, id, '', `REVOKE FILE ON *.* FROM 'app'@'%'`, { writes: true })).error).toBeNull();
    expect((await query(alice, id, 'app', 'SELECT 1')).error).toBeNull();

    // A MyISAM table refuses every Preview, by name.
    expect((await query(admin, id, 'app', 'CREATE TABLE legacy (a int) ENGINE=MyISAM', { writes: true })).error).toBeNull();
    const refused = await query(admin, id, 'app', 'DELETE FROM legacy', { preview: true, writes: true });
    expect(refused.error?.message).toContain('app.legacy (MyISAM)');
    expect((await query(admin, id, 'app', 'DROP TABLE legacy', { writes: true })).error).toBeNull();
    const ok = await query(admin, id, 'app', 'CREATE TABLE t (a int) ENGINE=InnoDB; INSERT INTO t VALUES (1)', { writes: true });
    expect(ok.error).toBeNull();
    const preview = await query(admin, id, 'app', 'DELETE FROM t', { preview: true, writes: true });
    expect(preview.error).toBeNull();
    expect(preview.results.at(-1)?.affected).toBe(1);
    const still = await query(admin, id, 'app', 'SELECT count(*) FROM t');
    expect(still.results[0].rows[0][0]).toBe('1');

    await removeDatabase(admin, id, name);
  });

  test('Redis: Read reads only, Read-write never confirms, no credentials; deleting the member deletes the grant', async () => {
    const name = `e2e-da-re-${runId()}`;
    const admin = await Panel.signIn();
    const id = await createRunning(admin, name, 'redis', '8');
    const email = `ared-${runId()}@example.com`;
    const alice = await createMember(admin, email, MEMBER_PASSWORD);
    const aliceId = (await admin.json<{ id: number; email: string }[]>('GET', '/api/users'))
      .find((row) => row.email === email)!.id;
    expect((await admin.raw('PUT', `/api/databases/${id}/grants/${aliceId}`, { level: 'read' })).status).toBe(204);

    const seeded = await admin.json<{ reply: { value: unknown } }>('POST', `/api/databases/${id}/redis/command`, {
      command: 'SET k v', db: 0, writes: true,
    });
    expect(seeded.reply.value).toBe('OK');

    const run = (command: string, writes = false) =>
      alice.json<{ reply?: { value?: unknown } | null; refused?: string; needsConfirm?: boolean }>(
        'POST', `/api/databases/${id}/redis/command`, { command, db: 0, writes });
    expect((await run('GET k')).reply?.value).toBe('v');
    expect((await run('SET k w', true)).refused).toContain('writes are off');
    expect((await run('CONFIG GET requirepass')).refused).toContain('not available to non-Admins');
    expect((await run('EVAL "return 1" 0', true)).refused).toContain('not available in the console');
    expect((await alice.raw('GET', `/api/databases/${id}/credentials`)).status).toBe(403);

    expect((await admin.raw('PUT', `/api/databases/${id}/grants/${aliceId}`, { level: 'read_write' })).status).toBe(204);
    expect((await run('SET k w', true)).reply?.value).toBe('OK');
    const flush = await run('FLUSHDB', true);
    expect(flush.refused).toContain('not available to non-Admins');
    expect(flush.needsConfirm ?? false).toBe(false);

    // Deleting the panel user removes the grant with it.
    expect((await admin.raw('DELETE', `/api/users/${aliceId}`)).status).toBe(204);
    expect(await admin.json<unknown[]>('GET', `/api/databases/${id}/grants`)).toEqual([]);

    await removeDatabase(admin, id, name);
  });

  test('browser: a grantee sees Data > Databases, two tabs, and a refused Preview', async ({ page }) => {
    const name = `e2e-da-ui-${runId()}`;
    const admin = await Panel.signIn();
    const id = await createRunning(admin, name, 'postgres', '18');
    await admin.json('POST', `/api/databases/${id}/logical-databases`, { name: 'shop', createOwner: true });
    await admin.json('POST', `/api/databases/${id}/users`, { name: 'shop_ro' });
    await admin.json('PUT', `/api/databases/${id}/users/shop_ro/access`, { access: { shop: 'read' } });
    expect((await admin.json<QueryAnswer>('POST', `/api/databases/${id}/query`, {
      database: 'shop', sql: 'CREATE TABLE t (id int); INSERT INTO t SELECT generate_series(1, 10)', writes: true,
    })).error).toBeNull();
    const email = `aui-${runId()}@example.com`;
    await createMember(admin, email, MEMBER_PASSWORD);
    const aliceId = (await admin.json<{ id: number; email: string }[]>('GET', '/api/users'))
      .find((row) => row.email === email)!.id;
    expect((await admin.raw('PUT', `/api/databases/${id}/grants/${aliceId}`, { dbUser: 'shop_ro' })).status).toBe(204);

    await page.goto('/signin');
    await page.getByLabel(/email/i).fill(email);
    await page.getByLabel(/password/i).first().fill(MEMBER_PASSWORD);
    await page.getByRole('button', { name: /sign in/i }).click();
    await page.waitForURL((u) => !u.pathname.startsWith('/signin'));

    const data = page.locator('.ant-menu-item-group').filter({ has: page.locator('.ant-menu-item-group-title', { hasText: 'Data' }) });
    await data.getByRole('menuitem', { name: 'Databases' }).click();
    await page.getByRole('link', { name: name }).click();

    // Only Connection and Query, landing on Query.
    await expect(page.getByRole('tab')).toHaveText(['Connection', 'Query']);
    await expect(page.getByRole('tab', { name: 'Query' })).toHaveAttribute('aria-selected', 'true');

    // A Preview as shop_ro is refused by its own permissions.
    await page.getByRole('switch', { name: 'Allow writes' }).click();
    await page.locator('.monaco-editor .inputarea').first().click({ force: true });
    await page.keyboard.type('DELETE FROM t WHERE id <= 3');
    await page.getByRole('button', { name: 'Preview' }).click();
    await expect(page.getByText(/permission denied/)).toBeVisible();

    await removeDatabase(admin, id, name);
  });
});
