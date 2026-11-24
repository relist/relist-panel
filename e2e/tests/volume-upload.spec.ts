import { createHash, randomBytes } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { expect, test, type Page } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Chunked Volume uploads end to end (.scratch/chunked-upload/spec.md,
// ADR-0053): a file larger than one 32 MiB chunk is sent in pieces, written
// into the Volume by the background job, and an upload cut off part-way
// resumes from the panel's offset after a reload. The files are real files on
// disk: a picked buffer would get a new lastModified each time, and a resume
// is matched on it.

test.describe.configure({ mode: 'serial' });

const MiB = 1 << 20;
const id = runId();
let panel: Panel;
let composePath = '';
let pagePath = '';
let source = '';
let dir = '';

/** An ASCII file of `bytes` bytes, so the read-back compares as text. */
function writeFile(name: string, bytes: number): { path: string; sha: string } {
  const content = randomBytes(bytes / 2).toString('hex');
  const path = join(dir, name);
  writeFileSync(path, content);
  return { path, sha: createHash('sha256').update(content).digest('hex') };
}

async function readBackSha(name: string): Promise<string> {
  const q = new URLSearchParams({ source, path: name, service: 'app' });
  const reply = await panel.raw('GET', `${composePath}/volumes/read?${q}`);
  expect(reply.status).toBe(200);
  return createHash('sha256').update(reply.body).digest('hex');
}

async function signIn(page: Page) {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
}

/** Opens the Explorer on the Volume and confirms the Service's user. */
async function openExplorer(page: Page) {
  await page.goto(pagePath);
  await page.getByRole('combobox').click();
  // antd's role=option nodes are hidden a11y mirrors; click the visible item.
  await page.locator('.ant-select-item-option', { hasText: 'app — 1000:1000' }).click();
  await page.getByRole('button', { name: 'Confirm' }).click();
  await expect(page.getByRole('button', { name: /Upload/ }).first()).toBeEnabled();
}

function fileInput(page: Page) {
  return page.locator('input[type="file"]:not([accept])');
}

test.beforeAll(async () => {
  dir = mkdtempSync(join(tmpdir(), 'relist-upload-'));
  panel = await Panel.signIn();
  const project = await panel.json('POST', '/api/projects', { name: `e2e-${id}`, description: 'volume-upload.spec' });
  const envs = await panel.json<{ id: number }[]>('GET', `/api/projects/${project.id}/environments`);
  const env = envs[0] ?? (await panel.json('POST', `/api/projects/${project.id}/environments`, { name: 'production' }));
  const base = `/api/projects/${project.id}/environments/${env.id}/composes`;
  const compose = await panel.json('POST', base, {
    name: 'files',
    yamlContent:
      // A fresh named volume's root is root's; `own` hands it to the app's
      // uid once, so the operation user can write into it.
      'services:\n  own:\n    image: alpine:3.20\n    command: ["chown", "1000:1000", "/data"]\n    volumes:\n      - data:/data\n  app:\n    image: alpine:3.20\n    command: ["sleep", "infinity"]\n    user: "1000:1000"\n    depends_on:\n      own:\n        condition: service_completed_successfully\n    volumes:\n      - data:/data\nvolumes:\n  data: {}\n',
  });
  composePath = `${base}/${compose.id}`;
  await panel.deploy(composePath);
  const volumes = await panel.json<{ volumes: { source: string }[] }>('GET', `${composePath}/volumes`);
  source = volumes.volumes[0].source;
  pagePath = `/projects/${project.id}/env/${env.id}/compose/${compose.id}/volumes/explorer?source=${encodeURIComponent(source)}&path=`;
});

test.afterAll(() => {
  if (dir) rmSync(dir, { recursive: true, force: true });
});

test('uploads 80 MiB in three chunks', async ({ page }) => {
  const file = writeFile('eighty.bin', 80 * MiB);
  const patches: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'PATCH' && r.url().includes('/volumes/uploads/')) patches.push(r.headers()['upload-offset'] ?? '');
  });
  await signIn(page);
  await openExplorer(page);

  await fileInput(page).setInputFiles(file.path);
  await expect(page.getByText('Uploaded eighty.bin')).toBeVisible({ timeout: 120_000 });

  expect(patches).toEqual(['0', String(32 * MiB), String(64 * MiB)]);
  expect(await readBackSha('eighty.bin')).toBe(file.sha);
});

test('resumes after an interruption', async ({ page }) => {
  const file = writeFile('resume.bin', 80 * MiB);
  await signIn(page);
  await openExplorer(page);

  // Let the first chunk through, then cut every later one off, as a closed tab
  // would; reload at once rather than wait out the client's backoff.
  let seen = 0;
  let cut!: () => void;
  const cutOff = new Promise<void>((resolve) => (cut = resolve));
  await page.route('**/volumes/uploads/*', async (route) => {
    if (route.request().method() !== 'PATCH') return route.continue();
    seen += 1;
    if (seen === 1) return route.continue();
    cut();
    return route.abort();
  });
  await fileInput(page).setInputFiles(file.path);
  await cutOff;
  await page.unrouteAll({ behavior: 'ignoreErrors' });

  await page.reload();
  await openExplorer(page);
  await expect(page.getByText('Unfinished uploads')).toBeVisible();

  const resumed: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'PATCH' && r.url().includes('/volumes/uploads/')) resumed.push(r.headers()['upload-offset'] ?? '');
  });
  await fileInput(page).setInputFiles(file.path);
  await page.getByRole('button', { name: 'Resume', exact: true }).click();
  await expect(page.getByText('Uploaded resume.bin')).toBeVisible({ timeout: 120_000 });

  expect(resumed[0]).toBe(String(32 * MiB));
  expect(await readBackSha('resume.bin')).toBe(file.sha);
});
