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
    scope: { all: true },
    sha256: 'abc',
    size: 1536,
    startedAt: '2026-11-14T10:00:00Z',
    status: 'succeeded',
    trigger: 'manual',
    ...over,
  };
}

function renderTab(db: Database = shop) {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseBackupsTab db={db} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseBackupsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listDatabaseBackups.mockResolvedValue([backup({})]);
    api.listDatabaseRestores.mockResolvedValue([]);
    api.databaseLogicalDatabases.mockResolvedValue({ databases: ['a', 'b'] });
    api.startDatabaseBackup.mockResolvedValue(backup({ status: 'running' }));
  });

  it('lists the Backups with their size, trigger and a Download link', async () => {
    renderTab();
    expect(await screen.findByText('1.5 KB')).toBeTruthy();
    expect(screen.getByText('Manual')).toBeTruthy();
    expect(screen.getByText('Whole instance')).toBeTruthy();
    const link = screen.getByRole('link', { name: /Download/ }) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/api/databases/1/backups/1/download');
  });

  it('backs up the whole instance by default', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Back up now' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Back up' }));
    await waitFor(() => expect(api.startDatabaseBackup).toHaveBeenCalledWith(1, { all: true }));
  });

  it('posts the selected databases, and keeps OK disabled until one is ticked', async () => {
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Back up now' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Selected databases'));
    const ok = within(dialog).getByRole('button', { name: 'Back up' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.click(await within(dialog).findByLabelText('a'));
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.startDatabaseBackup).toHaveBeenCalledWith(1, { databases: ['a'] }));
  });

  it('restores a Backup only after the Database name is typed', async () => {
    api.restoreDatabase.mockResolvedValue({});
    api.listDatabaseBackups.mockResolvedValue([backup({ scope: { databases: ['a', 'b'] } })]);
    const user = userEvent.setup();
    renderTab();
    await screen.findByText('1.5 KB');
    await user.click(screen.getByRole('button', { name: 'Restore' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Replaces these databases: a, b. Other databases are untouched.')).toBeTruthy();
    expect(within(dialog).getByText(/A backup of the current state is taken first/)).toBeTruthy();
    const ok = within(dialog).getByRole('button', { name: 'Restore' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.restoreDatabase).toHaveBeenCalledWith(1, 1, 'Shop'));
  });

  it('uploads a file to restore, after the same confirmation', async () => {
    api.restoreDatabaseUpload.mockResolvedValue({});
    const user = userEvent.setup();
    const { container } = renderTab();
    await screen.findByText('1.5 KB');
    const file = new File(['SELECT 1;'], 'dump.sql', { type: 'application/sql' });
    await user.upload(container.querySelector('input[type=file]') as HTMLInputElement, file);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Replaces the whole instance.')).toBeTruthy();
    expect(within(dialog).getByText('dump.sql')).toBeTruthy();
    await user.type(within(dialog).getByLabelText('Type Shop to confirm'), 'Shop');
    await user.click(within(dialog).getByRole('button', { name: 'Restore' }));
    await waitFor(() => expect(api.restoreDatabaseUpload).toHaveBeenCalledWith(1, file, 'Shop'));
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
});
