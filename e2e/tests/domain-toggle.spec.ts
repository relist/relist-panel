import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import { Panel, call, runId, sandbox, traefikUrl } from '../lib/sandbox.js';

// Turning a Domain off without deleting it (ADR-0052,
// .scratch/domain-toggle/spec.md): the switch applies on the next deploy,
// Traefik then answers the host with 404, the Service keeps its .internal
// alias on relist-network, and the host stays the Project's.

test.describe.configure({ mode: 'serial' });

const id = runId();
const host = `dt-${id}.localhost`;
const projectName = `dt-${id}`;

let panel: Panel;
let composePath = '';
let domainId = 0;

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

/** Fetches url from a throwaway container on relist-network, as another Compose would. */
function fetchOnSharedNetwork(url: string): string {
  return execFileSync('docker', ['run', '--rm', '--network', 'relist-network', 'busybox', 'wget', '-qO-', '-T', '10', url], {
    encoding: 'utf8',
    env: dockerEnv(),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

/** Polls until Traefik answers the expected status: it reads labels as containers start. */
async function expectStatus(url: string, status: number) {
  await expect.poll(async () => (await call(url)).status, { timeout: 30_000, intervals: [250, 500, 1000] }).toBe(status);
}

async function setEnabled(enabled: boolean) {
  return panel.json('PUT', `${composePath}/domains/${domainId}/enabled`, { enabled });
}

test.beforeAll(async () => {
  panel = await Panel.signIn();
  const project = await panel.json('POST', '/api/projects', { name: projectName, description: 'domain-toggle.spec' });
  const envs = await panel.json<{ id: number; name: string }[]>('GET', `/api/projects/${project.id}/environments`);
  const env = envs[0] ?? (await panel.json('POST', `/api/projects/${project.id}/environments`, { name: 'production' }));
  const base = `/api/projects/${project.id}/environments/${env.id}/composes`;
  const compose = await panel.json('POST', base, { name: 'web', yamlContent: 'services:\n  whoami:\n    image: traefik/whoami\n' });
  composePath = `${base}/${compose.id}`;
  domainId = (
    await panel.json('POST', `${composePath}/domains`, {
      host,
      path: '/',
      internalPath: '/',
      stripPath: false,
      service: 'whoami',
      port: 80,
      entrypoint: '',
      https: false,
      certProvider: 'none',
      certResolver: '',
      middlewares: [],
      authGroupId: null,
    })
  ).id;
});

test('a Domain that is on routes to its Service', async () => {
  await panel.deploy(composePath);
  await expectStatus(traefikUrl(host, 'http'), 200);
});

test('turning it off applies on the next deploy and answers 404', async () => {
  const off = await setEnabled(false);
  expect(off.enabled).toBe(false);
  const compose = await panel.json('GET', composePath);
  expect(compose.domainsChangedAt).not.toBeNull();
  // Nothing changes on the host until the deploy.
  expect((await call(traefikUrl(host, 'http'))).status).toBe(200);

  await panel.deploy(composePath);
  await expectStatus(traefikUrl(host, 'http'), 404);
});

test('the Service keeps its .internal alias while its Domain is off', async () => {
  const body = fetchOnSharedNetwork(`http://whoami.web.production.${projectName}.internal/`);
  expect(body).toContain('Hostname:');
});

test('another Project cannot take the host while the Domain is off', async () => {
  const other = await panel.json('POST', '/api/projects', { name: `dt-other-${id}`, description: 'domain-toggle.spec' });
  const envs = await panel.json<{ id: number }[]>('GET', `/api/projects/${other.id}/environments`);
  const base = `/api/projects/${other.id}/environments/${envs[0].id}/composes`;
  const compose = await panel.json('POST', base, { name: 'web', yamlContent: 'services:\n  whoami:\n    image: traefik/whoami\n' });
  const taken = await panel.raw('POST', `${base}/${compose.id}/domains`, {
    host,
    path: '/',
    internalPath: '/',
    stripPath: false,
    service: 'whoami',
    port: 80,
    entrypoint: '',
    https: false,
    certProvider: 'none',
    certResolver: '',
    middlewares: [],
    authGroupId: null,
  });
  expect(taken.status).toBe(409);
});

test('turning it on again routes the host after the next deploy', async () => {
  expect((await setEnabled(true)).enabled).toBe(true);
  await panel.deploy(composePath);
  await expectStatus(traefikUrl(host, 'http'), 200);
});

test.afterAll(async () => {
  if (composePath) {
    await panel.raw('POST', `${composePath}/stop`).catch(() => undefined);
  }
});
