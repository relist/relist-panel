// @vitest-environment jsdom
import '../test/dom-setup';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DockerPage } from './DockerPage';

const mocks = vi.hoisted(() => ({
  dockerReport: vi.fn(),
  pruneDocker: vi.fn(),
  globalDeploymentQueue: vi.fn(),
  resumeDocker: vi.fn(),
  migrationStatus: vi.fn(),
}));
vi.mock('../data/api', () => ({ api: mocks }));
const report = {
  checkedAt: new Date().toISOString(),
  version: '28.0',
  apiVersion: '1.48',
  hostname: 'docker-host',
  storageDriver: 'overlay2',
  dockerRootDir: '/var/lib/docker',
  containersRunning: 2,
  containersStopped: 1,
  containersPaused: 0,
  usage: [
    { kind: 'images', count: 3, active: 2, size: 400, reclaimable: null },
    { kind: 'containers', count: 3, active: 2, size: 120, reclaimable: 20 },
    { kind: 'volumes', count: 1, active: -1, size: null, reclaimable: null },
    { kind: 'build-cache', count: 4, active: 1, size: 100, reclaimable: 30 },
    { kind: 'networks', count: 3, active: -1, size: null, reclaimable: null },
  ],
  containers: [
    { id: 'abc', names: ['/web'], image: 'web:1', state: 'running', size: 40 },
  ],
  volumes: [
    { name: 'database', driver: 'local', size: null, references: null },
  ],
  networks: [],
  warnings: [],
};
function show() {
  return render(
    <SWRConfig
      value={{
        provider: () => new Map(),
        revalidateOnFocus: false,
        revalidateOnReconnect: false,
        shouldRetryOnError: false,
      }}
    >
      <MemoryRouter>
        <App>
          <DockerPage />
        </App>
      </MemoryRouter>
    </SWRConfig>,
  );
}
beforeEach(() => {
  mocks.dockerReport.mockResolvedValue(report);
  mocks.resumeDocker.mockResolvedValue({ resumed: true });
  mocks.globalDeploymentQueue.mockResolvedValue([]);
  mocks.migrationStatus.mockResolvedValue({ step: '', claimedBy: '', targetUrl: '', lockedAt: null, migratedAt: null, tokenExpiresAt: null });
  mocks.pruneDocker.mockResolvedValue({
    kind: 'build-cache',
    spaceReclaimed: 1024,
    deleted: ['cache-id'],
  });
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe('Docker host management', () => {
  it('shows Docker reports and keeps unavailable volume sizes honest', async () => {
    show();
    expect(await screen.findByText('docker-host')).toBeTruthy();
    expect(
      screen.getByRole('link', { name: 'Manage images' }).getAttribute('href'),
    ).toBe('/images');
    fireEvent.click(screen.getByRole('tab', { name: 'Volumes' }));
    expect(await screen.findByText('database')).toBeTruthy();
    expect(screen.getAllByText('Unavailable').length).toBeGreaterThan(0);
  });
  it('requires typed confirmation and displays actual reclaimed space', async () => {
    show();
    const button = await screen.findByRole('button', {
      name: 'Prune build cache',
    });
    await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
    fireEvent.click(button);
    const dialog = screen.getByRole('dialog');
    const confirm = within(dialog).getByRole('button', { name: 'Prune' });
    expect(confirm.hasAttribute('disabled')).toBe(true);
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'PRUNE' },
    });
    fireEvent.click(confirm);
    await waitFor(() =>
      expect(mocks.pruneDocker).toHaveBeenCalledWith('build-cache', 'PRUNE'),
    );
    expect(await screen.findByText(/Reclaimed 1.0 kB/)).toBeTruthy();
    await waitFor(() => expect(mocks.dockerReport).toHaveBeenCalledWith(true));
  });
  it('keeps the previous report but disables pruning when manual refresh fails', async () => {
    show();
    await screen.findByText('docker-host');
    mocks.dockerReport.mockRejectedValueOnce(
      new Error('Docker refresh failed'),
    );
    fireEvent.click(screen.getByRole('button', { name: /Refresh/ }));
    expect(await screen.findByText('Docker refresh failed')).toBeTruthy();
    expect(screen.getByText('docker-host')).toBeTruthy();
    expect(
      screen
        .getByRole('button', { name: 'Prune build cache' })
        .hasAttribute('disabled'),
    ).toBe(true);
  });
  it('requires acknowledgement before resuming after an uncertain prune', async () => {
    mocks.dockerReport.mockResolvedValue({
      ...report,
      maintenanceBlocked: true,
    });
    show();
    expect(
      await screen.findByText(/Previous prune completion is unknown/),
    ).toBeTruthy();
    expect(
      screen
        .getByRole('button', { name: 'Prune build cache' })
        .hasAttribute('disabled'),
    ).toBe(true);
    fireEvent.click(
      screen.getByRole('button', { name: 'Resume Docker operations' }),
    );
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByText(/Verify directly with the Docker daemon/),
    ).toBeTruthy();
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'RESUME DOCKER' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Resume' }));
    await waitFor(() =>
      expect(mocks.resumeDocker).toHaveBeenCalledWith('RESUME DOCKER'),
    );
  });
  it('warns about persistent data and requires the volume confirmation', async () => {
    show();
    const button = await screen.findByRole('button', {
      name: 'Prune anonymous volumes',
    });
    await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
    fireEvent.click(button);
    const dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByText(
        /Deleted volume data is not restored if a later deployment fails/,
      ),
    ).toBeTruthy();
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'PRUNE' },
    });
    expect(
      within(dialog)
        .getByRole('button', { name: 'Prune' })
        .hasAttribute('disabled'),
    ).toBe(true);
  });
  it('disables pruning while the queue has work', async () => {
    mocks.globalDeploymentQueue.mockResolvedValue([
      { composeId: 1, status: 'queued', number: 1 },
    ]);
    show();
    expect(
      await screen.findByText(/Deployment queue has pending or running work/),
    ).toBeTruthy();
    expect(
      screen
        .getByRole('button', { name: 'Prune build cache' })
        .hasAttribute('disabled'),
    ).toBe(true);
  });
  it('preserves a failed prune on screen and never retries it automatically', async () => {
    mocks.pruneDocker.mockRejectedValue(new Error('Docker refused the prune'));
    show();
    const button = await screen.findByRole('button', {
      name: 'Prune stopped containers',
    });
    await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
    fireEvent.click(button);
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByRole('textbox'), {
      target: { value: 'PRUNE' },
    });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Prune' }));
    expect(await screen.findByText('Docker refused the prune')).toBeTruthy();
    expect(mocks.pruneDocker).toHaveBeenCalledTimes(1);
  });
});
