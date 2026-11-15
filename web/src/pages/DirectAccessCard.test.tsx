// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, DirectAccess } from '../data/types';

const api = vi.hoisted(() => ({
  closeDirectAccess: vi.fn(),
  extendDirectAccess: vi.fn(),
  listDirectAccesses: vi.fn(),
  openDirectAccess: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const { DirectAccessCard } = await import('./DirectAccessCard');

const shop: Database = {
  cpus: 0,
  createdAt: '2026-11-14T00:00:00Z',
  defaultDatabase: 'postgres',
  deleted: false,
  desired: 'running',
  engine: 'postgres',
  host: 'relist-db-7',
  hostReadable: 'shop.db.internal',
  id: 7,
  image: 'postgres:18',
  job: '',
  lastError: '',
  memoryMb: 0,
  name: 'Shop',
  port: 5432,
  settingsOutdated: false,
  slug: 'shop',
  status: 'running',
  user: 'postgres',
  version: '18',
  warnings: [],
};

function access(over: Partial<DirectAccess> = {}): DirectAccess {
  return {
    bind: '127.0.0.1',
    channel: 'host_port',
    expiresAt: new Date(Date.now() + 3_600_000).toISOString(),
    hostPort: 49153,
    id: 3,
    openedAt: new Date().toISOString(),
    targetId: 7,
    targetKind: 'database',
    targetPort: 5432,
    ...over,
  };
}

function view(db: Database = shop) {
  return render(
    <SWRConfig value={{ dedupingInterval: 0, provider: () => new Map() }}>
      <App>
        <DirectAccessCard db={db} />
      </App>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  api.listDirectAccesses.mockResolvedValue({ accesses: [] });
});

describe('DirectAccessCard, closed', () => {
  it('offers the durations, an optional port and a quiet bind by default', async () => {
    view();
    expect(await screen.findByText('Direct access')).toBeTruthy();
    expect(screen.getByPlaceholderText('Any free port')).toBeTruthy();
    expect(screen.getByText('1 hour')).toBeTruthy(); // the default
    await userEvent.click(screen.getByRole('combobox', { name: /duration/i }));
    for (const label of ['15 minutes', '1 hour', '4 hours', '8 hours']) {
      expect(await screen.findAllByText(label)).not.toHaveLength(0);
    }
    expect(screen.queryByText('Anyone on the internet can reach this Database')).toBeNull();
  });

  it('warns in red when All interfaces is ticked', async () => {
    view();
    await userEvent.click(await screen.findByRole('checkbox', { name: /all interfaces/i }));
    const alert = screen.getByText('Anyone on the internet can reach this Database').closest('.ant-alert') as HTMLElement;
    expect(alert.className).toContain('ant-alert-error');
    expect(
      within(alert).getByText('Docker publishes this port past host firewalls such as UFW. Only its password protects it.'),
    ).toBeTruthy();
  });

  it('opens with the default body, and with a typed port and all interfaces', async () => {
    api.openDirectAccess.mockResolvedValue(access());
    view();
    await userEvent.click(await screen.findByRole('button', { name: /^open$/i }));
    await waitFor(() => expect(api.openDirectAccess).toHaveBeenCalled());
    expect(api.openDirectAccess.mock.calls[0][0]).toEqual({
      allInterfaces: false,
      channel: 'host_port',
      hostPort: 0,
      minutes: 60,
      targetId: 7,
      targetKind: 'database',
    });
    expect(await screen.findByText(/Close now/)).toBeTruthy();
  });

  it('sends a typed port and the all-interfaces choice', async () => {
    api.openDirectAccess.mockResolvedValue(access({ bind: '0.0.0.0', hostPort: 15432 }));
    view();
    await userEvent.type(await screen.findByPlaceholderText('Any free port'), '15432');
    await userEvent.click(screen.getByRole('checkbox', { name: /all interfaces/i }));
    await userEvent.click(screen.getByRole('button', { name: /^open$/i }));
    await waitFor(() => expect(api.openDirectAccess).toHaveBeenCalled());
    expect(api.openDirectAccess.mock.calls[0][0]).toMatchObject({ allInterfaces: true, hostPort: 15432, minutes: 60 });
  });

  it('shows the server error when opening fails', async () => {
    api.openDirectAccess.mockRejectedValue(new Error('port 15432: that port is already in use on the host'));
    view();
    await userEvent.click(await screen.findByRole('button', { name: /^open$/i }));
    expect(await screen.findByText(/already in use on the host/)).toBeTruthy();
  });

  it('disables Open for a stopped Database and says why', async () => {
    view({ ...shop, status: 'stopped' });
    const open = await screen.findByRole('button', { name: /^open$/i });
    expect((open as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('Start the Database first')).toBeTruthy();
  });
});

describe('DirectAccessCard, open', () => {
  beforeEach(() => {
    api.listDirectAccesses.mockResolvedValue({ accesses: [access()] });
  });

  it('shows the bind, the host port, the ssh command and a URL without the password', async () => {
    view();
    expect(await screen.findByText('49153')).toBeTruthy();
    expect(screen.getByText('127.0.0.1')).toBeTruthy();
    expect(screen.getByText(/Time left/)).toBeTruthy();
    expect(screen.getByText('ssh -N -L 49153:127.0.0.1:49153 <user>@localhost')).toBeTruthy();
    expect(screen.getByText('postgres://postgres@127.0.0.1:49153/postgres')).toBeTruthy();
    expect(screen.getByText(/Use the password above/)).toBeTruthy();
    expect(screen.getByRole('button', { name: /extend/i })).toBeTruthy();
    expect(screen.getByRole('button', { name: /close now/i })).toBeTruthy();
  });

  it('shows the server address instead of an ssh command on all interfaces', async () => {
    api.listDirectAccesses.mockResolvedValue({ accesses: [access({ bind: '0.0.0.0', hostPort: 15432 })] });
    view();
    expect(await screen.findByText('localhost:15432')).toBeTruthy();
    expect(screen.queryByText(/^ssh -N -L/)).toBeNull();
    expect(screen.getByText('postgres://postgres@localhost:15432/postgres')).toBeTruthy();
  });

  it('builds the URL with the engine rules', async () => {
    api.listDirectAccesses.mockResolvedValue({ accesses: [access({ hostPort: 40000 })] });
    view({ ...shop, defaultDatabase: '0', engine: 'redis', port: 6379, user: 'default' });
    expect(await screen.findByText('redis://127.0.0.1:40000/0')).toBeTruthy();
  });

  it('extends with the chosen duration', async () => {
    api.extendDirectAccess.mockResolvedValue(access());
    view();
    await userEvent.click(await screen.findByRole('button', { name: /extend/i }));
    await waitFor(() => expect(api.extendDirectAccess).toHaveBeenCalledWith(3, 60));
  });

  it('closes after the confirmation', async () => {
    api.closeDirectAccess.mockResolvedValue(undefined);
    view();
    await userEvent.click(await screen.findByRole('button', { name: /close now/i }));
    await userEvent.click(await screen.findByRole('button', { name: /^close$/i }));
    await waitFor(() => expect(api.closeDirectAccess).toHaveBeenCalledWith(3));
  });

  it('refetches when the countdown reaches zero', async () => {
    api.listDirectAccesses.mockResolvedValueOnce({ accesses: [access({ expiresAt: new Date(Date.now() + 1200).toISOString() })] });
    api.listDirectAccesses.mockResolvedValue({ accesses: [] });
    view();
    await screen.findByText('49153');
    await waitFor(() => expect(api.listDirectAccesses.mock.calls.length).toBeGreaterThan(1), { timeout: 5000 });
    expect(await screen.findByRole('button', { name: /^open$/i })).toBeTruthy();
  });
});
