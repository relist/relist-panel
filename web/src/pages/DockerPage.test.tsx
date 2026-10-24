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
  dockerPrunePlan: vi.fn(),
  globalDeploymentQueue: vi.fn(),
  resumeDocker: vi.fn(),
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
  mocks.dockerPrunePlan.mockResolvedValue({
    kind: 'all-volumes',
    delete: [],
    skip: [],
  });
  mocks.globalDeploymentQueue.mockResolvedValue([]);
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
  describe('prune plan', () => {
    const plan = {
      kind: 'all-volumes',
      delete: [
        { name: 'stray', size: 10 },
        { name: 'old-cache', size: null },
      ],
      skip: [{ name: 'shop_data', compose: 'shop', reason: 'used by Compose shop' }],
    };
    async function open(name: string) {
      show();
      if (name === 'Prune all unused volumes')
        fireEvent.click(await screen.findByText('Advanced cleanup'));
      const button = await screen.findByRole('button', { name });
      await waitFor(() => expect(button.hasAttribute('disabled')).toBe(false));
      fireEvent.click(button);
      return screen.getByRole('dialog');
    }
    it('lists what is deleted and what is kept for a panel Compose', async () => {
      mocks.dockerPrunePlan.mockResolvedValue(plan);
      const dialog = await open('Prune all unused volumes');
      expect(mocks.dockerPrunePlan).toHaveBeenCalledWith('all-volumes');
      expect(await within(dialog).findByText('Deleted (2)')).toBeTruthy();
      expect(within(dialog).getByText('stray')).toBeTruthy();
      expect(within(dialog).getByText('old-cache')).toBeTruthy();
      expect(
        within(dialog).getByText('Kept because a Compose uses them (1)'),
      ).toBeTruthy();
      expect(within(dialog).getByText('shop_data — Compose shop')).toBeTruthy();
    });
    it('caps a long list with an and-N-more line', async () => {
      mocks.dockerPrunePlan.mockResolvedValue({
        kind: 'all-volumes',
        delete: Array.from({ length: 53 }, (_, i) => ({
          name: `vol-${i}`,
          size: null,
        })),
        skip: [],
      });
      const dialog = await open('Prune all unused volumes');
      expect(await within(dialog).findByText('Deleted (53)')).toBeTruthy();
      expect(within(dialog).getByText('vol-49')).toBeTruthy();
      expect(within(dialog).queryByText('vol-50')).toBeNull();
      expect(within(dialog).getByText('and 3 more')).toBeTruthy();
    });
    it('includes panel data behind the stronger confirmation word', async () => {
      mocks.dockerPrunePlan.mockResolvedValue(plan);
      mocks.pruneDocker.mockResolvedValue({
        kind: 'all-volumes',
        spaceReclaimed: 0,
        deleted: [],
      });
      const dialog = await open('Prune all unused volumes');
      await within(dialog).findByText('Deleted (2)');
      fireEvent.click(
        within(dialog).getByRole('checkbox', {
          name: 'Also delete the ones panel Composes use',
        }),
      );
      expect(await within(dialog).findByText('Deleted (3)')).toBeTruthy();
      expect(within(dialog).queryByText(/Kept because/)).toBeNull();
      fireEvent.change(within(dialog).getByRole('textbox'), {
        target: { value: 'DELETE VOLUMES' },
      });
      expect(
        within(dialog).getByRole('button', { name: 'Prune' }).hasAttribute('disabled'),
      ).toBe(true);
      fireEvent.change(within(dialog).getByRole('textbox'), {
        target: { value: 'DELETE PANEL DATA' },
      });
      fireEvent.click(within(dialog).getByRole('button', { name: 'Prune' }));
      await waitFor(() =>
        expect(mocks.pruneDocker).toHaveBeenCalledWith(
          'all-volumes',
          'DELETE PANEL DATA',
          true,
        ),
      );
    });
    it('shows what the server skipped', async () => {
      mocks.dockerPrunePlan.mockResolvedValue(plan);
      mocks.pruneDocker.mockResolvedValue({
        kind: 'all-volumes',
        spaceReclaimed: 0,
        deleted: ['stray'],
        skipped: [
          { name: 'shop_data', compose: 'shop', reason: 'used by Compose shop' },
        ],
      });
      const dialog = await open('Prune all unused volumes');
      await within(dialog).findByText('Deleted (2)');
      fireEvent.change(within(dialog).getByRole('textbox'), {
        target: { value: 'DELETE VOLUMES' },
      });
      fireEvent.click(within(dialog).getByRole('button', { name: 'Prune' }));
      expect(await screen.findByText('Skipped 1')).toBeTruthy();
      expect(screen.getByText('shop_data (used by Compose shop)')).toBeTruthy();
    });
    it('disables confirm when the plan cannot be fetched', async () => {
      mocks.dockerPrunePlan.mockRejectedValue(new Error('plan unavailable'));
      const dialog = await open('Prune all unused volumes');
      expect(await within(dialog).findByText('plan unavailable')).toBeTruthy();
      fireEvent.change(within(dialog).getByRole('textbox'), {
        target: { value: 'DELETE VOLUMES' },
      });
      expect(
        within(dialog).getByRole('button', { name: 'Prune' }).hasAttribute('disabled'),
      ).toBe(true);
    });
    it('keeps the container confirmation unchanged', async () => {
      const dialog = await open('Prune stopped containers');
      expect(mocks.dockerPrunePlan).not.toHaveBeenCalled();
      expect(within(dialog).queryByRole('checkbox')).toBeNull();
      expect(within(dialog).queryByText(/Deleted \(/)).toBeNull();
    });
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
