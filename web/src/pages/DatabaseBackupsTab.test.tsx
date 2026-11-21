// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, DatabaseBackup, DatabaseRestore } from '../data/types';
import { DatabaseBackupsTab } from './DatabaseBackupsTab';

const api = vi.hoisted(() => ({
  databaseBackupUrl: (id: number, backupId: number) => `/api/databases/${id}/backups/${backupId}/download`,
  databaseLogicalDatabases: vi.fn(),
  deleteDatabaseBackup: vi.fn(),
  getDatabaseSchedule: vi.fn(),
  pinDatabaseBackup: vi.fn(),
  listBackupDestinations: vi.fn(),
  retryDatabaseBackupUpload: vi.fn(),
  listDatabaseBackups: vi.fn(),
  listDatabaseRestores: vi.fn(),
  restoreDatabase: vi.fn(),
  restoreDatabaseUpload: vi.fn(),
  startDatabaseBackup: vi.fn(),
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
  higherVersions: [],
  slug: 'shop',
  status: 'running',
  user: 'postgres',
  version: '18',
  warnings: [],
};

function backup(over: Partial<DatabaseBackup>): DatabaseBackup {
  return {
    error: '',
    finishedAt: '2026-11-14T10:00:05Z',
    id: 1,
    log: '',
    database: 'app',
    sha256: 'abc',
    asBackedUp: '',
    localRemoved: false,
    pinned: false,
    upload: null,
    size: 1536,
    startedAt: '2026-11-14T10:00:00Z',
    status: 'succeeded',
    trigger: 'manual',
    ...over,
  };
}

function renderTab(db: Database = shop, onOpenUsers?: () => void) {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseBackupsTab db={db} onOpenUsers={onOpenUsers} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseBackupsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listDatabaseBackups.mockResolvedValue([backup({})]);
    api.listDatabaseRestores.mockResolvedValue([]);
    api.listBackupDestinations.mockResolvedValue([]);
    api.getDatabaseSchedule.mockResolvedValue({
      all: true, databases: [], days: ['daily'], enabled: false, lastError: '', lastRunAt: null, localKeepCount: 7,
      localKeepDays: null, minute: 180, next: null, remoteKeepCount: 30, remoteKeepDays: null, destinationId: null, timeZone: 'UTC (UTC+00:00)',
    });
    api.databaseLogicalDatabases.mockResolvedValue({ databases: ['a', 'app'] });
    api.startDatabaseBackup.mockResolvedValue([backup({ status: 'running' })]);
  });

  it('lists the Backups with their database, size, trigger and a Download link', async () => {
    renderTab();
    expect(await screen.findByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('Manual')).toBeTruthy();
    expect(screen.getByText('app')).toBeTruthy();
    const link = screen.getByRole('link', { name: /Download/ }) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/api/databases/1/backups/1/download');
  });

  it('backs up all databases by default, one file each', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Back up now' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('One file per database.')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Back up' }));
    await waitFor(() => expect(api.startDatabaseBackup).toHaveBeenCalledWith(1, { all: true }));
  });

  it('posts the selected databases, and keeps OK disabled until one is ticked', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Back up now' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Choose databases'));
    const ok = within(dialog).getByRole('button', { name: 'Back up' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.click(await within(dialog).findByLabelText('a'));
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.startDatabaseBackup).toHaveBeenCalledWith(1, { databases: ['a'] }));
  });

  it('restores a Backup into its own database by default, after the name is typed', async () => {
    api.restoreDatabase.mockResolvedValue({});
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('Replaces the database app. It is backed up first. Apps connected to it are disconnected.')).toBeTruthy();
    const ok = within(dialog).getByRole('button', { name: 'Restore' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.restoreDatabase).toHaveBeenCalledWith(1, 1, 'app', 'Shop', false));
  });

  it('restores into a new database under the typed name', async () => {
    api.restoreDatabase.mockResolvedValue({});
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText(/Replaces the database app/);
    await user.click(within(dialog).getByLabelText('Restore into a new database'));
    await user.type(within(dialog).getByLabelText('New database name'), 'app_copy');
    expect(within(dialog).getByText('Creates the database app_copy.')).toBeTruthy();
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await user.click(within(dialog).getByRole('button', { name: 'Restore' }));
    await waitFor(() => expect(api.restoreDatabase).toHaveBeenCalledWith(1, 1, 'app_copy', 'Shop', false));
  });

  for (const untick of [false, true]) {
    it(`creates a new database owned as it was backed up${untick ? ', or not when unticked' : ''}`, async () => {
      api.restoreDatabase.mockResolvedValue({});
      api.listDatabaseBackups.mockResolvedValue([backup({ asBackedUp: 'app_owner' })]);
      const user = userEvent.setup();
      renderTab();
      await screen.findByText('1.5 KB');
      await user.click(screen.getByRole('button', { name: 'Restore' }));
      const dialog = await screen.findByRole('dialog');
      await within(dialog).findByText(/Replaces the database app/);
      // Replacing keeps the live database's owner: no choice to make.
      expect(within(dialog).queryByLabelText('Owned by app_owner, as when it was backed up')).toBeNull();
      await user.click(within(dialog).getByLabelText('Restore into a new database'));
      await user.type(within(dialog).getByLabelText('New database name'), 'app_copy');
      const keep = within(dialog).getByLabelText('Owned by app_owner, as when it was backed up') as HTMLInputElement;
      expect(keep.checked).toBe(true);
      if (untick) await user.click(keep);
      await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
      await user.click(within(dialog).getByRole('button', { name: 'Restore' }));
      await waitFor(() => expect(api.restoreDatabase).toHaveBeenCalledWith(1, 1, 'app_copy', 'Shop', !untick));
    });
  }

  it('defaults to a new database named after a Backup whose database is gone', async () => {
    api.listDatabaseBackups.mockResolvedValue([backup({ database: 'gone' })]);
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText('Creates the database gone.')).toBeTruthy();
  });

  it('disables Restore on a Backup of the earlier, several-databases format', async () => {
    api.listDatabaseBackups.mockResolvedValue([backup({ database: '' })]);
    renderTab();
    expect(await screen.findByText('Several databases')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Restore' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('uploads a file only after a target is picked, and sends it in the query', async () => {
    api.restoreDatabaseUpload.mockResolvedValue({});
    const user = userEvent.setup();
    const { container } = renderTab();
    await screen.findByText('1.5 KB');
    expect(screen.getByText('A dump of one database: pg_dump <db> or mysqldump <db>.')).toBeTruthy();
    const file = new File(['SELECT 1;'], 'dump.sql', { type: 'application/sql' });
    await user.upload(container.querySelector('input[type=file]') as HTMLInputElement, file);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('dump.sql')).toBeTruthy();
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    const ok = within(dialog).getByRole('button', { name: 'Restore' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.click(within(dialog).getByLabelText('Restore into a new database'));
    await user.type(within(dialog).getByLabelText('New database name'), 'imported');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.restoreDatabaseUpload).toHaveBeenCalledWith(1, file, 'imported', 'Shop'));
  });

  it('shows the error and the log of a failed Backup when its row expands', async () => {
    api.listDatabaseBackups.mockResolvedValue([
      backup({ error: 'pg_dumpall exited with code 1', log: 'connection lost', size: 0, status: 'failed' }),
    ]);
    const user = userEvent.setup();
    renderTab();
    await user.click(await screen.findByRole('button', { name: /Expand row/i }));
    expect(await screen.findByText(/pg_dumpall exited with code 1/)).toBeTruthy();
    expect(screen.getByText(/connection lost/)).toBeTruthy();
  });

  it('links a failed restore to its pre-restore Backup and shows its log', async () => {
    const restore: DatabaseRestore = {
      backupId: 1,
      error: 'the restore reported errors',
      finishedAt: '2026-11-14T11:00:05Z',
      id: 4,
      log: 'ERROR: syntax error',
      preBackupId: 2,
      source: 'backup',
      target: 'app',
      startedAt: '2026-11-14T11:00:00Z',
      status: 'failed',
      uploadName: '',
    };
    api.listDatabaseBackups.mockResolvedValue([backup({ id: 1 }), backup({ id: 2, trigger: 'pre_restore' })]);
    api.listDatabaseRestores.mockResolvedValue([restore]);
    const user = userEvent.setup();
    renderTab();
    const link = await screen.findByRole('link', { name: 'Backup #2' });
    expect(link.getAttribute('href')).toBe('/api/databases/1/backups/2/download');
    const expanders = screen.getAllByRole('button', { name: /Expand row/i });
    await user.click(expanders[expanders.length - 1]);
    expect(await screen.findByText(/ERROR: syntax error/)).toBeTruthy();
  });

  it('links a restore that needs a missing role to the Users tab', async () => {
    const restore: DatabaseRestore = {
      backupId: 1, error: 'the restore command exited with code 3', finishedAt: '2026-11-14T11:00:05Z', id: 4,
      log: 'psql:dump.sql:12: ERROR:  role "shop_app" does not exist', preBackupId: null, source: 'backup', target: 'app',
      startedAt: '2026-11-14T11:00:00Z', status: 'failed', uploadName: '',
    };
    api.listDatabaseRestores.mockResolvedValue([restore]);
    const onOpenUsers = vi.fn();
    const user = userEvent.setup();
    renderTab(shop, onOpenUsers);
    const expanders = await screen.findAllByRole('button', { name: /Expand row/i });
    await user.click(expanders[expanders.length - 1]);
    const link = await screen.findByRole('link', { name: 'Create the user shop_app on the Users tab' });
    expect(link.getAttribute('href')).toBe('?tab=users');
    await user.click(link);
    expect(onOpenUsers).toHaveBeenCalled();
  });

  it('labels a pre_drop Backup', async () => {
    api.listDatabaseBackups.mockResolvedValue([backup({ trigger: 'pre_drop' })]);
    renderTab();
    expect(await screen.findByText('Before drop')).toBeTruthy();
  });

  it('disables Back up now while a job runs or the Database is stopped', async () => {
    const { unmount } = renderTab({ ...shop, job: 'backup' });
    await screen.findByText('1.5 KB');
    expect((screen.getByRole('button', { name: 'Back up now' }) as HTMLButtonElement).disabled).toBe(true);
    unmount();
    renderTab({ ...shop, status: 'stopped' });
    await screen.findByText('1.5 KB');
    expect((screen.getByRole('button', { name: 'Back up now' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('deletes a Backup after Popconfirm', async () => {
    api.deleteDatabaseBackup.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    const confirm = await screen.findAllByRole('button', { name: 'Delete' });
    await user.click(confirm[confirm.length - 1]);
    await waitFor(() => expect(api.deleteDatabaseBackup).toHaveBeenCalledWith(1, 1));
  });

  it('offers only Download and Delete on a deleted Database', async () => {
    renderTab({ ...shop, deleted: true, status: 'deleted' });
    await screen.findByText('1.5 KB');
    expect(screen.queryByRole('button', { name: 'Back up now' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Restore' })).toBeNull();
    expect(screen.getByRole('link', { name: /Download/ })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeTruthy();
  });

  it('pins a scheduled Backup, and offers no pin on a manual one', async () => {
    api.pinDatabaseBackup.mockResolvedValue(undefined);
    api.listDatabaseBackups.mockResolvedValue([backup({ id: 1, trigger: 'manual' }), backup({ id: 2, trigger: 'scheduled' })]);
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('Scheduled');
    expect(screen.getAllByRole('button', { name: 'Pin' })).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Pin' }));
    await waitFor(() => expect(api.pinDatabaseBackup).toHaveBeenCalledWith(1, 2, true));
  });

  it('shows a pinned Backup with Unpin', async () => {
    api.listDatabaseBackups.mockResolvedValue([backup({ id: 2, pinned: true, trigger: 'scheduled' })]);
    const user = userEvent.setup();
    renderTab();
    await user.click(await screen.findByRole('button', { name: 'Unpin' }));
    await waitFor(() => expect(api.pinDatabaseBackup).toHaveBeenCalledWith(1, 2, false));
  });

  it('marks a Backup whose file was rotated away as Remote only, with Download and Restore disabled', async () => {
    api.listDatabaseBackups.mockResolvedValue([backup({ localRemoved: true, trigger: 'scheduled' })]);
    renderTab();
    expect(await screen.findByText('Remote only')).toBeTruthy();
    expect(screen.queryByRole('link', { name: /Download/ })).toBeNull();
    expect((screen.getByRole('button', { name: /Download/ }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: 'Restore' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('shows each copy: Copied, Copying and Copy failed with Retry', async () => {
    api.retryDatabaseBackupUpload.mockResolvedValue(undefined);
    api.listDatabaseBackups.mockResolvedValue([
      backup({ id: 1, trigger: 'scheduled', upload: { destination: 'minio', encrypted: false, error: '', status: 'uploaded', uploadedAt: '2026-11-14T10:01:00Z' } }),
      backup({ id: 2, trigger: 'scheduled', upload: { destination: 'minio', encrypted: false, error: '', status: 'running', uploadedAt: null } }),
      backup({ id: 3, trigger: 'scheduled', upload: { destination: 'minio', encrypted: false, error: 'connection refused', status: 'failed', uploadedAt: null } }),
    ]);
    const user = userEvent.setup();
    renderTab();
    expect(await screen.findByText('Copied')).toBeTruthy();
    expect(screen.getByText('Copying…')).toBeTruthy();
    expect(screen.getByText('Copy failed')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(api.retryDatabaseBackupUpload).toHaveBeenCalledWith(1, 3));
  });

  it('says a delete also deletes the copy in the destination', async () => {
    api.listDatabaseBackups.mockResolvedValue([
      backup({ id: 1, trigger: 'scheduled', upload: { destination: 'minio', encrypted: false, error: '', status: 'uploaded', uploadedAt: '2026-11-14T10:01:00Z' } }),
    ]);
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('Copied');
    await user.click(screen.getByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('Also deletes its copy in minio.')).toBeTruthy();
  });
});

describe('encrypted copies', () => {
  it('says an uploaded copy is encrypted', async () => {
    api.listDatabaseBackups.mockResolvedValue([
      backup({
        id: 1,
        trigger: 'scheduled',
        upload: { destination: 'minio', encrypted: true, error: '', status: 'uploaded', uploadedAt: '2026-11-14T10:01:00Z' },
      }),
    ]);
    const user = userEvent.setup();
    renderTab();
    await user.hover(await screen.findByText('Copied'));
    expect(await screen.findByText(/Encrypted \(\.age\)/)).toBeTruthy();
  });

  it('asks for the passphrase of an age file and sends it in the restore', async () => {
    api.restoreDatabaseUpload.mockResolvedValue({});
    const user = userEvent.setup();
    const { container } = renderTab();
    await screen.findByText('1.5 KB');
    const file = new File(['age-encryption.org/v1\nrest'], 'dump.sql.age', { type: 'application/octet-stream' });
    await user.upload(container.querySelector('input[type=file]') as HTMLInputElement, file);
    const dialog = await screen.findByRole('dialog');
    const pass = await within(dialog).findByLabelText('Passphrase');
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await user.click(within(dialog).getByLabelText('Restore into a new database'));
    await user.type(within(dialog).getByLabelText('New database name'), 'imported');
    const ok = within(dialog).getByRole('button', { name: 'Restore' }) as HTMLButtonElement;
    // The passphrase is required before Restore enables.
    expect(ok.disabled).toBe(true);
    await user.type(pass, 'restore-passphrase-0123456789');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() =>
      expect(api.restoreDatabaseUpload).toHaveBeenCalledWith(1, file, 'imported', 'Shop', 'restore-passphrase-0123456789'),
    );
  }, 15_000);

  it('shows no passphrase field for a plain file', async () => {
    const user = userEvent.setup();
    const { container } = renderTab();
    await screen.findByText('1.5 KB');
    const file = new File(['SELECT 1;'], 'dump.sql', { type: 'application/sql' });
    await user.upload(container.querySelector('input[type=file]') as HTMLInputElement, file);
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText('dump.sql');
    expect(within(dialog).queryByLabelText('Passphrase')).toBeNull();
  });
});
