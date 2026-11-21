// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { SWRConfig } from 'swr';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database } from '../data/types';
import { DatabasePage } from './DatabasePage';

const api = vi.hoisted(() => ({
  databaseAction: vi.fn(),
  databaseCredentials: vi.fn(),
  databaseLogs: vi.fn(),
  deleteDatabase: vi.fn(),
  getDatabase: vi.fn(),
  renameDatabase: vi.fn(),
  updateDatabaseImage: vi.fn(),
  changeDatabaseVersion: vi.fn(),
  listVersionChanges: vi.fn(),
  deletePreviousData: vi.fn(),
  databaseBackupUrl: (id: number, backupId: number) => `/api/databases/${id}/backups/${backupId}/download`,
  listDatabaseBackups: vi.fn(),
  listDatabaseRestores: vi.fn(),
  databaseLogicalDatabases: vi.fn(),
  listDirectAccesses: vi.fn(),
  listDatabaseUsers: vi.fn(),
  databaseLogicalDetails: vi.fn(),
  getDatabaseSchedule: vi.fn(),
  resetSuperuserPassword: vi.fn(),
  recreateDatabase: vi.fn(),
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
  upgrades: [],
  slug: 'shop',
  status: 'running',
  user: 'postgres',
  version: '18',
  warnings: [],
};

function renderPage(entry = '/databases/1') {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route element={<DatabasePage />} path="/databases/:databaseId" />
            <Route element={<h1>Databases list</h1>} path="/databases" />
          </Routes>
        </MemoryRouter>
      </App>
    </SWRConfig>,
  );
}

describe('DatabasePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getDatabase.mockResolvedValue(shop);
    api.databaseLogs.mockResolvedValue({ logs: 'ready' });
    api.listDatabaseBackups.mockResolvedValue([]);
    api.listDatabaseRestores.mockResolvedValue([]);
    api.listDirectAccesses.mockResolvedValue({ accesses: [] });
    api.updateDatabaseImage.mockResolvedValue(undefined);
    api.changeDatabaseVersion.mockResolvedValue({ id: 1 });
    api.listVersionChanges.mockResolvedValue([]);
    api.listDatabaseUsers.mockResolvedValue({
      users: [
        { access: { app: 'full', postgres: 'none' }, managed: true, name: 'app', owns: ['app'] },
        { access: { app: 'none', postgres: 'read' }, managed: false, name: 'legacy', owns: [] },
      ],
    });
    api.databaseLogicalDetails.mockResolvedValue({ databases: [] });
    api.getDatabaseSchedule.mockResolvedValue({ all: true, databases: [], enabled: false });
    api.resetSuperuserPassword.mockResolvedValue(undefined);
  });

  it('orders the tabs of a SQL Database', async () => {
    renderPage();
    await screen.findByText('Host (stable)');
    const names = screen.getAllByRole('tab').map((tab) => tab.textContent);
    expect(names).toEqual(['Connection', 'Databases', 'Users', 'Query', 'Backups', 'Logs', 'Settings']);
  });

  it('opens the tab named by ?tab=', async () => {
    renderPage('/databases/1?tab=users');
    expect(await screen.findByRole('button', { name: /Create user/ })).toBeTruthy();
  });

  it('shows Redis neither the Databases nor the Users tab', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, engine: 'redis', user: 'default', defaultDatabase: '0', port: 6379 });
    renderPage();
    await screen.findByText('Host (stable)');
    const names = screen.getAllByRole('tab').map((tab) => tab.textContent);
    expect(names).not.toContain('Users');
    expect(names).not.toContain('Databases');
  });

  it('reveals the password of the user picked on the Connection tab', async () => {
    api.databaseCredentials.mockResolvedValue({
      env: 'DATABASE_URL=postgres://app:pw@relist-db-1:5432/app', password: 'pw', url: 'postgres://app:pw@relist-db-1:5432/app',
      urlReadable: '', user: 'app',
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('Host (stable)');
    const picker = await screen.findByRole('combobox', { name: 'User' });
    await user.click(picker);
    const options = await screen.findAllByText('app');
    await user.click(options[options.length - 1]);
    expect(screen.queryByText('legacy')).toBeNull(); // only managed users are offered
    await user.click(screen.getByRole('button', { name: /Reveal/ }));
    await waitFor(() => expect(api.databaseCredentials).toHaveBeenCalledWith(1, 'app', 'app'));
  });

  it('keeps Reset superuser password disabled until the name is typed, and warns', async () => {
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('Host (stable)');
    await user.click(await screen.findByRole('button', { name: 'Reset superuser password' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/stop connecting until given the new one/)).toBeTruthy();
    expect(within(dialog).getByText(/restarts/)).toBeTruthy();
    const ok = within(dialog).getByRole('button', { name: 'Reset password' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(within(dialog).getByRole('textbox'), 'Shop');
    expect(ok.disabled).toBe(false);
    await user.click(ok);
    await waitFor(() => expect(api.resetSuperuserPassword).toHaveBeenCalledWith(1, 'Shop'));
  });

  it('asks for the password only when Reveal is clicked', async () => {
    api.databaseCredentials.mockResolvedValue({
      env: 'DATABASE_URL=postgres://postgres:s3cret@relist-db-1:5432/postgres',
      password: 's3cret',
      url: 'postgres://postgres:s3cret@relist-db-1:5432/postgres',
      urlReadable: 'postgres://postgres:s3cret@shop.db.internal:5432/postgres',
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('Host (stable)');
    expect(api.databaseCredentials).not.toHaveBeenCalled();
    expect(screen.queryByText('s3cret')).toBeNull();
    await user.click(screen.getByRole('button', { name: /Reveal/ }));
    expect(await screen.findByText('s3cret')).toBeTruthy();
    expect(api.databaseCredentials).toHaveBeenCalledWith(1, undefined, undefined);
    expect(screen.getByText(/networks: \[relist-network\]/)).toBeTruthy();
  });

  it('ends the Connection tab with the Direct access card', async () => {
    renderPage();
    expect(await screen.findByText('Direct access')).toBeTruthy();
    expect(api.listDirectAccesses).toHaveBeenCalledWith('database', 1);
  });

  it('keeps Delete disabled until the typed name matches', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('tab', { name: 'Settings' }));
    await user.click(await screen.findByRole('button', { name: 'Delete database' }));
    const dialog = await screen.findByRole('dialog');
    const ok = within(dialog).getByRole('button', { name: 'Delete database' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(screen.getByLabelText('Type Shop to confirm'), 'Shop');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.deleteDatabase).toHaveBeenCalledWith(1, 'Shop', false));
  });

  it('says the data volume is gone and offers no Repair', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, status: 'volume_missing' });
    renderPage();
    expect(await screen.findByText('The data volume is gone')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Repair' })).toBeNull();
  });

  it('recreates a lost volume from the Backups the Admin picks, newest of each database first', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, status: 'volume_missing' });
    const backup = (id: number, database: string, startedAt: string, over = {}) => ({
      asBackedUp: '', database, error: '', finishedAt: startedAt, id, localRemoved: false, log: '', pinned: false,
      sha256: 'x', size: 1024, startedAt, status: 'succeeded', trigger: 'manual', upload: null, ...over,
    });
    api.listDatabaseBackups.mockResolvedValue([
      backup(4, 'app', '2026-11-20T10:00:00Z'),
      backup(3, 'app', '2026-11-19T10:00:00Z'),
      backup(2, 'shop', '2026-11-18T10:00:00Z'),
      backup(5, 'gone', '2026-11-20T11:00:00Z', { localRemoved: true }),
      backup(6, 'shop', '2026-11-21T10:00:00Z', { status: 'failed' }),
    ]);
    api.recreateDatabase.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Recreate and restore…' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByLabelText('Restore app')).toBeTruthy();
    expect(within(dialog).getByLabelText('Restore shop')).toBeTruthy();
    // Only a Backup on disk can be restored: a Remote only one is left out.
    expect(within(dialog).queryByLabelText('Restore gone')).toBeNull();
    await user.click(within(dialog).getByLabelText('Restore shop'));
    const ok = within(dialog).getByRole('button', { name: 'Recreate' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.recreateDatabase).toHaveBeenCalledWith(1, 'Shop', [4]));
  });

  it('keeps one primary button: Start, when a stopped Database also needs Repair', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, desired: 'stopped', lastError: 'boom', status: 'error' });
    renderPage();
    const start = await screen.findByRole('button', { name: 'Start' });
    const repair = screen.getByRole('button', { name: 'Repair' });
    expect(start.className).toContain('ant-btn-primary');
    expect(repair.className).not.toContain('ant-btn-primary');
  });

  it('offers no Recreate for Redis, which keeps no Backups', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, engine: 'redis', status: 'volume_missing' });
    renderPage();
    expect(await screen.findByText('The data volume is gone')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Recreate and restore…' })).toBeNull();
  });

  it('applies new settings only when the Admin asks', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, settingsOutdated: true });
    api.databaseAction.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderPage();
    expect(await screen.findByText('New container settings are available')).toBeTruthy();
    expect(api.databaseAction).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Apply settings' }));
    await waitFor(() => expect(api.databaseAction).toHaveBeenCalledWith(1, 'apply-settings'));
  });

  it('shows no settings notice when they are current', async () => {
    renderPage();
    await screen.findByText('Host (stable)');
    expect(screen.queryByText('New container settings are available')).toBeNull();
  });

  it('offers Repair on drift', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, status: 'drift' });
    renderPage();
    expect(await screen.findByRole('button', { name: 'Repair' })).toBeTruthy();
  });

  it('disables the lifecycle buttons and says so while a backup runs', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, job: 'backup' });
    renderPage();
    expect(await screen.findByText(/A backup is running/)).toBeTruthy();
    for (const name of ['Stop', 'Restart', 'Update image']) {
      expect((screen.getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
    }
  });

  it('updates the image with Back up first ticked by default', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Update image' }));
    const dialog = await screen.findByRole('dialog');
    const box = within(dialog).getByLabelText('Back up first') as HTMLInputElement;
    expect(box.checked).toBe(true);
    await user.click(within(dialog).getByRole('button', { name: 'Update image' }));
    await waitFor(() => expect(api.updateDatabaseImage).toHaveBeenCalledWith(1, true));
  });

  it('can update without a backup', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Update image' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Back up first'));
    await user.click(within(dialog).getByRole('button', { name: 'Update image' }));
    await waitFor(() => expect(api.updateDatabaseImage).toHaveBeenCalledWith(1, false));
  });

  it('unticks and disables Back up first on a stopped Database', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, desired: 'stopped', status: 'stopped' });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Update image' }));
    const dialog = await screen.findByRole('dialog');
    const box = within(dialog).getByLabelText('Back up first') as HTMLInputElement;
    expect(box.checked).toBe(false);
    expect(box.disabled).toBe(true);
    expect(within(dialog).getByText('Start the Database to back up first')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Update image' }));
    await waitFor(() => expect(api.updateDatabaseImage).toHaveBeenCalledWith(1, false));
  });

  it('has no Back up first and no Backups tab for Redis', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, engine: 'redis', image: 'redis:8' });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Update image' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByLabelText('Back up first')).toBeNull();
    expect(screen.queryByRole('tab', { name: 'Backups' })).toBeNull();
    await user.click(within(dialog).getByRole('button', { name: 'Update image' }));
    await waitFor(() => expect(api.updateDatabaseImage).toHaveBeenCalledWith(1, false));
  });

  it('shows a Backups tab for a SQL engine', async () => {
    renderPage();
    expect(await screen.findByRole('tab', { name: 'Backups' })).toBeTruthy();
  });

  it('sends deleteBackups only when the checkbox is ticked, off by default', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('tab', { name: 'Settings' }));
    await user.click(await screen.findByRole('button', { name: 'Delete database' }));
    const dialog = await screen.findByRole('dialog');
    const box = within(dialog).getByLabelText('Also delete its backups') as HTMLInputElement;
    expect(box.checked).toBe(false);
    await user.click(box);
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await user.click(within(dialog).getByRole('button', { name: 'Delete database' }));
    await waitFor(() => expect(api.deleteDatabase).toHaveBeenCalledWith(1, 'Shop', true));
  });

  it('shows only the Backups of a deleted Database', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, deleted: true, name: 'Shop (deleted #1)', status: 'deleted' });
    renderPage();
    expect(await screen.findByText('This Database was deleted. Its backups are kept until you delete them.')).toBeTruthy();
    expect(await screen.findByText('Backups')).toBeTruthy();
    expect(screen.queryByRole('tab', { name: 'Settings' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Update image' })).toBeNull();
  });
});



describe('Change version', () => {
  const pg16: Database = { ...shop, version: '16', image: 'postgres:16', upgrades: ['17', '18'] };

  beforeEach(() => {
    vi.clearAllMocks();
    api.databaseLogs.mockResolvedValue({ logs: 'ready' });
    api.listDatabaseBackups.mockResolvedValue([]);
    api.listDatabaseRestores.mockResolvedValue([]);
    api.listDirectAccesses.mockResolvedValue({ accesses: [] });
    api.listDatabaseUsers.mockResolvedValue({ users: [] });
    api.databaseLogicalDetails.mockResolvedValue({ databases: [] });
    api.getDatabaseSchedule.mockResolvedValue(undefined);
    api.changeDatabaseVersion.mockResolvedValue({ id: 1 });
    api.listVersionChanges.mockResolvedValue([]);
  });

  async function openChange(db: Database) {
    api.getDatabase.mockResolvedValue(db);
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: 'Change version' }));
    return { dialog: await screen.findByRole('dialog'), user };
  }

  it('shows the running change on the Settings tab as soon as it starts', async () => {
    api.getDatabase.mockResolvedValue(pg16);
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('tab', { name: 'Settings' }));
    await waitFor(() => expect(api.listVersionChanges).toHaveBeenCalled());
    expect(screen.queryByText('Version changes')).toBeNull();
    api.listVersionChanges.mockResolvedValue([
      {
        error: '',
        finishedAt: null,
        fromVersion: '16',
        id: 1,
        log: '',
        method: 'dump_restore',
        previousData: '',
        startedAt: '2026-11-17T10:00:00Z',
        status: 'running',
        toVersion: '18',
      },
    ]);
    await user.click(screen.getByRole('button', { name: 'Change version' }));
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Change to 18' }));
    const card = (await screen.findByText('Version changes')).closest('.ant-card') as HTMLElement;
    expect(within(card).getByText('Running')).toBeTruthy();
  });

  it('is not offered without a higher version', async () => {
    api.getDatabase.mockResolvedValue(shop);
    renderPage();
    await screen.findByRole('button', { name: 'Update image' });
    expect(screen.queryByRole('button', { name: 'Change version' })).toBeNull();
  });

  it('is disabled while a job runs', async () => {
    api.getDatabase.mockResolvedValue({ ...pg16, job: 'backup' });
    renderPage();
    const button = (await screen.findByRole('button', { name: 'Change version' })) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });

  it('moves postgres to the newest version by default, with no Back up first', async () => {
    const { dialog, user } = await openChange(pg16);
    expect(within(dialog).getByText(/moves the data to PostgreSQL 18 on a new volume/)).toBeTruthy();
    expect(within(dialog).getByText(/Clients are disconnected from the start/)).toBeTruthy();
    expect(within(dialog).queryByLabelText('Back up first')).toBeNull();
    await user.click(within(dialog).getByRole('combobox', { name: 'Version' }));
    expect((await screen.findAllByText('17')).length).toBeGreaterThan(0);
    await user.click(within(dialog).getByRole('button', { name: 'Change to 18' }));
    await waitFor(() => expect(api.changeDatabaseVersion).toHaveBeenCalledWith(1, '18', false));
  });

  it('can pick a lower step', async () => {
    const { dialog, user } = await openChange(pg16);
    await user.click(within(dialog).getByRole('combobox', { name: 'Version' }));
    await user.click((await screen.findAllByTitle('17')).at(-1)!);
    await user.click(within(dialog).getByRole('button', { name: 'Change to 17' }));
    await waitFor(() => expect(api.changeDatabaseVersion).toHaveBeenCalledWith(1, '17', false));
  });

  it('backs mariadb up first by default, and can skip it', async () => {
    const maria: Database = { ...shop, engine: 'mariadb', version: '11.4', image: 'mariadb:11.4', upgrades: ['11.8'] };
    const { dialog, user } = await openChange(maria);
    expect(within(dialog).getByText(/There is no going back to 11.4 once it starts/)).toBeTruthy();
    const box = within(dialog).getByLabelText('Back up first') as HTMLInputElement;
    expect(box.checked).toBe(true);
    await user.click(box);
    await user.click(within(dialog).getByRole('button', { name: 'Change to 11.8' }));
    await waitFor(() => expect(api.changeDatabaseVersion).toHaveBeenCalledWith(1, '11.8', false));
  });

  it('sends Back up first for mariadb when left ticked', async () => {
    const maria: Database = { ...shop, engine: 'mariadb', version: '11.4', image: 'mariadb:11.4', upgrades: ['11.8'] };
    const { dialog, user } = await openChange(maria);
    await user.click(within(dialog).getByRole('button', { name: 'Change to 11.8' }));
    await waitFor(() => expect(api.changeDatabaseVersion).toHaveBeenCalledWith(1, '11.8', true));
  });

  it('warns that redis cannot go back, with no backup option', async () => {
    const redis: Database = { ...shop, engine: 'redis', version: '7.4', image: 'redis:7.4', upgrades: ['8'] };
    const { dialog } = await openChange(redis);
    expect(within(dialog).getByText(/Redis 8 rewrites the data file in a format 7.4 cannot read/)).toBeTruthy();
    expect(within(dialog).queryByLabelText('Back up first')).toBeNull();
  });
});
