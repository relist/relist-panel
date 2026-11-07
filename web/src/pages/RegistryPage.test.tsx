// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { ExternalRegistry, RegistryCleanupState, RegistryState, RegistryTag } from '../data/types';
import { RegistryPage } from './RegistryPage';

const api = vi.hoisted(() => ({
  checkRegistry: vi.fn(),
  getRegistryCleanup: vi.fn(),
  previewRegistryCleanup: vi.fn(),
  runRegistryCleanup: vi.fn(),
  saveRegistryCleanup: vi.fn(),
  createPushCredential: vi.fn(),
  deletePushCredential: vi.fn(),
  deleteRegistryData: vi.fn(),
  deleteRegistryTag: vi.fn(),
  disableRegistry: vi.fn(),
  enableRegistry: vi.fn(),
  getRegistry: vi.fn(),
  listPushCredentials: vi.fn(),
  listRegistryRepositories: vi.fn(),
  listRegistryTags: vi.fn(),
  repairRegistry: vi.fn(),
  saveRegistry: vi.fn(),
  createExternalRegistry: vi.fn(),
  deleteExternalRegistry: vi.fn(),
  listExternalRegistries: vi.fn(),
  testExternalRegistry: vi.fn(),
  updateExternalRegistry: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const ghcr: ExternalRegistry = {
  host: 'ghcr.io',
  id: 1,
  lastTestAt: '2026-11-06T00:00:00Z',
  lastTestError: '',
  lastTestOk: true,
  username: 'ci',
};
const hub: ExternalRegistry = {
  host: 'docker.io',
  id: 2,
  lastTestAt: '2026-11-06T00:00:00Z',
  lastTestError: 'unauthorized: incorrect username or password',
  lastTestOk: false,
  username: 'me',
};

const off: RegistryState = {
  composesUsing: [],
  diskBytes: 0,
  lastError: '',
  readTimeout: '3600s',
  settings: { enabled: false, hostname: 'registry.example.com', tls: 'letsencrypt' },
  status: 'off',
};
const on: RegistryState = { ...off, diskBytes: 3 * 1024 * 1024, settings: { ...off.settings, enabled: true }, status: 'running' };

const cleanup: RegistryCleanupState = {
  next: '2026-11-08T03:00:00Z',
  running: false,
  runs: [
    {
      deleted: ['app:v0', 'app:v0.1'],
      finishedAt: '2026-11-07T03:02:00Z',
      freedBytes: 5 * 1024 * 1024,
      id: 4,
      log: 'app: deleted sha256:bbbb (v0, v0.1)',
      result: 'ok',
      startedAt: '2026-11-07T03:00:00Z',
      trigger: 'schedule',
    },
  ],
  settings: { days: ['daily'], enabled: true, keep: 10, lastRunAt: null, minute: 180 },
};

function show(path = '/registry?tab=external') {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter initialEntries={[path]}>
        <RegistryPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listExternalRegistries.mockResolvedValue([ghcr, hub]);
  api.getRegistry.mockResolvedValue(on);
  api.listPushCredentials.mockResolvedValue([]);
  api.listRegistryRepositories.mockResolvedValue([]);
  api.getRegistryCleanup.mockResolvedValue(cleanup);
});

describe('RegistryPage External registries', () => {
  it('lists host, username and the last Test result', async () => {
    show();
    const row = (await screen.findByText('ghcr.io')).closest('tr')!;
    expect(within(row).getByText('ci')).toBeTruthy();
    expect(within(row).getByText('Passed')).toBeTruthy();
    const failed = screen.getByText('docker.io').closest('tr')!;
    expect(within(failed).getByText('Failed')).toBeTruthy();
  });

  it('adds a registry with the pull-only hint on the form', async () => {
    api.createExternalRegistry.mockResolvedValue(ghcr);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getAllByRole('button', { name: /Add registry/ })[0]);
    expect(screen.getByText('Use a token that can only pull, such as a GitHub PAT with read:packages.')).toBeTruthy();
    await user.type(screen.getByLabelText('Host'), 'quay.io');
    await user.type(screen.getByLabelText('Username'), 'bot');
    await user.type(screen.getByLabelText('Token or password'), 'tok');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.createExternalRegistry).toHaveBeenCalledWith({ host: 'quay.io', secret: 'tok', username: 'bot' }));
  });

  it('puts a duplicate host error under the Host field', async () => {
    api.createExternalRegistry.mockRejectedValue(new ApiError(409, 'an External registry for this host already exists', 'host'));
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getAllByRole('button', { name: /Add registry/ })[0]);
    await user.type(screen.getByLabelText('Host'), 'ghcr.io');
    await user.type(screen.getByLabelText('Username'), 'bot');
    await user.type(screen.getByLabelText('Token or password'), 'tok');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('an External registry for this host already exists')).toBeTruthy();
  });

  it('edits without showing the stored secret and keeps it when left empty', async () => {
    api.updateExternalRegistry.mockResolvedValue(ghcr);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Edit ghcr.io' }));
    const secret = screen.getByLabelText('Token or password') as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.placeholder).toBe('Unchanged');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.updateExternalRegistry).toHaveBeenCalledWith(1, { host: 'ghcr.io', secret: '', username: 'ci' }));
  });

  it('tests a login and shows why it failed', async () => {
    api.testExternalRegistry.mockResolvedValue({ error: 'denied: denied', ok: false });
    api.listExternalRegistries
      .mockResolvedValueOnce([ghcr, hub])
      .mockResolvedValue([{ ...ghcr, lastTestError: 'denied: denied', lastTestOk: false }, hub]);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Test ghcr.io' }));
    await waitFor(() => expect(api.testExternalRegistry).toHaveBeenCalledWith(1));
    expect(await screen.findByText('denied: denied')).toBeTruthy();
  });

  it('deletes a registry after confirming', async () => {
    api.deleteExternalRegistry.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Delete ghcr.io' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.deleteExternalRegistry).toHaveBeenCalledWith(1));
  });

  it('shows an empty state with an Add registry button', async () => {
    api.listExternalRegistries.mockResolvedValue([]);
    show();
    expect(await screen.findByText('No External registries yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /Add registry/ }).length).toBe(2);
  });
});

describe('RegistryPage when the Registry is off', () => {
  it.each(['/registry', '/registry?tab=credentials'])('%s says so and links to Settings', async (path) => {
    api.getRegistry.mockResolvedValue(off);
    show(path);
    expect(await screen.findByText('Registry is off')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open Settings' }).getAttribute('href')).toBe('/registry?tab=settings');
    expect(api.listRegistryRepositories).not.toHaveBeenCalled();
    expect(api.listPushCredentials).not.toHaveBeenCalled();
  });
});

describe('RegistryPage Push credentials', () => {
  it('shows a new token once with the login command and the login-action snippet', async () => {
    api.createPushCredential.mockResolvedValue({
      credential: { createdAt: '2026-11-07T00:00:00Z', createdBy: 'admin', id: 3, name: 'github-ci' },
      token: 'tok-secret-123',
    });
    const user = userEvent.setup();
    show('/registry?tab=credentials');
    await user.click((await screen.findAllByRole('button', { name: /Create credential/ }))[0]);
    await user.type(screen.getByLabelText('Name'), 'github-ci');
    await user.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createPushCredential).toHaveBeenCalledWith('github-ci'));
    expect(await screen.findByText('tok-secret-123')).toBeTruthy();
    expect(screen.getByText('docker login registry.example.com -u github-ci')).toBeTruthy();
    expect(screen.getByText(/docker\/login-action@v3/)).toBeTruthy();
    expect(screen.getByText(/shown only once/)).toBeTruthy();
    const loads = api.listPushCredentials.mock.calls.length;
    await user.keyboard('{Escape}');
    expect(api.listPushCredentials.mock.calls.length).toBe(loads);
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(api.listPushCredentials.mock.calls.length).toBeGreaterThan(loads));
  });
});

describe('RegistryPage Repositories', () => {
  const tags: RegistryTag[] = [
    { created: '2026-11-06T00:00:00Z', digest: 'sha256:aaaaaaaaaaaaaaaa', inUseBy: [], kept: false, size: 2048, tag: 'v1' },
    { created: '2026-11-06T00:00:00Z', digest: 'sha256:aaaaaaaaaaaaaaaa', inUseBy: [], kept: false, size: 2048, tag: 'latest' },
    { created: '2026-11-05T00:00:00Z', digest: 'sha256:bbbbbbbbbbbbbbbb', inUseBy: [], kept: false, size: 1024, tag: 'v0' },
  ];

  async function openTags(user: ReturnType<typeof userEvent.setup>) {
    show('/registry');
    await user.click(await screen.findByRole('button', { name: 'Show tags of app' }));
    await screen.findByText('v0');
  }

  beforeEach(() => {
    api.listRegistryRepositories.mockResolvedValue([{ name: 'app', tagCount: 3 }]);
    api.listRegistryTags.mockResolvedValue(tags);
    api.deleteRegistryTag.mockResolvedValue({ deleted: ['latest', 'v1'] });
  });

  it('shows Disk used and the tags with the full image reference to copy', async () => {
    const user = userEvent.setup();
    await openTags(user);
    expect(screen.getByText('3.0 MB')).toBeTruthy();
    expect(api.listRegistryTags).toHaveBeenCalledWith('app');
    expect(screen.getAllByText('aaaaaaaaaaaa').length).toBe(2);
  });

  it('confirms a delete by listing the tags that share the digest', async () => {
    const user = userEvent.setup();
    await openTags(user);
    await user.click(screen.getByRole('button', { name: 'Delete app:v1' }));
    expect(await screen.findByText(/Also deletes latest/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Delete tag' }));
    await waitFor(() => expect(api.deleteRegistryTag).toHaveBeenCalledWith('app', 'v1', undefined));
  });

  it('sends the confirm when only a tag sharing the digest is in use', async () => {
    const user_ = { composeId: 7, composeName: 'web', environmentId: 2, environmentName: 'prod', projectId: 1, projectName: 'shop' };
    api.listRegistryTags.mockResolvedValue([tags[0], { ...tags[1], inUseBy: [user_] }, tags[2]]);
    const user = userEvent.setup();
    await openTags(user);
    await user.click(screen.getByRole('button', { name: 'Delete app:v1' }));
    await user.type(await screen.findByLabelText('Type v1 to confirm'), 'v1');
    await user.click(screen.getByRole('button', { name: 'Delete tag' }));
    await waitFor(() => expect(api.deleteRegistryTag).toHaveBeenCalledWith('app', 'v1', 'v1'));
  });

  it('needs the tag typed when a Compose uses it', async () => {
    const user_ = { composeId: 7, composeName: 'web', environmentId: 2, environmentName: 'prod', projectId: 1, projectName: 'shop' };
    api.listRegistryTags.mockResolvedValue([{ ...tags[0], inUseBy: [user_] }, tags[1], tags[2]]);
    const user = userEvent.setup();
    await openTags(user);
    expect(screen.getByRole('link', { name: 'web' }).getAttribute('href')).toBe('/projects/1/env/2/compose/7');
    await user.click(screen.getByRole('button', { name: 'Delete app:v1' }));
    const ok = await screen.findByRole('button', { name: 'Delete tag' });
    expect(ok.hasAttribute('disabled')).toBe(true);
    await user.type(screen.getByLabelText('Type v1 to confirm'), 'v1');
    expect(ok.hasAttribute('disabled')).toBe(false);
    await user.click(ok);
    await waitFor(() => expect(api.deleteRegistryTag).toHaveBeenCalledWith('app', 'v1', 'v1'));
  });
});

describe('RegistryPage Settings', () => {
  it('shows the status, Repair only on Drift, and the read timeout with a Traefik link', async () => {
    show('/registry?tab=settings');
    expect(await screen.findByText('Running')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Repair/ })).toBeNull();
    expect(screen.getByText('3600s')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Traefik settings' }).getAttribute('href')).toBe('/traefik');
  });

  it('offers Repair on Drift', async () => {
    api.getRegistry.mockResolvedValue({ ...on, status: 'drift' });
    api.repairRegistry.mockResolvedValue(on);
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: /Repair/ }));
    await waitFor(() => expect(api.repairRegistry).toHaveBeenCalled());
  });

  it.each(['stopped', 'error'] as const)('offers Repair when %s', async (status) => {
    api.getRegistry.mockResolvedValue({ ...on, lastError: status === 'error' ? 'pull failed' : '', status });
    show('/registry?tab=settings');
    expect(await screen.findByRole('button', { name: /Repair/ })).toBeTruthy();
  });

  it('names a refused connection as such', async () => {
    api.checkRegistry.mockResolvedValue({ error: 'dial tcp 203.0.113.9:443: connect: connection refused', ok: false, step: 'connect' });
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: /Check endpoint/ }));
    expect(await screen.findByText('Connection failed')).toBeTruthy();
  });

  it('names the failing step of Check endpoint', async () => {
    api.checkRegistry.mockResolvedValue({ error: 'x509: certificate is valid for traefik.default', ok: false, step: 'tls' });
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: /Check endpoint/ }));
    expect(await screen.findByText('TLS failed')).toBeTruthy();
    expect(screen.getByText('x509: certificate is valid for traefik.default')).toBeTruthy();
  });

  it('keeps the danger zone disabled while the Registry is on', async () => {
    show('/registry?tab=settings');
    const button = await screen.findByRole('button', { name: /Delete all registry data/ });
    expect(button.hasAttribute('disabled')).toBe(true);
  });

  it('deletes the data after the hostname is typed when off', async () => {
    api.getRegistry.mockResolvedValue(off);
    api.deleteRegistryData.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: /Delete all registry data/ }));
    await user.type(screen.getByLabelText('Type registry.example.com to confirm'), 'registry.example.com');
    await user.click(screen.getByRole('button', { name: 'Delete data' }));
    await waitFor(() => expect(api.deleteRegistryData).toHaveBeenCalledWith('registry.example.com'));
  });

  it('warns which Composes will fail to pull before Disable', async () => {
    api.getRegistry.mockResolvedValue({ ...on, composesUsing: ['web', 'worker'] });
    api.disableRegistry.mockResolvedValue({ warnings: ['web', 'worker'] });
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: 'Disable' }));
    expect(await screen.findByText(/web, worker/)).toBeTruthy();
    expect(screen.getByText(/next deploy will fail to pull/)).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Disable Registry' }));
    await waitFor(() => expect(api.disableRegistry).toHaveBeenCalled());
  });

  it('enables with the saved hostname', async () => {
    api.getRegistry.mockResolvedValue(off);
    api.enableRegistry.mockResolvedValue(on);
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: 'Enable' }));
    await waitFor(() => expect(api.enableRegistry).toHaveBeenCalled());
  });
});

describe('RegistryPage cleanup', () => {
  it('offers Daily or Weekly with weekdays, and saves the schedule', async () => {
    api.saveRegistryCleanup.mockResolvedValue({ ...cleanup, settings: { ...cleanup.settings, days: ['mon', 'thu'], keep: 5 } });
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await screen.findByText('Registry cleanup');
    expect(screen.queryByRole('checkbox', { name: 'Mon' })).toBeNull();
    await user.click(screen.getByRole('radio', { name: 'Weekly' }));
    await user.click(screen.getByRole('checkbox', { name: 'Mon' }));
    await user.click(screen.getByRole('checkbox', { name: 'Thu' }));
    const keep = screen.getByLabelText('Keep newest tags per repository');
    await user.clear(keep);
    await user.type(keep, '5');
    await user.click(screen.getByRole('button', { name: 'Save cleanup' }));
    await waitFor(() =>
      expect(api.saveRegistryCleanup).toHaveBeenCalledWith({ days: ['mon', 'thu'], enabled: true, keep: 5, minute: 180 }),
    );
  });

  it('previews what Run cleanup now deletes, then runs it', async () => {
    api.previewRegistryCleanup.mockResolvedValue({ app: ['v1', 'v2'] });
    api.runRegistryCleanup.mockResolvedValue({ runId: 5 });
    const user = userEvent.setup();
    show('/registry?tab=settings');
    await user.click(await screen.findByRole('button', { name: 'Run cleanup now' }));
    expect(await screen.findByText('v1, v2')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Run cleanup' }));
    await waitFor(() => expect(api.runRegistryCleanup).toHaveBeenCalled());
  });

  it('lists the last runs with the size freed', async () => {
    show('/registry?tab=settings');
    expect(await screen.findByText('5.0 MB')).toBeTruthy();
    expect(screen.getByText('Schedule')).toBeTruthy();
    expect(screen.getByText('2 tags')).toBeTruthy();
  });
});

describe('RegistryPage Kept tags', () => {
  it('badges the tags cleanup keeps', async () => {
    api.listRegistryRepositories.mockResolvedValue([{ name: 'app', tagCount: 2 }]);
    api.listRegistryTags.mockResolvedValue([
      { created: '2026-11-06T00:00:00Z', digest: 'sha256:aaaa', inUseBy: [], kept: true, size: 1, tag: 'v2' },
      { created: '2026-11-05T00:00:00Z', digest: 'sha256:bbbb', inUseBy: [], kept: false, size: 1, tag: 'v1' },
    ]);
    const user = userEvent.setup();
    show('/registry');
    await user.click(await screen.findByRole('button', { name: 'Show tags of app' }));
    await screen.findByText('v1');
    expect(screen.getAllByText('Kept').length).toBe(1);
  });
});

describe('RegistryPage External registries polish', () => {
  it('puts an error under the field the API names', async () => {
    api.createExternalRegistry.mockRejectedValue(new ApiError(422, 'username cannot contain ":"', 'username'));
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getAllByRole('button', { name: /Add registry/ })[0]);
    await user.type(screen.getByLabelText('Host'), 'quay.io');
    await user.type(screen.getByLabelText('Username'), 'a:b');
    await user.type(screen.getByLabelText('Token or password'), 'tok');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    const message = await screen.findByText('username cannot contain ":"');
    expect(message.closest('.ant-form-item')?.textContent).toContain('Username');
  });

  it('tests two logins at once, each with its own spinner', async () => {
    let release: () => void = () => {};
    api.testExternalRegistry.mockImplementation(() => new Promise((resolve) => (release = () => resolve({ error: '', ok: true }))));
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Test ghcr.io' }));
    await user.click(screen.getByRole('button', { name: 'Test docker.io' }));
    expect(api.testExternalRegistry).toHaveBeenCalledTimes(2);
    for (const name of ['Test ghcr.io', 'Test docker.io']) {
      expect(screen.getByRole('button', { name }).className).toContain('ant-btn-loading');
    }
    release();
  });

  it('wraps a long Test error instead of widening the table', async () => {
    api.listExternalRegistries.mockResolvedValue([{ ...hub, lastTestError: 'x'.repeat(300) }]);
    show();
    const error = await screen.findByText('x'.repeat(300));
    expect(error.style.overflowWrap).toBe('anywhere');
  });
});
