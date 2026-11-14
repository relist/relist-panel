import { expect, test } from '@playwright/test';
import { Panel, runId, sandbox } from '../lib/sandbox.js';

// Monaco loads on demand (web/src/components/MonacoGate.tsx): pages without an
// editor never download it, and the first editor still gets the bundled copy
// with its YAML worker and the Compose schema, not a CDN copy.

const id = runId();
let editorPath = '';

test.beforeAll(async () => {
  const panel = await Panel.signIn();
  const project = await panel.json('POST', '/api/projects', { name: `e2e-${id}`, description: 'editor.spec' });
  const envs = await panel.json<{ id: number }[]>('GET', `/api/projects/${project.id}/environments`);
  const env = envs[0] ?? (await panel.json('POST', `/api/projects/${project.id}/environments`, { name: 'production' }));
  const compose = await panel.json('POST', `/api/projects/${project.id}/environments/${env.id}/composes`, {
    name: 'web',
    yamlContent: 'services:\n  whoami:\n    image: traefik/whoami\n',
  });
  editorPath = `/projects/${project.id}/env/${env.id}/compose/${compose.id}/editor`;
});

test('Monaco loads only when an editor opens, with its YAML worker', async ({ page }) => {
  const scripts: string[] = [];
  const foreign: string[] = [];
  page.on('request', (r) => {
    const { pathname } = new URL(r.url());
    if (pathname.endsWith('.js')) scripts.push(pathname);
    // A CDN fetch means the loader was not configured before the editor mounted.
    if (!r.url().startsWith(sandbox().panelUrl) && !r.url().startsWith('data:')) foreign.push(r.url());
  });
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));

  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));
  // The panel polls in the background, so the network never goes idle; the
  // sidebar means the app has booted.
  await expect(page.getByRole('menuitem').first()).toBeVisible();
  expect(scripts.filter((s) => /editor\.api|monaco|worker/.test(s))).toEqual([]);

  await page.goto(editorPath);
  const editor = page.locator('.monaco-editor').first();
  await expect(editor).toBeVisible();
  await expect(editor).toContainText('traefik/whoami');

  // The YAML worker answers with the Compose schema: an unknown top-level key
  // gets a marker.
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type('\nnotacomposekey: 1');
  await expect(page.locator('.monaco-editor .squiggly-warning, .monaco-editor .squiggly-error').first()).toBeVisible({
    timeout: 15_000,
  });
  expect(scripts.some((s) => /yaml\.worker-/.test(s))).toBe(true);
  expect(errors).toEqual([]);
  expect(foreign).toEqual([]);
});
