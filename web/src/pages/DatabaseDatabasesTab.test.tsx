// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, LogicalDatabase } from '../data/types';
import { DatabaseDatabasesTab } from './DatabaseDatabasesTab';

const api = vi.hoisted(() => ({
  databaseLogicalDetails: vi.fn(),
  createLogicalDatabase: vi.fn(),
  restrictLogicalDatabase: vi.fn(),
  dropLogicalDatabase: vi.fn(),
  getDatabaseSchedule: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const shop: Database = {
  cpus: 0,
  createdAt: '2026-11-14T00:00:00Z',
  defaultDatabase: 'postgres',
  deleted: false,
  desired: 'running',
  engine: 'postgres',
  host: 'relist-db-1',
  hostReadable: 'shop.db.internal',
  id: 1,
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

const app: LogicalDatabase = { anyUserCanConnect: false, charset: '', name: 'app', owner: 'app', protected: false, sizeBytes: 8192 };
const old: LogicalDatabase = { anyUserCanConnect: true, charset: '', name: 'legacy', owner: 'postgres', protected: false, sizeBytes: 1048576 };
const main: LogicalDatabase = { anyUserCanConnect: true, charset: '', name: 'postgres', owner: 'postgres', protected: true, sizeBytes: 7000 };

function renderTab(db: Database = shop) {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseDatabasesTab db={db} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseDatabasesTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.databaseLogicalDetails.mockResolvedValue({ databases: [app, old, main] });
    api.getDatabaseSchedule.mockResolvedValue({ all: true, databases: [], enabled: false });
    api.createLogicalDatabase.mockResolvedValue({ name: 'blog' });
    api.restrictLogicalDatabase.mockResolvedValue(undefined);
    api.dropLogicalDatabase.mockResolvedValue(undefined);
  });

  it('lists size and owner on Postgres', async () => {
    renderTab();
    const row = (await screen.findByText('legacy')).closest('tr') as HTMLElement;
    expect(within(row).getByText('1 MB')).toBeTruthy();
    expect(within(row).getByText('postgres')).toBeTruthy();
    const mine = screen.getAllByText('app')[0].closest('tr') as HTMLElement;
    expect(within(mine).getByText('8 KB')).toBeTruthy();
    expect(screen.getByText('Owner')).toBeTruthy();
  });

  it('lists the character set on MySQL', async () => {
    api.databaseLogicalDetails.mockResolvedValue({
      databases: [{ anyUserCanConnect: false, charset: 'utf8mb4', name: 'blog', owner: '', protected: false, sizeBytes: 100 }],
    });
    renderTab({ ...shop, engine: 'mysql', user: 'root', defaultDatabase: '' });
    expect(await screen.findByText('utf8mb4')).toBeTruthy();
    expect(screen.getByText('Character set')).toBeTruthy();
    expect(screen.queryByText('Any user can connect')).toBeNull();
  });

  it('marks only the databases any user can connect to, with Restrict', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    expect(screen.getAllByText('Any user can connect').length).toBe(2); // legacy and postgres
    expect(screen.queryByRole('button', { name: 'Restrict app' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Restrict legacy' }));
    await waitFor(() => expect(api.restrictLogicalDatabase).toHaveBeenCalledWith(1, 'legacy'));
  });

  it('creates a database with its owning user ticked by default', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: /Create database/ }));
    const dialog = await screen.findByRole('dialog');
    const box = within(dialog).getByRole('checkbox', { name: 'Create a user that owns it' }) as HTMLInputElement;
    expect(box.checked).toBe(true);
    await user.type(within(dialog).getByLabelText('Name'), 'blog');
    await user.click(within(dialog).getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createLogicalDatabase).toHaveBeenCalledWith(1, 'blog', true));
  });

  it('can create a database without a user', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: /Create database/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'blog');
    await user.click(within(dialog).getByRole('checkbox', { name: 'Create a user that owns it' }));
    await user.click(within(dialog).getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createLogicalDatabase).toHaveBeenCalledWith(1, 'blog', false));
  });

  it('drops only after the name is typed, backing up first by default', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Drop app' }));
    const dialog = await screen.findByRole('dialog');
    const ok = within(dialog).getByRole('button', { name: 'Drop database' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    expect((within(dialog).getByRole('checkbox', { name: 'Back up first' }) as HTMLInputElement).checked).toBe(true);
    await user.type(within(dialog).getByRole('textbox'), 'app');
    expect(ok.disabled).toBe(false);
    await user.click(ok);
    await waitFor(() => expect(api.dropLogicalDatabase).toHaveBeenCalledWith(1, 'app', 'app', true));
  });

  it('offers no Drop for a protected database', async () => {
    renderTab();
    await screen.findByText('legacy');
    expect(screen.getByRole('button', { name: 'Drop app' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Drop postgres' })).toBeNull();
  });

  it('says a drop also leaves the Backup schedule when the schedule names it', async () => {
    api.getDatabaseSchedule.mockResolvedValue({ all: false, databases: ['app'], enabled: true });
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Drop app' }));
    expect(await screen.findByText('It is also removed from the Backup schedule.')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Cancel' }));
    await user.click(screen.getByRole('button', { name: 'Drop legacy' }));
    await waitFor(() => expect(screen.queryByText('It is also removed from the Backup schedule.')).toBeNull());
  });

  it('shows the server error of a failed create', async () => {
    api.createLogicalDatabase.mockRejectedValue(new Error('a database named blog already exists'));
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: /Create database/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'blog');
    await user.click(within(dialog).getByRole('button', { name: 'Create' }));
    expect(await within(dialog).findByText('a database named blog already exists')).toBeTruthy();
  });
});
