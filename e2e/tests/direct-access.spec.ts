import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 5: a Direct access publishes a Database on a host port of
// the sandbox's Docker host. Clients run as `docker run --network host`, so
// 127.0.0.1 is that host. Real expiries are not waited out here: the deadline
// is covered by the relist-proxy unit tests.

const execFileAsync = promisify(execFile);

function dockerEnv() {
  const env = sandbox();
  return { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig };
}

function docker(...args: string[]): string {
  return execFileSync('docker', args, { encoding: 'utf8', env: dockerEnv() }).trim();
}

const PSQL = 'postgres:18';
const REDIS = 'redis:8';

function psqlArgs(port: number, password: string, sql: string): string[] {
  return ['run', '--rm', '--network', 'host', PSQL, 'psql', `postgresql://postgres:${password}@127.0.0.1:${port}/postgres`, '-At', '-c', sql];
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
    .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${id}`)).status, { timeout: 240_000 })
    .toBe('running');
  return id;
}

async function password(panel: Panel, id: number): Promise<string> {
  return (await panel.json<{ password: string }>('GET', `/api/databases/${id}/credentials`)).password;
}

// This run's proxy only: a failed earlier run may have left another one.
function accessContainers(accessId: number): string {
  return docker('ps', '-a', '--filter', `name=^relist-access-${accessId}$`, '--format', '{{.Names}}');
}

test.describe('direct access', () => {
  test.describe.configure({ mode: 'serial', timeout: 8 * 60_000 });

  test('opens on Postgres from the card, survives an Extend, closes', async ({ page }) => {
    const name = `e2e-da-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'postgres', '18');
    const pw = await password(panel, id);
    // Make sure the client image is there before anything is timed.
    docker('pull', '-q', PSQL);

    await signIn(page);
    await page.goto(`/databases/${id}`);
    const card = page.locator('.ant-card', { has: page.getByText('Direct access', { exact: true }) });
    await expect(card.getByRole('button', { name: 'Open', exact: true })).toBeEnabled();

    // 1. Open for 15 minutes, port picked by Docker, and read it from the card.
    await card.getByRole('combobox', { name: 'Duration' }).click();
    await page.getByTitle('15 minutes').click();
    await card.getByRole('button', { name: 'Open', exact: true }).click();
    const portCell = card.locator('.ant-descriptions-row', { hasText: 'Host port' }).locator('.ant-descriptions-item-content');
    await expect(portCell).toBeVisible();
    const port = Number.parseInt((await portCell.innerText()).trim(), 10);
    expect(port).toBeGreaterThan(1023);
    await expect(card.getByText(`ssh -N -L ${port}:127.0.0.1:${port} <user>@`)).toBeVisible();
    await expect(card.getByText(`postgres://postgres@127.0.0.1:${port}/postgres`)).toBeVisible();
    await expect(card.getByText(pw)).toHaveCount(0);

    const [{ id: accessId }] = (await panel.json<{ accesses: { id: number }[] }>('GET', `/api/direct-accesses?targetKind=database&targetId=${id}`)).accesses;
    expect(accessContainers(accessId)).toBe(`relist-access-${accessId}`);

    // 2. A real client gets through the proxy.
    expect(docker(...psqlArgs(port, pw, 'select 1'))).toBe('1');

    // 3. A held session survives an Extend.
    const held = execFileAsync('docker', psqlArgs(port, pw, 'select pg_sleep(8)'), { env: dockerEnv() });
    await new Promise((resolve) => setTimeout(resolve, 2000));
    await card.getByRole('button', { name: 'Extend', exact: true }).click();
    await expect
      .poll(async () => (await panel.json<{ items: { target: string }[] }>('GET', '/api/audit?action=direct_access.extend')).items.filter((i) => i.target === name).length)
      .toBe(1);
    await held; // rejects (non-zero exit) if the Extend cut it
    expect(docker(...psqlArgs(port, pw, 'select 2'))).toBe('2');

    // 4. A second open on the same Database is refused.
    const again = await panel.raw('POST', '/api/direct-accesses', {
      targetKind: 'database', targetId: id, channel: 'host_port', allInterfaces: false, hostPort: 0, minutes: 15,
    });
    expect(again.status).toBe(409);

    // 5. Close now: the port refuses and no proxy container is left.
    await card.getByRole('button', { name: 'Close now' }).click();
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(card.getByRole('button', { name: 'Open', exact: true })).toBeVisible();
    expect(() => docker(...psqlArgs(port, pw, 'select 1'))).toThrow();
    expect(accessContainers(accessId)).toBe('');

    // 6. Open, extend and close are in the audit log.
    for (const action of ['direct_access.open', 'direct_access.extend', 'direct_access.close']) {
      const { items } = await panel.json<{ items: { target: string; detail: Record<string, unknown> }[] }>('GET', `/api/audit?action=${action}`);
      const mine = items.filter((item) => item.target === name);
      expect(mine.length, action).toBeGreaterThan(0);
      expect(JSON.stringify(mine[0].detail)).not.toContain(pw);
    }

    await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
  });

  test('reaches Redis, and deleting the Database removes its proxy', async () => {
    const name = `e2e-da-redis-${runId()}`;
    const panel = await Panel.signIn();
    const id = await createRunning(panel, name, 'redis', '8');
    const pw = await password(panel, id);
    docker('pull', '-q', REDIS);

    const opened = await panel.json<{ id: number; hostPort: number; bind: string }>('POST', '/api/direct-accesses', {
      targetKind: 'database', targetId: id, channel: 'host_port', allInterfaces: false, hostPort: 0, minutes: 15,
    });
    expect(opened.bind).toBe('127.0.0.1');
    const out = docker('run', '--rm', '--network', 'host', REDIS, 'redis-cli', '-h', '127.0.0.1', '-p', String(opened.hostPort), '-a', pw, '--no-auth-warning', 'ping');
    expect(out).toBe('PONG');
    expect(accessContainers(opened.id)).toBe(`relist-access-${opened.id}`);

    await panel.json('DELETE', `/api/databases/${id}`, { confirm: name, deleteBackups: true });
    expect(accessContainers(opened.id)).toBe('');
    const { accesses } = await panel.json<{ accesses: unknown[] }>('GET', `/api/direct-accesses?targetKind=database&targetId=${id}`);
    expect(accesses).toHaveLength(0);
  });
});
