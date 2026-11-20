import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 4: the Query tab's API, through the panel URL (Traefik), on
// Postgres 18, MySQL 8.4 and MariaDB 11.8. Each engine gets a fresh Database,
// seeded through the API with writes allowed, and deletes it afterwards.

type Cell = null | string | { text?: string; hex?: string; bytes: number };
type Result = { statementIndex: number; columns: { name: string }[]; rows: Cell[][]; truncated: boolean; command: string; affected: number | null };
type Response = {
  results: Result[];
  durationMs: number;
  error: null | { message: string; statementIndex: number; statementStart: number; position: number | null };
  stopped: string;
  cancelled: boolean;
};

const READ_ONLY = /read[- ]only transaction/i;

type Engine = {
  engine: 'postgres' | 'mysql' | 'mariadb';
  version: string;
  /** The logical database every run uses. */
  db: string;
  /** Statements that need a Database first (MySQL has no default one). */
  create?: string;
  seed: string[];
  /** Seeds a table `big` of 2,000,000 rows; each entry is one run. */
  big: string[];
  /** Counts of what the refused writes would have made; all must stay 0. */
  untouched: string;
  sleep: string;
  /** Counts the server's running copies of `text`, not counting this very probe. */
  activity: (text: string) => string;
  bigint: string;
  repeat: string;
};

const ENGINES: Engine[] = [
  {
    engine: 'postgres',
    version: '18',
    db: 'postgres',
    seed: [
      'CREATE TABLE items (id int PRIMARY KEY, name text)',
      "INSERT INTO items VALUES (1, 'a'), (2, 'b')",
      'CREATE SEQUENCE sq',
    ],
    big: ['CREATE TABLE big AS SELECT g AS id, md5(g::text) AS h FROM generate_series(1, 2000000) g'],
    untouched:
      "SELECT (SELECT count(*) FROM information_schema.tables WHERE table_name = 'nope') + (SELECT count(*) FROM pg_database WHERE datname = 'nopedb') + (SELECT count(*) FROM pg_roles WHERE rolname = 'nopeuser') + (SELECT count(*) FROM items WHERE id > 2) AS changed",
    sleep: 'SELECT pg_sleep(60)',
    activity: (text) => `SELECT count(*) AS n FROM pg_stat_activity WHERE state = 'active' AND pid <> pg_backend_pid() AND query = '${text}'`,
    bigint: 'SELECT 9007199254740993::bigint AS n, \'\'::text AS e, \'\\xfffe\'::bytea AS b',
    repeat: "SELECT repeat('a', 1000000) AS t",
  },
  ...(['mysql', 'mariadb'] as const).map(
    (engine): Engine => ({
      engine,
      version: engine === 'mysql' ? '8.4' : '11.8',
      db: 'shop',
      create: 'CREATE DATABASE shop',
      seed: [
        'CREATE TABLE items (id int PRIMARY KEY, name varchar(20))',
        "INSERT INTO items VALUES (1, 'a'), (2, 'b')",
      ],
      // 1 row, then the table doubles into itself 21 times: 2^21 rows is 2,097,152.
      big: [
        'CREATE TABLE big (id int, h varchar(40))',
        "INSERT INTO big VALUES (1, 'x')",
        ...[14, 3, 1, 1, 1, 1].map((n) => Array(n).fill('INSERT INTO big SELECT id, h FROM big').join(';\n')),
      ],
      untouched:
        "SELECT (SELECT count(*) FROM information_schema.tables WHERE table_name = 'nope') + (SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'nopedb') + (SELECT count(*) FROM mysql.user WHERE user = 'nopeuser') + (SELECT count(*) FROM items WHERE id > 2) AS changed",
      sleep: 'SELECT SLEEP(60)',
      activity: (text) => `SELECT COUNT(*) AS n FROM information_schema.PROCESSLIST WHERE INFO = '${text}' AND ID <> CONNECTION_ID()`,
      bigint: "SELECT 9007199254740993 AS n, '' AS e, UNHEX('FFFE') AS b",
      repeat: "SELECT REPEAT('a', 1000000) AS t",
    }),
  ),
];

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
  await expect(page.getByRole('menuitem').first()).toBeVisible();
}

for (const e of ENGINES) {
  test.describe(`query on ${e.engine} ${e.version}`, () => {
    test.describe.configure({ mode: 'serial', timeout: 10 * 60_000 });
    let panel: Panel;
    let id = 0;
    let name = '';

    const run = async (sql: string, writes = false, database = e.db): Promise<Response> =>
      panel.json<Response>('POST', `/api/databases/${id}/query`, { database, sql, writes });
    const scalar = async (sql: string): Promise<string> => {
      const r = await run(sql);
      expect(r.error, sql).toBeNull();
      return r.results[0].rows[0][0] as string;
    };
    const activeCount = async (text: string): Promise<number> => Number(await scalar(e.activity(text)));
    const queryAudits = async () =>
      (await panel.json<{ items: { id: number; action: string; detail: Record<string, any>; outcome: string; target: string }[] }>('GET', '/api/audit?action=database.query'))
        .items;

    test.beforeAll(async () => {
      panel = await Panel.signIn();
      name = `e2e-q-${e.engine}-${runId()}`;
      ({ id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine: e.engine, version: e.version }));
      await expect
        .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 240_000 })
        .toBe('running');
      if (e.create) {
        expect((await run(e.create, true, '')).error).toBeNull();
      }
      for (const sql of e.seed) {
        const r = await run(sql, true);
        expect(r.error, sql).toBeNull();
      }
    });

    test.afterAll(async () => {
      if (id) {
        await panel.raw('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
      }
    });

    test('read-only refuses writes', async () => {
      const refused = [
        "INSERT INTO items VALUES (3, 'c')",
        'CREATE TABLE nope (a int)',
        'DROP TABLE items',
        'TRUNCATE items',
        'CREATE DATABASE nopedb',
        e.engine === 'postgres' ? 'CREATE ROLE nopeuser' : 'CREATE USER nopeuser',
        ...(e.engine === 'postgres'
          ? ["SELECT nextval('sq')", "DO $$ BEGIN INSERT INTO items VALUES (4, 'd'); END $$", "SELECT 1; INSERT INTO items VALUES (5, 'e')"]
          : []),
      ];
      for (const sql of refused) {
        const r = await run(sql);
        expect(r.error?.message ?? 'no error', sql).toMatch(READ_ONLY);
      }
      if (e.engine !== 'postgres') {
        // A procedure that writes is created with writes on, then refused read-only.
        const made = await run('CREATE PROCEDURE wp() INSERT INTO items VALUES (50, \'p\')', true);
        expect(made.error).toBeNull();
        expect((await run('CALL wp()')).error?.message ?? 'no error').toMatch(READ_ONLY);
        expect((await run("COMMIT; INSERT INTO items VALUES (51, 'q')")).error?.message ?? 'no error').toMatch(READ_ONLY);
      }
      expect(await scalar(e.untouched)).toBe('0');
      expect(await scalar('SELECT count(*) FROM items')).toBe('2');
    });

    test('writes allowed', async () => {
      const r = await run("INSERT INTO items VALUES (3, 'c')", true);
      expect(r.error).toBeNull();
      expect(r.results[0].affected).toBe(1);
      expect(await scalar('SELECT count(*) FROM items')).toBe('3');
      expect((await run('DELETE FROM items WHERE id = 3', true)).results[0].affected).toBe(1);
    });

    // Preview (ADR-0045, kept by ADR-0049): DML in a transaction that is
    // always rolled back.
    test('Preview reports what DML would do, then rolls it back', async () => {
      type PreviewAnswer = Omit<Response, 'results'> & { preview?: boolean; results: (Result & { verb?: string; target?: string })[] };
      const preview = (sql: string, database = e.db) =>
        panel.json<PreviewAnswer>('POST', `/api/databases/${id}/query`, { database, sql, writes: true, preview: true });

      const del = await preview('DELETE FROM items WHERE id <= 1');
      expect(del.preview).toBe(true);
      expect(del.error).toBeNull();
      expect(del.results[0].affected).toBe(1);
      expect(del.results[0].verb).toBe('delete');
      expect(del.results[0].target).toBe('items');
      expect(await scalar('SELECT count(*) FROM items')).toBe('2');

      const ddl = await preview('TRUNCATE items');
      expect(ddl.error?.statementIndex).toBe(0);
      expect(ddl.error?.message).toContain('starts with TRUNCATE');
      const commit = await preview('DELETE FROM items WHERE id = 1; COMMIT');
      expect(commit.error?.statementIndex).toBe(1);
      expect(await scalar('SELECT count(*) FROM items')).toBe('2');

      if (e.engine !== 'postgres') {
        // A table that cannot roll back refuses every Preview, by name.
        expect((await run('CREATE TABLE legacy (a int) ENGINE=MyISAM', true)).error).toBeNull();
        try {
          const refused = await preview('DELETE FROM items');
          expect(refused.error?.message).toContain(`${e.db}.legacy (MyISAM)`);
          expect(await scalar('SELECT count(*) FROM items')).toBe('2');
        } finally {
          expect((await run('DROP TABLE legacy', true)).error).toBeNull();
        }
      }
    });

    test('row cap stops the server query', async () => {
      for (const sql of e.big) {
        const r = await run(sql, true);
        expect(r.error, sql).toBeNull();
        expect(r.cancelled).toBe(false);
      }
      expect(await scalar('SELECT count(*) FROM big')).toBe(e.engine === 'postgres' ? '2000000' : '2097152');

      const began = Date.now();
      const r = await run('SELECT * FROM big');
      const took = Date.now() - began;
      console.log(`${e.engine}: SELECT * FROM big over 2M rows answered in ${took} ms`);
      expect(r.results[0].rows).toHaveLength(1000);
      expect(r.results[0].truncated).toBe(true);
      expect(r.stopped).toContain('Stopped after 1,000 rows.');
      expect(took).toBeLessThan(3000);
      await expect.poll(() => activeCount('SELECT * FROM big'), { timeout: 5000 }).toBe(0);
    });

    test('cancel kills the server query and is audited', async () => {
      // The list is paged, newest first: tell the new row by its id, not by a count.
      const lastId = Math.max(0, ...(await queryAudits()).map((a) => a.id));
      // The browser hangs up one second in; the query must be running on the
      // server until then, or its absence afterwards proves nothing.
      const pending = panel.raw('POST', `/api/databases/${id}/query`, { database: e.db, sql: e.sleep, writes: false }, 1000);
      await expect.poll(() => activeCount(e.sleep), { intervals: [100], timeout: 900 }).toBe(1);
      expect((await pending).status).toBe(0);
      const hungUp = Date.now();
      let gone = -1;
      await expect
        .poll(
          async () => {
            const n = await activeCount(e.sleep);
            if (n === 0 && gone < 0) gone = Date.now() - hungUp;
            return n;
          },
          { intervals: [100], timeout: 2000 },
        )
        .toBe(0);
      console.log(`${e.engine}: ${e.sleep} gone ${gone} ms after the browser hung up`);
      const fresh = async () => (await queryAudits()).filter((a) => a.id > lastId && a.detail.outcome === 'cancelled');
      await expect.poll(async () => (await fresh()).length, { timeout: 5000 }).toBe(1);
      const row = (await fresh())[0];
      expect(row.target).toBe(name);
      expect(row.detail.statements).toBe('SELECT');
      expect(JSON.stringify(row.detail)).not.toContain('SLEEP(');
      expect(JSON.stringify(row.detail)).not.toContain('pg_sleep');
    });

    test('values keep their digits and bytes', async () => {
      const r = await run(e.bigint);
      expect(r.error).toBeNull();
      const [n, empty, b] = r.results[0].rows[0];
      expect(n).toBe('9007199254740993');
      expect(empty).toBe('');
      if (e.engine === 'postgres') {
        expect(b).toBe('\\xfffe');
      } else {
        expect(b).toEqual({ hex: 'fffe', bytes: 2 });
      }
      const big = await run(e.repeat);
      const cell = big.results[0].rows[0][0] as { text: string; bytes: number };
      expect(cell.bytes).toBe(1000000);
      expect(cell.text.length).toBeLessThanOrEqual(65536);
      expect(cell.text.length).toBeGreaterThan(60000);
    });

    test('multi-statement batches', async () => {
      if (e.engine === 'postgres') {
        const two = await run('SELECT 1; SELECT 2');
        expect(two.results.map((r) => r.rows[0][0])).toEqual(['1', '2']);
        const bad = await run('SELECT 1; SELEC 2');
        expect(bad.error?.position).toBe(10);
        expect(bad.error?.message).toMatch(/syntax error/);
      } else {
        const bad = await run('SELECT 1; SELEC 2');
        expect(bad.results).toHaveLength(1);
        expect(bad.error).toMatchObject({ statementIndex: 1, statementStart: 10, position: null });
        const batch = [
          'DELIMITER //',
          'CREATE PROCEDURE p() BEGIN SELECT 1 AS a; SELECT 2 AS b; END//',
          'DELIMITER ;',
          'CALL p();',
        ].join('\n');
        const r = await run(batch, true);
        expect(r.error).toBeNull();
        expect(r.results.map((x) => x.command)).toEqual(['OK', 'CALL 1', 'CALL 1']);
        expect(r.results.slice(1).map((x) => x.rows[0][0])).toEqual(['1', '2']);
      }
    });

    test('the audit trail names each run by kind and keeps no text', async () => {
      const last = Math.max(0, ...(await queryAudits()).map((a) => a.id));
      await run("SELECT 'audit-marker'");
      // Each of these writes the password a way a pattern could miss.
      const secrets =
        e.engine === 'postgres'
          ? ["CREATE ROLE audited PASSWORD $pw$hunter2$pw$", "CREATE ROLE audited PASSWORD /* c */ 'hunter2'"]
          : ["CREATE USER audited IDENTIFIED BY _utf8mb4'hunter2'", "CREATE USER audited IDENTIFIED BY X'68756e74657232'"];
      for (const secret of secrets) {
        const refused = await run(secret);
        expect(refused.error).not.toBeNull();
      }
      const fresh = (await queryAudits()).filter((a) => a.id > last).sort((a, b) => a.id - b.id);
      expect(fresh).toHaveLength(3);
      expect(fresh[0].detail).toMatchObject({ statements: 'SELECT', outcome: 'ok', writes: false, database: e.db, rows: 1, errorCode: '' });
      const kind = e.engine === 'postgres' ? 'CREATE ROLE' : 'CREATE USER';
      for (const row of fresh.slice(1)) {
        expect(row.detail).toMatchObject({ statements: kind, outcome: 'error', errorStatement: 1 });
        expect(row.detail.errorCode).toMatch(/^\w+$/);
      }
      const text = JSON.stringify(fresh);
      for (const leak of ['audit-marker', 'hunter2', '68756e', 'audited']) {
        expect(text).not.toContain(leak);
      }
    });

    if (e.engine === 'postgres') {
      test('the Query tab runs a clicked table and exports CSV', async ({ page }) => {
        await signIn(page);
        await page.goto(`/databases/${id}`);
        await page.getByRole('tab', { name: 'Query' }).click();
        await page.getByText('items', { exact: true }).click();
        await page.getByRole('button', { name: 'Run', exact: true }).click();
        await expect(page.getByRole('tab', { name: '1 · SELECT 2' })).toBeVisible();
        await expect(page.getByText('a', { exact: true }).first()).toBeVisible();
        const download = page.waitForEvent('download');
        await page.getByRole('button', { name: 'Export CSV' }).click();
        const file = await download;
        expect(file.suggestedFilename()).toMatch(/\.csv$/);
      });
    }
  });
}
