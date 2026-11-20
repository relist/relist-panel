import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 4: the Redis console and key browser, through the panel URL
// (Traefik), on Redis 8 and 7.4. Each version gets a fresh Database and
// deletes it afterwards.

type Reply = { type: string; value: any; bytes?: number; more?: number };
type Response = { reply: Reply | null; refused?: string; needsConfirm?: boolean; error?: string; durationMs: number };
type KeyValue = { type: string; total: number; more: boolean; items: { field?: string; value: string; score?: string }[] };

function docker(...args: string[]): string {
  const env = sandbox();
  return execFileSync('docker', args, {
    encoding: 'utf8',
    env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig },
  }).trim();
}

/** redis-cli inside the Database's own container: the check that does not go through the panel. */
function redisCli(container: string, ...args: string[]): string {
  return docker('exec', container, 'sh', '-c', `redis-cli -a "$REDIS_PASSWORD" --no-auth-warning ${args.map((a) => `'${a}'`).join(' ')}`);
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

for (const version of ['8', '7.4']) {
  test.describe(`console on redis ${version}`, () => {
    test.describe.configure({ mode: 'serial', timeout: 5 * 60_000 });
    let panel: Panel;
    let id = 0;
    let name = '';
    let container = '';

    const run = (command: string, opts: { db?: number; writes?: boolean; confirm?: boolean } = {}): Promise<Response> =>
      panel.json<Response>('POST', `/api/databases/${id}/redis/command`, {
        db: opts.db ?? 0,
        command,
        writes: opts.writes ?? false,
        confirmDangerous: opts.confirm ?? false,
      });
    const audits = async () =>
      (
        await panel.json<{ items: { id: number; action: string; detail: Record<string, any>; target: string }[] }>(
          'GET',
          '/api/audit?action=database.redis_command',
        )
      ).items;

    test.beforeAll(async () => {
      panel = await Panel.signIn();
      name = `e2e-rc-${version.replace('.', '')}-${runId()}`;
      ({ id } = await panel.json<{ id: number }>('POST', '/api/databases', { name, engine: 'redis', version }));
      container = `relist-db-${id}`;
      await expect
        .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 240_000 })
        .toBe('running');
    });

    test.afterAll(async () => {
      if (id) await panel.raw('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
    });

    test('read-only refuses SET and CONFIG SET; scripts never run', async () => {
      const refused = [
        'SET x 1',
        'CONFIG SET maxmemory 1mb',
        'FLUSHALL',
        'DEL x',
      ];
      for (const line of refused) {
        const r = await run(line);
        expect(r.refused, line).toBe('writes are off');
        expect(r.reply).toBeNull();
      }
      // A script is a built-up command, so it would get around the
      // classifier: never in the console, writes or not.
      const scripts = [
        `EVAL "return redis.call('set','x','1')" 0`,
        `EVAL_RO "return 1" 0`,
        'FCALL myfunc 0',
        'SCRIPT LOAD "return 1"',
      ];
      for (const line of scripts) {
        const r = await run(line, { writes: true });
        expect(r.refused, line).toContain('not available in the console');
        expect(r.reply).toBeNull();
      }
      expect(redisCli(container, 'DBSIZE')).toBe('0');
      expect(redisCli(container, 'CONFIG', 'GET', 'maxmemory')).not.toContain('1048576');

      const get = await run('GET x');
      expect(get.reply).toMatchObject({ type: 'nil' });
      expect((await run('PING')).reply).toMatchObject({ type: 'string', value: 'PONG' });
      expect((await run('CONFIG GET maxmemory')).reply?.type).toBe('array');
    });

    test('commands that never run in the console', async () => {
      for (const line of ['SUBSCRIBE c', 'MONITOR', 'MULTI', 'SELECT 1', 'AUTH x', 'CLIENT REPLY OFF', 'HELLO 3']) {
        const r = await run(line, { writes: true });
        expect(r.refused, line).toContain('not available in the console');
      }
      expect((await run('NOSUCHCOMMAND', { writes: true })).refused).toBe('unknown command');
    });

    test('writes allowed: SET then GET', async () => {
      expect((await run('SET k v', { writes: true })).reply).toMatchObject({ type: 'string', value: 'OK' });
      expect((await run('GET k')).reply).toMatchObject({ type: 'bulk', value: 'v' });
      expect(redisCli(container, 'GET', 'k')).toBe('v');
      // The selector picks the logical database.
      expect((await run('SET other 1', { db: 3, writes: true })).reply?.value).toBe('OK');
      expect((await run('GET other')).reply?.type).toBe('nil');
      expect((await run('GET other', { db: 3 })).reply?.value).toBe('1');
    });

    test('FLUSHALL waits for the second confirmation', async () => {
      const first = await run('FLUSHALL', { writes: true });
      expect(first.needsConfirm).toBe(true);
      expect(first.reply).toBeNull();
      expect(redisCli(container, 'EXISTS', 'k')).toBe('1');
      const done = await run('FLUSHALL', { writes: true, confirm: true });
      expect(done.reply?.value).toBe('OK');
      expect(redisCli(container, 'DBSIZE')).toBe('0');
    });

    test('a 100 KB value comes back cut with its true size', async () => {
      await run(`SET big ${'x'.repeat(100000)}`, { writes: true });
      const r = await run('GET big');
      expect(r.reply?.type).toBe('bulk');
      expect(r.reply?.bytes).toBe(100000);
      expect(String(r.reply?.value)).toHaveLength(65536);
      // And a collection of 1,500 is cut at 1,000 with the rest counted.
      redisCli(container, 'EVAL', 'for i=1,1500 do redis.call("rpush","long",i) end', '0');
      const all = await run('LRANGE long 0 -1');
      expect(all.reply?.value).toHaveLength(1000);
      expect(all.reply?.more).toBe(500);
    });

    test('the key browser reads each type up to 1,000 elements', async () => {
      redisCli(container, 'EVAL', 'for i=1,1500 do redis.call("hset","hh",i,i); redis.call("sadd","ss",i); redis.call("zadd","zz",i,i) end', '0');
      for (const [key, type, field] of [
        ['hh', 'hash', true],
        ['long', 'list', false],
        ['ss', 'set', false],
        ['zz', 'zset', false],
      ] as const) {
        const kv = await panel.json<KeyValue>('GET', `/api/databases/${id}/redis/key?db=0&key=${key}`);
        expect(kv.type, key).toBe(type);
        expect(kv.total, key).toBe(1500);
        expect(kv.more, key).toBe(true);
        expect(kv.items.length, key).toBeGreaterThanOrEqual(900);
        expect(kv.items.length, key).toBeLessThanOrEqual(1100);
        if (field) expect(kv.items[0].field).toBeTruthy();
        if (type === 'zset') expect(kv.items[0].score).toBeTruthy();
      }
      const scan = await panel.json<{ keys: { key: string; type: string; ttlMs: number }[]; cursor: string }>(
        'GET',
        `/api/databases/${id}/redis/keys?db=0&pattern=h*&cursor=0`,
      );
      expect(scan.keys).toEqual([{ key: 'hh', type: 'hash', ttlMs: -1 }]);
      const unsupported = await run('XADD st * a 1', { writes: true });
      expect(unsupported.reply?.type).toBe('bulk');
      const st = await panel.json<KeyValue & { unsupported?: boolean }>('GET', `/api/databases/${id}/redis/key?db=0&key=st`);
      expect(st).toMatchObject({ type: 'stream', unsupported: true });
    });

    test('every run is audited, refused ones too, and a password never is', async () => {
      const last = Math.max(0, ...(await audits()).map((a) => a.id));
      await run('SET audited 1');
      await run('GET audited');
      const { password } = await panel.json<{ password: string }>('GET', `/api/databases/${id}/credentials`);
      // The same password again: the line is recorded, nothing changes.
      const set = await run(`CONFIG SET requirepass ${password}`, { writes: true, confirm: true });
      expect(set.reply?.value).toBe('OK');
      await run('FLUSHALL', { writes: true });
      const fresh = (await audits()).filter((a) => a.id > last).reverse();
      expect(fresh.map((a) => [a.detail.command, a.detail.outcome])).toEqual([
        ['SET', 'refused'],
        ['GET', 'ok'],
        ['CONFIG SET', 'ok'],
        ['FLUSHALL', 'refused'],
      ]);
      const text = JSON.stringify(fresh);
      expect(text).not.toContain(password);
      expect(text).not.toContain('audited');
      expect(fresh.every((a) => a.target === name && a.detail.database === 0)).toBe(true);
      expect(fresh[2].detail.writes).toBe(true);
    });

    if (version === '8') {
      test('the Console tab runs a command, scans keys and opens a hash', async ({ page }) => {
        await run('FLUSHALL', { writes: true, confirm: true });
        await run('SET k v', { writes: true });
        await run('HSET user:1 name Ada', { writes: true });
        await signIn(page);
        await page.goto(`/databases/${id}`);
        await page.getByRole('tab', { name: 'Console' }).click();
        await page.getByLabel('Command').fill('GET k');
        await page.getByLabel('Command').press('Enter');
        await expect(page.getByText('"v"', { exact: true })).toBeVisible();
        await page.getByRole('button', { name: 'Scan' }).click();
        await page.getByText('user:1', { exact: true }).click();
        await expect(page.getByText('Ada', { exact: true })).toBeVisible();
        await expect(page.getByText('name', { exact: true })).toBeVisible();
        // A dangerous command asks first, and says so.
        await page.getByRole('switch', { name: /allow writes/i }).click();
        await page.getByLabel('Command').fill('FLUSHALL');
        await page.getByLabel('Command').press('Enter');
        await expect(page.getByText("FLUSHALL can't be undone. Run it?")).toBeVisible();
        expect(redisCli(container, 'DBSIZE')).toBe('2');
        await page.getByRole('button', { name: 'Cancel' }).click();
        expect(redisCli(container, 'DBSIZE')).toBe('2');
      });
    }
  });
}
