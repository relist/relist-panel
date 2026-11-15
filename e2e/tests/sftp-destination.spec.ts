import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 6a: an SFTP Backup destination (OpenSSH on relist-network).
// It is added and tested in the browser with a library key, a schedule copies
// a Backup to it, the key cannot be deleted while it is used, deleting the
// Backup removes the file, a replaced server (new host keys) fails Test until
// Reset host key, and a password destination passes Test.

const SFTP_IMAGE = 'lscr.io/linuxserver/openssh-server:10.3_p1-r2-ls243';
const USER = 'e2e';

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

/** The minute of the day, in the panel container's time zone, `ahead` minutes from now. */
function panelMinute(ahead: number): number {
  const [h, m] = docker('exec', 'relist-panel', 'date', '+%H %M').split(' ').map(Number);
  return (h * 60 + m + ahead) % 1440;
}

/** The SHA256 fingerprints of the server's host keys, one per key type. */
function serverFingerprints(container: string): string[] {
  return ['ed25519', 'ecdsa', 'rsa'].map((t) => {
    const out = docker('exec', container, 'ssh-keygen', '-lf', `/config/ssh_host_keys/ssh_host_${t}_key.pub`);
    return out.split(/\s+/)[1];
  });
}

type Dest = { id: number; name: string; lastTestOk: boolean; lastTestError: string; sftp: { hostKey: string } };

test('a scheduled backup is copied to an SFTP server, with a host key trusted on first Test', async ({ page }) => {
  test.setTimeout(10 * 60_000);
  const panel = await Panel.signIn();
  const id = runId();
  const server = `e2e-sftp-${id}`;
  const dbName = `e2e-sftp-db-${id}`;
  const keyName = `e2e-sftp-key-${id}`;
  const keyDest = `e2e-sftp-k-${id}`;
  const pwDest = `e2e-sftp-p-${id}`;
  const password = `pw-${id}-e2e-secret`;
  let databaseId = 0;
  let keyId = 0;

  const start = (publicKey: string) => {
    docker('rm', '-f', server);
    docker('run', '-d', '--name', server, '--network', 'relist-network',
      '-e', `USER_NAME=${USER}`, '-e', `PUBLIC_KEY=${publicKey}`, '-e', 'PASSWORD_ACCESS=true', '-e', `USER_PASSWORD=${password}`,
      SFTP_IMAGE);
  };
  const waitSshd = async () => {
    await expect
      .poll(() => { try { return docker('exec', server, 'pgrep', 'sshd') !== '' && docker('exec', server, 'ls', '/config/ssh_host_keys/ssh_host_rsa_key.pub') !== ''; } catch { return false; } },
        { timeout: 120_000, intervals: [2_000] })
      .toBe(true);
  };
  const list = async () => (await panel.json<Dest[]>('GET', '/api/backup-destinations'));
  const testDest = async (destId: number) => panel.json<Dest>('POST', `/api/backup-destinations/${destId}/test`);

  try {
    // Setup: a library key, and an OpenSSH server that accepts it.
    const key = await panel.json<{ id: number; publicKey: string }>('POST', '/api/ssh-keys', { name: keyName });
    keyId = key.id;
    start(key.publicKey);
    await waitSshd();

    // 1. Add the destination in the browser, Test it, and compare the fingerprint.
    await signIn(page);
    await page.goto('/backup-destinations');
    await page.getByRole('button', { name: 'Add destination' }).first().click();
    let dialog = page.getByRole('dialog');
    await dialog.getByText('SFTP', { exact: true }).click();
    await dialog.getByLabel('Name').fill(keyDest);
    await dialog.getByLabel('Host').fill(server);
    await dialog.getByLabel('Port').fill('2222');
    await dialog.getByLabel('User').fill(USER);
    await dialog.getByLabel('Base path').fill('backups');
    await dialog.getByRole('combobox', { name: 'SSH key' }).click();
    await page.getByTitle(keyName).click();
    await expect(dialog.getByText(key.publicKey)).toBeVisible();
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText(keyDest)).toBeVisible();
    // Edit straight after Save, while the Add dialog may still be closing,
    // opens with this destination's values, not the Add form's.
    await page.getByRole('button', { name: `Edit ${keyDest}` }).click();
    await expect(page.getByRole('dialog').getByLabel('Name')).toHaveValue(keyDest);
    await expect(page.getByRole('dialog').getByLabel('Host')).toHaveValue(server);
    await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
    await page.getByRole('button', { name: `Test ${keyDest}` }).click();
    await expect(page.getByRole('row').filter({ hasText: keyDest }).getByText('OK')).toBeVisible({ timeout: 60_000 });
    await page.getByRole('button', { name: `Edit ${keyDest}` }).click();
    dialog = page.getByRole('dialog');
    const shown = (await dialog.locator('code', { hasText: 'SHA256:' }).first().innerText()).trim();
    expect(shown.split(' ')[0]).toMatch(/^(ssh-ed25519|ecdsa-sha2-nistp256|ssh-rsa)$/);
    expect(serverFingerprints(server)).toContain(shown.split(' ')[1]);
    await dialog.getByRole('button', { name: 'Cancel' }).click();
    const dest = (await list()).find((d) => d.name === keyDest)!;
    expect(JSON.stringify(await list())).not.toContain('PRIVATE KEY');

    // 2. A schedule copies a Backup to it.
    const created = await panel.json<{ id: number; slug: string }>('POST', '/api/databases', { name: dbName, engine: 'postgres', version: '18' });
    databaseId = created.id;
    await expect
      .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${databaseId}`)).status, { timeout: 240_000 })
      .toBe('running');
    const saved = await panel.raw('PUT', `/api/databases/${databaseId}/schedule`, {
      all: false, databases: ['postgres'], days: ['daily'], destinationId: dest.id, enabled: true,
      localKeepCount: 7, localKeepDays: null, minute: panelMinute(1), remoteKeepCount: 30, remoteKeepDays: null,
    });
    expect(saved.status).toBe(200);
    type Row = { id: number; trigger: string; size: number; upload: { status: string; destination: string } | null };
    let copied: Row | undefined;
    await expect
      .poll(async () => {
        const rows = await panel.json<Row[]>('GET', `/api/databases/${databaseId}/backups`);
        copied = rows.find((r) => r.trigger === 'scheduled' && r.upload?.status === 'uploaded');
        return copied?.id ?? 0;
      }, { timeout: 150_000, intervals: [3_000] })
      .toBeGreaterThan(0);
    const files = docker('exec', server, 'find', '/config/backups', '-type', 'f').split('\n').filter(Boolean);
    expect(files).toHaveLength(1);
    expect(files[0]).toMatch(new RegExp(`^/config/backups/${created.slug}-${databaseId}/postgres/[^/]+$`));
    expect(files[0]).not.toContain('.part');
    expect(Number(docker('exec', server, 'stat', '-c', '%s', files[0]))).toBe(copied!.size);

    // 3. The key cannot be deleted while the destination uses it.
    const refused = await panel.raw('DELETE', `/api/ssh-keys/${keyId}`);
    expect(refused.status).toBe(409);
    expect(refused.body).toContain(keyDest);

    // 4. Deleting the Backup removes the file (before the server is replaced).
    await panel.json('DELETE', `/api/databases/${databaseId}/backups/${copied!.id}`);
    expect(docker('exec', server, 'find', '/config/backups', '-type', 'f', '-path', `*${created.slug}-${databaseId}/*`)).toBe('');

    // 5. A replaced server presents new host keys: Test fails until Reset host key.
    const before = shown;
    start(key.publicKey);
    await waitSshd();
    const changed = await testDest(dest.id);
    expect(changed.lastTestOk).toBe(false);
    expect(changed.lastTestError).toContain('host key changed');
    const reset = await panel.json<Dest>('POST', `/api/backup-destinations/${dest.id}/reset-host-key`);
    expect(reset.sftp.hostKey).toBe('');
    const again = await testDest(dest.id);
    expect(again.lastTestOk, again.lastTestError).toBe(true);
    expect(again.sftp.hostKey).not.toBe('');
    expect(serverFingerprints(server)).toContain(again.sftp.hostKey.split(' ')[1]);
    expect(again.sftp.hostKey.split(' ')[1]).not.toBe(before.split(' ')[1]);

    // 6. Password auth.
    const pw = await panel.json<Dest>('POST', '/api/backup-destinations', {
      name: pwDest, type: 'sftp', secret: password,
      sftp: { host: server, port: 2222, user: USER, path: 'pw', auth: 'password', sshKeyId: null },
    });
    const tested = await testDest(pw.id);
    expect(tested.lastTestOk, tested.lastTestError).toBe(true);
    expect(JSON.stringify(await list())).not.toContain(password);
  } finally {
    if (databaseId) await panel.raw('DELETE', `/api/databases/${databaseId}`, { confirm: dbName, deleteBackups: true });
    for (const d of await list()) {
      if (d.name === keyDest || d.name === pwDest) await panel.raw('DELETE', `/api/backup-destinations/${d.id}?confirm=0`);
    }
    if (keyId) await panel.raw('DELETE', `/api/ssh-keys/${keyId}`);
    docker('rm', '-f', server);
  }
});
