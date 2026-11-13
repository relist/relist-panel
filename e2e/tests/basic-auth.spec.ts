import { expect, test } from '@playwright/test';
import { Panel, call, runId, sandbox, traefikUrl } from '../lib/sandbox.js';

// Basic auth groups end to end (ADR-0036, .scratch/basic-auth/spec.md): the
// panel writes a group to Traefik's file provider, a Domain naming the group
// asks for its users, and edits apply without a redeploy. One Compose running
// traefik/whoami (it echoes request headers) is shared by the steps below.

test.describe.configure({ mode: 'serial' });

const id = runId();
const hostA = `ba-${id}.localhost`; // HTTP only
const hostB = `bas-${id}.localhost`; // HTTPS, certificate provider None
const bobPassword = 'pa$$ "w#rd x1';

let panel: Panel;
let composePath = '';
let groupId = 0;
let aliceId = 0;
let alicePassword = '';
let domainA = 0;

function domainInput(host: string, https: boolean, authGroupId: number | null) {
  return {
    host,
    path: '/',
    internalPath: '/',
    stripPath: false,
    service: 'whoami',
    port: 80,
    entrypoint: '',
    https,
    certProvider: 'none',
    certResolver: '',
    middlewares: [],
    authGroupId,
  };
}

/** Polls until Traefik answers the expected status: file-provider edits take a moment. */
async function expectStatus(url: string, status: number, auth?: string) {
  await expect
    .poll(async () => (await call(url, { auth })).status, { timeout: 20_000, intervals: [250, 500, 1000] })
    .toBe(status);
}

test.beforeAll(async () => {
  panel = await Panel.signIn();
  const project = await panel.json('POST', '/api/projects', { name: `e2e-${id}`, description: 'basic-auth.spec' });
  // A new Project comes with a production Environment.
  const envs = await panel.json<{ id: number; name: string }[]>('GET', `/api/projects/${project.id}/environments`);
  const env = envs[0] ?? (await panel.json('POST', `/api/projects/${project.id}/environments`, { name: 'production' }));
  const base = `/api/projects/${project.id}/environments/${env.id}/composes`;
  const compose = await panel.json('POST', base, {
    name: 'web',
    yamlContent: 'services:\n  whoami:\n    image: traefik/whoami\n',
  });
  composePath = `${base}/${compose.id}`;
});

test('an admin creates a group and a user in the browser, and reveals the password', async ({ page }) => {
  const env = sandbox();
  await page.goto('/signin');
  await page.getByLabel(/email/i).fill(env.adminEmail);
  await page.getByLabel(/password/i).first().fill(env.adminPassword);
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForURL((u) => !u.pathname.startsWith('/signin'));

  await page.getByRole('menuitem', { name: 'Basic auth' }).click();
  await expect(page.getByRole('heading', { name: 'Basic auth' })).toBeVisible();
  // The header button; an empty list shows a second one.
  await page.getByRole('button', { name: 'New group' }).first().click();
  const create = page.getByRole('dialog', { name: 'New group' });
  await create.getByLabel('Group name').fill(`Staging ${id}`);
  await create.getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/basic-auth\/\d+$/);
  groupId = Number(new URL(page.url()).pathname.split('/').pop());

  await page.getByRole('button', { name: 'Add user' }).click();
  const add = page.getByRole('dialog', { name: 'Add user' });
  await add.getByLabel('Username').fill('alice');
  await add.getByRole('button', { name: 'Generate' }).click();
  await add.getByRole('button', { name: 'Add', exact: true }).click();
  await expect(add).toBeHidden();

  await page.getByRole('button', { name: 'Show password of alice' }).click();
  const group = await panel.json('GET', `/api/auth-groups/${groupId}`);
  aliceId = group.users.find((u: { username: string }) => u.username === 'alice').id;
  ({ password: alicePassword } = await panel.json('GET', `/api/auth-groups/${groupId}/users/${aliceId}/password`));
  expect(alicePassword).toHaveLength(24);
  await expect(page.getByText(alicePassword)).toBeVisible();
});

test('no response but the reveal carries a password or a hash', async () => {
  for (const pathname of ['/api/auth-groups', `/api/auth-groups/${groupId}`]) {
    const body = (await panel.raw('GET', pathname)).body;
    expect(body).not.toMatch(/"(password|hash)"/);
    expect(body).not.toContain(alicePassword);
  }
});

test('Traefik asks for the group on HTTP and HTTPS Domains', async () => {
  await panel.json('POST', `/api/auth-groups/${groupId}/users`, { username: 'bob', password: bobPassword });
  domainA = (await panel.json('POST', `${composePath}/domains`, domainInput(hostA, false, groupId))).id;
  await panel.json('POST', `${composePath}/domains`, domainInput(hostB, true, groupId));
  await panel.deploy(composePath);

  const a = traefikUrl(hostA, 'http');
  await expectStatus(a, 401);
  const challenge = await call(a);
  expect(challenge.headers['www-authenticate']).toContain(`realm="Staging ${id}"`);

  const ok = await call(a, { auth: `alice:${alicePassword}` });
  expect(ok.status).toBe(200);
  // removeHeader: the app never sees the credentials.
  expect(ok.body).not.toMatch(/^Authorization:/im);
  expect((await call(a, { auth: 'alice:wrong-password' })).status).toBe(401);
  expect((await call(a, { auth: `bob:${bobPassword}` })).status).toBe(200);

  // HTTP to HTTPS redirects before any challenge.
  const redirect = await call(traefikUrl(hostB, 'http'));
  expect([301, 308]).toContain(redirect.status);
  expect(redirect.headers.location).toMatch(/^https:/);
  expect((await call(traefikUrl(hostB, 'https'))).status).toBe(401);
  expect((await call(traefikUrl(hostB, 'https'), { auth: `alice:${alicePassword}` })).status).toBe(200);
});

test('password and user changes apply without a redeploy', async () => {
  const before = (await panel.json<unknown[]>('GET', `${composePath}/deployments`)).length;
  const a = traefikUrl(hostA, 'http');
  const newPassword = `new-${id}-password-xyz`;
  await panel.json('PATCH', `/api/auth-groups/${groupId}/users/${aliceId}`, { password: newPassword });
  await expectStatus(a, 401, `alice:${alicePassword}`);
  await expectStatus(a, 200, `alice:${newPassword}`);
  alicePassword = newPassword;

  const group = await panel.json('GET', `/api/auth-groups/${groupId}`);
  const bob = group.users.find((u: { username: string }) => u.username === 'bob');
  await panel.json('DELETE', `/api/auth-groups/${groupId}/users/${bob.id}`);
  await expectStatus(a, 401, `bob:${bobPassword}`);

  expect((await panel.json<unknown[]>('GET', `${composePath}/deployments`)).length).toBe(before);
});

test('an empty group refuses everyone', async () => {
  const empty = await panel.json('POST', '/api/auth-groups', { name: `Empty ${id}` });
  await panel.json('PUT', `${composePath}/domains/${domainA}`, domainInput(hostA, false, empty.id));
  await panel.deploy(composePath);
  await expectStatus(traefikUrl(hostA, 'http'), 401, `alice:${alicePassword}`);

  await panel.json('PUT', `${composePath}/domains/${domainA}`, domainInput(hostA, false, groupId));
  await panel.deploy(composePath);
  await expectStatus(traefikUrl(hostA, 'http'), 200, `alice:${alicePassword}`);
  await panel.json('DELETE', `/api/auth-groups/${empty.id}`);
});

test('a group cannot be deleted while a Domain or a running container uses it', async () => {
  const used = await panel.raw('DELETE', `/api/auth-groups/${groupId}`);
  expect(used.status).toBe(409);
  const hosts = JSON.parse(used.body).domains.map((d: { host: string }) => d.host).sort();
  expect(hosts).toEqual([hostB, hostA].sort());

  // Both Domains leave the group but are not redeployed: the containers'
  // labels still name its middleware.
  const { domains } = await panel.json('GET', `${composePath}/domains`);
  for (const d of domains) {
    await panel.json('PUT', `${composePath}/domains/${d.id}`, domainInput(d.host, d.https, null));
  }
  const running = await panel.raw('DELETE', `/api/auth-groups/${groupId}`);
  expect(running.status).toBe(409);
  expect(JSON.parse(running.body).error).toMatch(/Running containers still use this group: .*whoami.*Redeploy/);

  await panel.deploy(composePath);
  await panel.json('DELETE', `/api/auth-groups/${groupId}`);
  await expectStatus(traefikUrl(hostA, 'http'), 200);
});

test('the audit log records reveals and never a password', async () => {
  const reveals = await panel.raw('GET', '/api/audit?action=auth_group_user.password_read');
  expect(reveals.status).toBe(200);
  expect(reveals.body).toContain(`Staging ${id}`);
  const recent = await panel.raw('GET', '/api/audit?action=auth_group');
  for (const body of [reveals.body, recent.body]) {
    expect(body).not.toContain(alicePassword);
    expect(body).not.toContain('w#rd');
  }
});

test.afterAll(async () => {
  // Leave nothing running: stop the Compose so later specs start clean.
  if (composePath) {
    await panel.raw('POST', `${composePath}/stop`).catch(() => undefined);
  }
});
