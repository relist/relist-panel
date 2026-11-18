import { execFileSync } from 'node:child_process';
import { expect, test, type Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// A logical database's owner, which a non-Admin grantee may hold, plants
// code in its own database. None of it may ever run as the superuser:
// - a per-database setting replayed after a restore (stored with
//   SET … FROM CURRENT, so the server never quoted it);
// - an operator shadowing the catalog's through the database's search_path,
//   hit by the Users tab's Save and the Query tab's table tree;
// - a view whose function runs with the querying user's rights, hit by an
//   Admin's Query run.
// The planted functions touch a marker file only when they run as a
// superuser, so a marker in the container is the exploit.

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

type Run = { args: string[]; secret?: Record<string, string>; input?: string };

function docker(first: string | Run, ...rest: string[]): string {
  const run: Run = typeof first === 'string' ? { args: [first, ...rest] } : first;
  return execFileSync('docker', run.args, {
    encoding: 'utf8',
    env: { ...dockerEnv(), ...run.secret },
    input: run.input,
    stdio: [run.input === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'],
  }).trim();
}

/** The mariadb client on stdin, so a script can change its DELIMITER. */
function maria(id: number, user: string, password: string, database: string, script: string): Run {
  return {
    args: ['run', '--rm', '-i', '--network', 'relist-network', '-e', 'MYSQL_PWD', 'mariadb:11.8',
      'mariadb', '-h', `relist-db-${id}`, '-u', user, '-N', '-D', database],
    secret: { MYSQL_PWD: password },
    input: script,
  };
}

function psql(id: number, user: string, password: string, database: string, sql: string): Run {
  return {
    args: ['run', '--rm', '--network', 'relist-network', '-e', 'PGPASSWORD', '-e', 'PGCONNECT_TIMEOUT=10', 'postgres:18',
      'psql', '-h', `relist-db-${id}`, '-U', user, '-d', database, '-At', '-v', 'ON_ERROR_STOP=1', '-c', sql],
    secret: { PGPASSWORD: password },
  };
}

/** Whether a planted function ran as a superuser and left its marker. */
function pwned(id: number, marker: string): boolean {
  try {
    docker('exec', `relist-db-${id}`, 'test', '-e', `/tmp/${marker}`);
    return true;
  } catch {
    return false;
  }
}

/** plpgsql that touches /tmp/<marker> when the caller is a superuser. */
function hook(marker: string): string {
  return `IF (SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = current_user) THEN
    EXECUTE 'COPY (SELECT 1) TO PROGRAM ''touch /tmp/${marker}''';
  END IF;`;
}

type Creds = { user: string; password: string };

async function creds(panel: Panel, id: number, user?: string, database?: string): Promise<Creds> {
  const query = user ? `?user=${encodeURIComponent(user)}&database=${encodeURIComponent(database ?? '')}` : '';
  return panel.json<Creds>('GET', `/api/databases/${id}/credentials${query}`);
}

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
}

test.describe('owner-planted code never runs as the superuser', () => {
  test.describe.configure({ mode: 'serial', timeout: 10 * 60_000 });

  let panel: Panel;
  let id = 0;
  let name = '';
  let su: Creds;

  test.beforeAll(async () => {
    test.setTimeout(10 * 60_000);
    name = `e2e-sec-${runId()}`;
    panel = await Panel.signIn();
    ({ id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine: 'postgres', version: '18' }));
    await expect
      .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 300_000 })
      .toBe('running');
    docker('pull', '-q', 'postgres:18');
    for (const db of ['app1', 'app2', 'app3']) {
      await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: db, createOwner: true });
    }
    su = await creds(panel, id);
  });

  test.afterAll(async () => {
    if (id) await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
  });

  const isSuper = (role: string) =>
    docker(psql(id, 'postgres', su.password, 'postgres', `SELECT rolsuper FROM pg_roles WHERE rolname = '${role}'`));

  test('a setting stored FROM CURRENT is replayed as a value after a restore', async () => {
    const owner = await creds(panel, id, 'app1', 'app1');
    docker(psql(id, 'app1', owner.password, 'app1',
      "SELECT set_config('search_path', 'x;ALTER/**/ROLE/**/app1/**/SUPERUSER;--', false); " +
        'ALTER ROLE CURRENT_USER IN DATABASE app1 SET search_path FROM CURRENT'));

    await panel.json('POST', `/api/databases/${id}/backups`, { databases: ['app1'] });
    let backupId = 0;
    await expect
      .poll(async () => {
        const list = await panel.json<{ id: number; database: string; status: string }[]>('GET', `/api/databases/${id}/backups`);
        backupId = list.find((b) => b.database === 'app1' && b.status === 'succeeded')?.id ?? 0;
        return backupId > 0;
      }, { timeout: 120_000 })
      .toBe(true);
    await panel.json('POST', `/api/databases/${id}/restores`, { backupId, target: 'app1', confirm: name });
    await expect
      .poll(async () => (await panel.json<{ status: string }[]>('GET', `/api/databases/${id}/restores`))[0]?.status, { timeout: 120_000 })
      .toBe('succeeded');

    expect(isSuper('app1')).toBe('f');
    const kept = docker(psql(id, 'postgres', su.password, 'postgres',
      "SELECT array_to_string(setconfig, ' ') FROM pg_db_role_setting s JOIN pg_roles r ON r.oid = s.setrole " +
        "JOIN pg_database d ON d.oid = s.setdatabase WHERE r.rolname = 'app1' AND d.datname = 'app1'"));
    expect(kept).toContain('search_path=');
  });

  test("a shadowing operator in the database's search_path does not hijack the Users tab or the table tree", async () => {
    const owner = await creds(panel, id, 'app2', 'app2');
    docker(psql(id, 'app2', owner.password, 'app2', `
      CREATE SCHEMA evil;
      CREATE FUNCTION evil.f(name, text) RETURNS boolean LANGUAGE plpgsql AS $$ BEGIN
        ${hook('pwned-operator')}
        RETURN pg_catalog.namenlike($1, $2);
      END $$;
      CREATE OPERATOR evil.!~~ (LEFTARG = name, RIGHTARG = text, FUNCTION = evil.f);
      ALTER DATABASE app2 SET search_path = evil, pg_catalog;`));

    await panel.json('POST', `/api/databases/${id}/users`, { name: 'reader2' });
    await panel.json('PUT', `/api/databases/${id}/users/reader2/access`, { access: { app2: 'read' } });
    await panel.json('GET', `/api/databases/${id}/query/schema?database=app2`);
    // A run the Admin chose to make as the superuser starts on pg_catalog too.
    const asSuper = await panel.json<{ user: string; error: { message: string } | null }>('POST', `/api/databases/${id}/query`, {
      database: 'app2', sql: "SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname NOT LIKE 'pg%'", asSuperuser: true });
    expect(asSuper.error).toBeNull();
    expect(asSuper.user).toBe('postgres');

    expect(pwned(id, 'pwned-operator')).toBe(false);
    expect(isSuper('app2')).toBe('f');
  });

  test("a view's function runs as the owner, not the superuser, when an Admin queries it", async ({ page }) => {
    const owner = await creds(panel, id, 'app3', 'app3');
    docker(psql(id, 'app3', owner.password, 'app3', `
      CREATE TABLE orders_data (n int);
      INSERT INTO orders_data VALUES (7);
      CREATE FUNCTION public.hook_rows() RETURNS SETOF orders_data LANGUAGE plpgsql AS $$ BEGIN
        ${hook('pwned-view')}
        RETURN QUERY SELECT * FROM orders_data;
      END $$;
      CREATE VIEW public.orders AS SELECT * FROM public.hook_rows();`));

    // Clicking the view in the table tree fills the editor; it runs nothing.
    await signIn(page);
    await page.goto(`/databases/${id}`);
    await page.getByRole('tab', { name: 'Query' }).click();
    await page.getByRole('combobox', { name: 'Database' }).click();
    await page.getByTitle('app3', { exact: true }).click();
    const orders = page.locator('.ant-tree-treenode').filter({ hasText: 'orders' });
    await expect(orders.getByText('view', { exact: true })).toBeVisible();
    await page.getByText('orders', { exact: true }).click();
    await page.waitForTimeout(2_000);
    expect(pwned(id, 'pwned-view')).toBe(false);

    // Run in the console connects as the owner too, and says so.
    await page.getByRole('button', { name: 'Run', exact: true }).click();
    await expect(page.getByText('Ran as app3')).toBeVisible();
    expect(pwned(id, 'pwned-view')).toBe(false);

    // Run, by default, connects as the database's owner.
    const run = await panel.json<{ user: string; results: { rows: unknown[][] }[]; error: { message: string } | null }>(
      'POST', `/api/databases/${id}/query`, { database: 'app3', sql: 'SELECT n FROM orders' });
    expect(run.error).toBeNull();
    expect(run.user).toBe('app3');
    expect(run.results[0].rows).toEqual([['7']]);
    expect(pwned(id, 'pwned-view')).toBe(false);

    // The superuser is an explicit choice.
    const asSuper = await panel.json<{ user: string; results: { rows: unknown[][] }[] }>(
      'POST', `/api/databases/${id}/query`, { database: 'app3', sql: 'SELECT current_user', asSuperuser: true });
    expect(asSuper.user).toBe('postgres');
    expect(asSuper.results[0].rows).toEqual([['postgres']]);
  });
});

test.describe('mariadb', () => {
  test.describe.configure({ timeout: 10 * 60_000 });

  test("a Full user's view does not run as root unless the Admin chooses it", async () => {
    const name = `e2e-sec-maria-${runId()}`;
    const panel = await Panel.signIn();
    const { id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine: 'mariadb', version: '11.8' });
    try {
      await expect
        .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 300_000 })
        .toBe('running');
      docker('pull', '-q', 'mariadb:11.8');
      await panel.json('POST', `/api/databases/${id}/logical-databases`, { name: 'shop', createOwner: true });
      const owner = await creds(panel, id, 'shop', 'shop');
      const root = await creds(panel, id);
      // As the reader, the function copies what only root can read.
      docker(maria(id, 'shop', owner.password, 'shop', `CREATE TABLE loot (n BIGINT);
DELIMITER //
CREATE FUNCTION f() RETURNS INT SQL SECURITY INVOKER MODIFIES SQL DATA
BEGIN
  DECLARE CONTINUE HANDLER FOR SQLEXCEPTION BEGIN END;
  INSERT INTO shop.loot SELECT COUNT(*) FROM mysql.user;
  RETURN 1;
END//
DELIMITER ;
CREATE SQL SECURITY INVOKER VIEW orders AS SELECT f() AS n;
`));
      const loot = () => docker(maria(id, root.user, root.password, 'shop', 'SELECT COUNT(*) FROM loot;'));

      const refused = await panel.raw('POST', `/api/databases/${id}/query`, { database: 'shop', sql: 'SELECT * FROM orders' });
      expect(refused.status).toBe(409);
      expect(refused.body).toContain('Run as superuser');
      expect(loot()).toBe('0');

      // The switch really is root: the function's copy now succeeds.
      const chosen = await panel.json<{ user: string }>('POST', `/api/databases/${id}/query`,
        { database: 'shop', sql: 'SELECT * FROM orders', asSuperuser: true });
      expect(chosen.user).toBe('root');
      expect(loot()).toBe('1');
    } finally {
      await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
    }
  });
});
