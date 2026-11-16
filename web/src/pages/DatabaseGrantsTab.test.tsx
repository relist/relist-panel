// @vitest-environment jsdom
import '../test/dom-setup';

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database } from '../data/types';
import { DatabaseGrantsTab } from './DatabaseGrantsTab';

const api = vi.hoisted(() => ({
  databaseGrants: vi.fn(),
  deleteDatabaseGrant: vi.fn(),
  listDatabaseUsers: vi.fn(),
  listUsers: vi.fn(),
  setDatabaseGrant: vi.fn(),
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

const cache: Database = { ...shop, defaultDatabase: '0', engine: 'redis', id: 2, name: 'Cache', port: 6379, user: 'default' };

const panelUsers = [
  { createdAt: '2026-01-01T00:00:00Z', email: 'admin@example.com', id: 1, isAdmin: true, isOwner: true, lastLoginAt: null, status: 'active', twoFactorEnabled: false },
  { createdAt: '2026-01-01T00:00:00Z', email: 'alice@example.com', id: 2, isAdmin: false, isOwner: false, lastLoginAt: null, status: 'active', twoFactorEnabled: false },
] as never[];

function renderTab(db: Database) {
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseGrantsTab db={db} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseGrantsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listUsers.mockResolvedValue(panelUsers);
    api.listDatabaseUsers.mockResolvedValue({
      users: [
        { access: {}, managed: true, name: 'shop_ro', owns: [] },
        { access: {}, managed: false, name: 'legacy', owns: [] },
      ],
    });
  });

  it('lists the grants', async () => {
    api.databaseGrants.mockResolvedValue([
      { createdAt: '2026-11-16T00:00:00Z', dbUser: 'shop_ro', email: 'alice@example.com', userId: 2 },
    ]);
    renderTab(shop);
    expect(await screen.findByText('alice@example.com')).toBeTruthy();
    expect(screen.getByText('shop_ro')).toBeTruthy();
  });

  it('adds an SQL grant: a non-Admin panel user and a managed user', async () => {
    api.databaseGrants.mockResolvedValue([]);
    api.setDatabaseGrant.mockResolvedValue(undefined);
    renderTab(shop);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Add/ }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Panel user'));
    expect(await screen.findByRole('option', { name: 'alice@example.com' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'admin@example.com' })).toBeNull();
    fireEvent.click(screen.getByText('alice@example.com', { selector: '.ant-select-item-option-content' }));
    await user.click(within(dialog).getByLabelText('Database user'));
    expect(await screen.findByRole('option', { name: 'shop_ro' })).toBeTruthy();
    expect(screen.queryByRole('option', { name: 'legacy' })).toBeNull();
    fireEvent.click(screen.getByText('shop_ro', { selector: '.ant-select-item-option-content' }));
    await user.click(within(dialog).getByRole('button', { name: /Give access/ }));
    await waitFor(() => expect(api.setDatabaseGrant).toHaveBeenCalledWith(shop.id, 2, { dbUser: 'shop_ro' }));
  });

  it('adds a Redis grant at a level', async () => {
    api.databaseGrants.mockResolvedValue([]);
    api.setDatabaseGrant.mockResolvedValue(undefined);
    renderTab(cache);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: /Add/ }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Panel user'));
    await screen.findByRole('option', { name: 'alice@example.com' });
    fireEvent.click(screen.getByText('alice@example.com', { selector: '.ant-select-item-option-content' }));
    expect(within(dialog).queryByLabelText('Database user')).toBeNull();
    fireEvent.click(within(dialog).getByRole('radio', { name: 'Read-write' }));
    await user.click(within(dialog).getByRole('button', { name: /Give access/ }));
    await waitFor(() => expect(api.setDatabaseGrant).toHaveBeenCalledWith(cache.id, 2, { level: 'read_write' }));
    expect(api.listDatabaseUsers).not.toHaveBeenCalled();
  });

  it('removes a grant after a confirmation', async () => {
    api.databaseGrants.mockResolvedValue([
      { createdAt: '2026-11-16T00:00:00Z', dbUser: 'shop_ro', email: 'alice@example.com', userId: 2 },
    ]);
    api.deleteDatabaseGrant.mockResolvedValue(undefined);
    renderTab(shop);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Remove alice@example.com' }));
    await user.click(await screen.findByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(api.deleteDatabaseGrant).toHaveBeenCalledWith(shop.id, 2));
  });
});
