// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { Database, DatabaseUser, LogicalDatabase } from '../data/types';
import { DatabaseUsersTab } from './DatabaseUsersTab';

const api = vi.hoisted(() => ({
  databaseLogicalDetails: vi.fn(),
  listDatabaseUsers: vi.fn(),
  createDatabaseUser: vi.fn(),
  setDatabaseUserAccess: vi.fn(),
  resetDatabaseUserPassword: vi.fn(),
  deleteDatabaseUser: vi.fn(),
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

const dbs: LogicalDatabase[] = [
  { anyUserCanConnect: false, charset: '', name: 'app', owner: 'app', protected: false, sizeBytes: 1 },
  { anyUserCanConnect: true, charset: '', name: 'postgres', owner: 'postgres', protected: true, sizeBytes: 1 },
  { anyUserCanConnect: false, charset: '', name: 'web', owner: 'app', protected: false, sizeBytes: 1 },
];
const users: DatabaseUser[] = [
  { access: { app: 'full', postgres: 'none', web: 'full' }, managed: true, name: 'app', owns: ['app', 'web'] },
  { access: { app: 'none', postgres: 'none', web: 'read' }, managed: false, name: 'legacy', owns: [] },
];

function renderTab(db: Database = shop) {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseUsersTab db={db} />
      </App>
    </SWRConfig>,
  );
}

function group(dialog: HTMLElement, name: string) {
  return within(dialog).getByRole('group', { name: `Access to ${name}` });
}

// jsdom treats every Segmented as one radio group (they share a name), so the
// selected item is read from its class.
function selected(el: HTMLElement): string | undefined {
  return el.querySelector('.ant-segmented-item-selected')?.textContent ?? undefined;
}

describe('DatabaseUsersTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.databaseLogicalDetails.mockResolvedValue({ databases: dbs });
    api.listDatabaseUsers.mockResolvedValue({ users });
    api.createDatabaseUser.mockResolvedValue({ name: 'bob' });
    api.setDatabaseUserAccess.mockResolvedValue(undefined);
    api.resetDatabaseUserPassword.mockResolvedValue(undefined);
    api.deleteDatabaseUser.mockResolvedValue(undefined);
  });

  it('says a user made outside the panel has an unknown password', async () => {
    const user = userEvent.setup();
    renderTab();
    const row = (await screen.findByText('legacy')).closest('tr') as HTMLElement;
    expect(within(row).getByText('password unknown')).toBeTruthy();
    const managed = screen.getByText('app').closest('tr') as HTMLElement;
    expect(within(managed).queryByText('password unknown')).toBeNull();
    await user.click(within(row).getByRole('button', { name: 'Reset password legacy' }));
    await user.click(await screen.findByRole('button', { name: 'Reset' }));
    await waitFor(() => expect(api.resetDatabaseUserPassword).toHaveBeenCalledWith(1, 'legacy'));
  });

  it('creates a user', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: /Create user/ }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'bob');
    await user.click(within(dialog).getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createDatabaseUser).toHaveBeenCalledWith(1, 'bob'));
  });

  it('sets one level per database and saves them all', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Access legacy' }));
    const dialog = await screen.findByRole('dialog');
    expect(selected(group(dialog, 'web'))).toBe('Read-only');
    await user.click(within(group(dialog, 'app')).getByText('Read-only'));
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.setDatabaseUserAccess).toHaveBeenCalledWith(1, 'legacy', { app: 'read', postgres: 'none', web: 'read' }),
    );
  });

  it('fixes the owner of a database at Full', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Access app' }));
    const dialog = await screen.findByRole('dialog');
    const g = group(dialog, 'app');
    expect(selected(g)).toBe('Full');
    expect((within(g).getByRole('radio', { name: 'None' }) as HTMLInputElement).disabled).toBe(true);
    expect((within(g).getByRole('radio', { name: 'Read-only' }) as HTMLInputElement).disabled).toBe(true);
  });

  it('refuses Full on a database postgres owns, and explains membership on the others', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Access legacy' }));
    const dialog = await screen.findByRole('dialog');
    expect((within(group(dialog, 'postgres')).getByRole('radio', { name: 'Full' }) as HTMLInputElement).disabled).toBe(true);
    expect(within(dialog).queryByText(/A member gets everything the owner role has/)).toBeNull();
    await user.click(within(group(dialog, 'app')).getByText('Full'));
    expect(within(dialog).getByText('A member gets everything the owner role has, in every database it owns.')).toBeTruthy();
    expect(within(dialog).getByText('Inside app the user acts as app, so what it creates belongs to app.')).toBeTruthy();
  });

  it('notes that MySQL lists only accounts on host %', async () => {
    api.listDatabaseUsers.mockResolvedValue({ users: [users[1]] });
    renderTab({ ...shop, engine: 'mysql', user: 'root', defaultDatabase: '' });
    await screen.findByText('legacy');
    expect(screen.getByText('Only accounts on host % are shown.')).toBeTruthy();
  });

  it('does not show that note on Postgres', async () => {
    renderTab();
    await screen.findByText('legacy');
    expect(screen.queryByText('Only accounts on host % are shown.')).toBeNull();
  });

  it('shows the 409 text of a refused delete', async () => {
    api.deleteDatabaseUser.mockRejectedValue(
      new Error('app owns databases: app, web. Give them another owner or drop them first'),
    );
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('legacy');
    await user.click(screen.getByRole('button', { name: 'Delete app' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText(/app owns databases: app, web/)).toBeTruthy();
  });
});

describe('Delete with grants', () => {
  it('confirms listing the panel users, then resends with confirmGrants', async () => {
    api.listDatabaseUsers.mockResolvedValue({
      users: [{ access: {}, managed: true, name: 'shop_ro', owns: [] }],
    });
    api.databaseLogicalDetails.mockResolvedValue({ databases: dbs });
    api.deleteDatabaseUser
      .mockRejectedValueOnce(new ApiError(409, 'shop_ro is used by alice@example.com; confirm to delete it and their access', undefined, { error: 'x', users: ['alice@example.com'] }))
      .mockResolvedValueOnce(undefined);
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <App>
          <DatabaseUsersTab db={shop} />
        </App>
      </SWRConfig>,
    );
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Delete shop_ro' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/alice@example\.com/)).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: /Delete/ }));
    await waitFor(() => expect(api.deleteDatabaseUser).toHaveBeenLastCalledWith(shop.id, 'shop_ro', true));
  });
});
