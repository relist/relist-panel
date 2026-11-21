import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Databases phase 3: a Backup destination (a MinIO bucket on relist-network)
// is added and tested in the browser, a Postgres Database's schedule copies a
// scheduled Backup to it, the object is checked with mc, and deleting the
// Backup deletes the object. The MinIO server and client images are the newest
// pullable releases (the upstream minio/minio and minio/mc images are no
// longer published): MinIO RELEASE.2025-07-23T15-54-02Z and mc
// RELEASE.2025-07-21T05-28-08Z, from the bitnamilegacy mirror.

const MINIO_IMAGE = 'bitnamilegacy/minio:2025.7.23-debian-12-r5';
const MC_IMAGE = 'bitnamilegacy/minio-client:2025.7.21-debian-12-r3';
const MINIO = 'e2e-minio';
const MINIO_USER = 'e2eminio';
const MINIO_PASSWORD = 'e2e-minio-secret-1234';
const BUCKET = 'relist-e2e';

function docker(...args: string[]): string {
  const env = sandbox();
  return execFileSync('docker', args, {
    encoding: 'utf8',
    env: { ...process.env, DOCKER_HOST: env.dockerHost, DOCKER_CONFIG: env.dockerConfig },
  }).trim();
}

// One-off mc on relist-network, with the alias "l" given by the environment.
function mc(...args: string[]): string {
  return docker(
    'run', '--rm', '--network', 'relist-network',
    '-e', `MC_HOST_l=http://${MINIO_USER}:${MINIO_PASSWORD}@${MINIO}:9000`,
    '--entrypoint', 'mc', MC_IMAGE, ...args,
  );
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

test('a scheduled backup is copied to MinIO, and deleting it deletes the object', async ({ page }) => {
  test.setTimeout(8 * 60_000);
  const panel = await Panel.signIn();
  const name = `e2e-dst-${runId()}`;
  const destName = `e2e-minio-${runId()}`;
  let databaseId = 0;

  docker('rm', '-f', MINIO);
  try {
    // 1. MinIO on relist-network, and its bucket.
    docker('run', '-d', '--name', MINIO, '--network', 'relist-network',
      '-e', `MINIO_ROOT_USER=${MINIO_USER}`, '-e', `MINIO_ROOT_PASSWORD=${MINIO_PASSWORD}`, MINIO_IMAGE);
    await expect
      .poll(() => { try { return mc('mb', '--ignore-existing', `l/${BUCKET}`); } catch { return ''; } }, { timeout: 90_000 })
      .toContain(BUCKET);

    // 2. Add the destination in the browser, and Test it.
    await signIn(page);
    await page.goto('/backup-destinations');
    await page.getByRole('button', { name: 'Add destination' }).first().click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Name').fill(destName);
    await dialog.getByLabel('Endpoint').fill(`http://${MINIO}:9000`);
    await expect(dialog.getByText('The secret and backups travel unencrypted.')).toBeVisible();
    await dialog.getByLabel('Bucket').fill(BUCKET);
    await dialog.getByLabel('Access key ID').fill(MINIO_USER);
    await dialog.getByLabel('Secret access key').fill(MINIO_PASSWORD);
    await dialog.getByLabel('Path-style addressing').click();
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect(page.getByText(destName)).toBeVisible();
    await page.getByRole('button', { name: `Test ${destName}` }).click();
    await expect(page.getByRole('row').filter({ hasText: destName }).getByText('OK')).toBeVisible({ timeout: 60_000 });
    const [dest] = (await panel.json<{ id: number; name: string }[]>('GET', '/api/backup-destinations')).filter((d) => d.name === destName);
    expect(JSON.stringify(await panel.json('GET', '/api/backup-destinations'))).not.toContain(MINIO_PASSWORD);

    // 3. A Postgres Database, scheduled for the next minute, copying to the destination.
    const created = await panel.json<{ id: number; slug: string }>('POST', '/api/databases', { name, engine: 'postgres', version: '18' });
    databaseId = created.id;
    await expect
      .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${databaseId}`)).status, { timeout: 240_000 })
      .toBe('running');
    const saved = await panel.raw('PUT', `/api/databases/${databaseId}/schedule`, {
      all: false, databases: ['postgres'], days: ['daily'], destinationId: dest.id, enabled: true,
      localKeepCount: 7, localKeepDays: null, minute: panelMinute(1), remoteKeepCount: 30, remoteKeepDays: null,
    });
    expect(saved.status).toBe(200);

    // 4. Within a tick, a scheduled Backup exists and was copied.
    type Row = { id: number; trigger: string; status: string; upload: { status: string; destination: string } | null };
    let copied: Row | undefined;
    await expect
      .poll(async () => {
        const rows = await panel.json<Row[]>('GET', `/api/databases/${databaseId}/backups`);
        copied = rows.find((r) => r.trigger === 'scheduled' && r.upload?.status === 'uploaded');
        return copied?.id ?? 0;
      }, { timeout: 150_000, intervals: [3_000] })
      .toBeGreaterThan(0);
    expect(copied?.upload?.destination).toBe(destName);

    // 5. The object is in the bucket under <slug>-<id>/postgres/.
    const listing = mc('ls', '--recursive', `l/${BUCKET}`);
    const key = listing.split('\n').map((l) => l.trim().split(/\s+/).pop() ?? '').find((k) => k.startsWith(`${created.slug}-${databaseId}/postgres/`));
    expect(key, listing).toBeTruthy();
    expect(mc('stat', `l/${BUCKET}/${key}`)).toContain('Size');

    // 6. Deleting the Backup deletes its copy.
    await panel.json('DELETE', `/api/databases/${databaseId}/backups/${copied?.id}`);
    expect(() => mc('stat', `l/${BUCKET}/${key}`)).toThrow();

    // 7. The destination is still used by the schedule, so it cannot be deleted.
    const refused = await panel.raw('DELETE', `/api/backup-destinations/${dest.id}`);
    expect(refused.status).toBe(409);
    expect(refused.body).toContain(name);
    await panel.json('DELETE', `/api/databases/${databaseId}`, { confirm: name, deleteBackups: true });
    databaseId = 0;
    await panel.json('DELETE', `/api/backup-destinations/${dest.id}`);
  } finally {
    if (databaseId) {
      await panel.raw('DELETE', `/api/databases/${databaseId}`, { confirm: name, deleteBackups: true });
    }
    for (const d of await panel.json<{ id: number; name: string }[]>('GET', '/api/backup-destinations')) {
      if (d.name === destName) await panel.raw('DELETE', `/api/backup-destinations/${d.id}?confirm=0`);
    }
    docker('rm', '-f', MINIO);
  }
});

// Databases phase 9b (ADR-0046): a destination with encryption on stores
// copies as age files. The copy decrypts with the passphrase outside the
// panel (typage, the age author's reference TypeScript implementation),
// restores through Restore from file with the passphrase, and a file whose
// scrypt work factor is above 2^15 (typage's default is 2^18) is refused.
// The passphrase itself never appears in output or assertions.
test('an encrypted copy opens with age and restores through the panel', async ({ page }) => {
  test.setTimeout(10 * 60_000);
  const age = await import('age-encryption');
  const { gunzipSync } = await import('node:zlib');
  const panel = await Panel.signIn();
  const name = `e2e-enc-${runId()}`;
  const destName = `e2e-vault-${runId()}`;
  const passphrase = `e2e-pass-${runId()}-0123456789`;
  const secretValue = `e2e-secret-row-${runId()}`;
  let databaseId = 0;

  docker('rm', '-f', MINIO);
  try {
    // 1. MinIO on relist-network, and its bucket.
    docker('run', '-d', '--name', MINIO, '--network', 'relist-network',
      '-e', `MINIO_ROOT_USER=${MINIO_USER}`, '-e', `MINIO_ROOT_PASSWORD=${MINIO_PASSWORD}`, MINIO_IMAGE);
    await expect
      .poll(() => { try { return mc('mb', '--ignore-existing', `l/${BUCKET}`); } catch { return ''; } }, { timeout: 90_000 })
      .toContain(BUCKET);

    // 2. An encrypting destination, added over the API.
    const dest = await panel.json<{ id: number; encrypt: boolean }>('POST', '/api/backup-destinations', {
      encrypt: true, name: destName, passphrase, secret: MINIO_PASSWORD,
      s3: { accessKeyId: MINIO_USER, bucket: BUCKET, endpoint: `http://${MINIO}:9000`, pathStyle: true, prefix: 'enc', region: '' },
      type: 's3',
    });
    expect(dest.encrypt).toBe(true);

    // 3. A Postgres Database with a seeded row, scheduled to copy to it.
    const created = await panel.json<{ id: number; slug: string }>('POST', '/api/databases', { name, engine: 'postgres', version: '18' });
    databaseId = created.id;
    await expect
      .poll(async () => (await panel.json<{ status: string }>('GET', `/api/databases/${databaseId}`)).status, { timeout: 240_000 })
      .toBe('running');
    const seeded = await panel.json<{ error: unknown }>('POST', `/api/databases/${databaseId}/query`, {
      database: 'postgres', preview: false, sql: `CREATE TABLE e2e_vault (v text); INSERT INTO e2e_vault VALUES ('${secretValue}');`, writes: true,
    });
    expect(seeded.error).toBeNull();
    const saved = await panel.raw('PUT', `/api/databases/${databaseId}/schedule`, {
      all: false, databases: ['postgres'], days: ['daily'], destinationId: dest.id, enabled: true,
      localKeepCount: 7, minute: panelMinute(1), remoteKeepCount: 30,
    });
    expect(saved.status).toBe(200);

    // 4. The copy arrives marked encrypted, under a .age key.
    type Row = { id: number; trigger: string; upload: { status: string; encrypted: boolean } | null };
    let copied: Row | undefined;
    await expect
      .poll(async () => {
        const rows = await panel.json<Row[]>('GET', `/api/databases/${databaseId}/backups`);
        copied = rows.find((r) => r.trigger === 'scheduled' && r.upload?.status === 'uploaded');
        return copied?.id ?? 0;
      }, { timeout: 150_000, intervals: [3_000] })
      .toBeGreaterThan(0);
    expect(copied?.upload?.encrypted).toBe(true);
    const listing = mc('ls', '--recursive', `l/${BUCKET}`);
    const key = listing.split('\n').map((l) => l.trim().split(/\s+/).pop() ?? '').find((k) => k.startsWith(`enc/${created.slug}-${databaseId}/postgres/`));
    expect(key, listing).toBeTruthy();
    expect(key?.endsWith('.age'), key).toBe(true);

    // 5. The object decrypts outside the panel with the passphrase: age interop.
    const b64 = docker(
      'run', '--rm', '--network', 'relist-network',
      '-e', `MC_HOST_l=http://${MINIO_USER}:${MINIO_PASSWORD}@${MINIO}:9000`,
      '--entrypoint', '/bin/sh', MC_IMAGE, '-c', `mc cat 'l/${BUCKET}/${key}' | base64 -w0`,
    );
    const encBytes = Buffer.from(b64, 'base64');
    const decrypter = new age.Decrypter();
    decrypter.addPassphrase(passphrase);
    const plain = await decrypter.decrypt(new Uint8Array(encBytes), 'uint8array');
    const dump = gunzipSync(Buffer.from(plain)).toString('utf8');
    expect(dump).toContain(secretValue);

    // 6. The .age copy restores through Restore from file with the passphrase.
    await signIn(page);
    await page.goto(`/databases/${databaseId}`);
    await page.getByRole('tab', { name: 'Backups' }).click();
    await page.locator('input[type=file]').setInputFiles({ buffer: encBytes, mimeType: 'application/octet-stream', name: 'copy.sql.gz.age' });
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Passphrase').fill(passphrase);
    await dialog.getByLabel('Restore into a new database').click();
    await dialog.getByLabel('New database name').fill('restored');
    await dialog.getByLabel(`Type ${name} to confirm`).fill(name);
    await dialog.getByRole('button', { name: 'Restore' }).click();
    await expect
      .poll(async () => {
        const restores = await panel.json<{ status: string }[]>('GET', `/api/databases/${databaseId}/restores`);
        return restores[0]?.status ?? '';
      }, { timeout: 120_000 })
      .toBe('succeeded');
    const back = await panel.json<{ results: { rows: string[][] }[]; error: unknown }>('POST', `/api/databases/${databaseId}/query`, {
      database: 'restored', preview: false, sql: 'SELECT v FROM e2e_vault', writes: false,
    });
    expect(back.error).toBeNull();
    expect(JSON.stringify(back.results[0]?.rows)).toContain(secretValue);

    // 7. A file at typage's default work factor 2^18 is refused before any
    // derivation, telling the admin to decrypt it with age first.
    const encrypter = new age.Encrypter();
    encrypter.setPassphrase(passphrase);
    const expensive = await encrypter.encrypt(new TextEncoder().encode('SELECT 1;\n'));
    await page.locator('input[type=file]').setInputFiles({ buffer: Buffer.from(expensive), mimeType: 'application/octet-stream', name: 'expensive.sql.age' });
    await dialog.getByLabel('Passphrase').fill(passphrase);
    await dialog.getByLabel('Restore into a new database').click();
    await dialog.getByLabel('New database name').fill('refused');
    await dialog.getByLabel(`Type ${name} to confirm`).fill(name);
    await dialog.getByRole('button', { name: 'Restore' }).click();
    await expect(page.getByText(/decrypt it with `age -d` first/)).toBeVisible({ timeout: 30_000 });
  } finally {
    if (databaseId) {
      await panel.raw('DELETE', `/api/databases/${databaseId}`, { confirm: name, deleteBackups: true });
    }
    for (const d of await panel.json<{ id: number; name: string }[]>('GET', '/api/backup-destinations')) {
      if (d.name === destName) await panel.raw('DELETE', `/api/backup-destinations/${d.id}?confirm=0`);
    }
    docker('rm', '-f', MINIO);
  }
});
