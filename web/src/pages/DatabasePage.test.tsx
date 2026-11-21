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
  databaseBackupUrl: (id: number, backupId: number) => `/api/databases/${id}/backups/${backupId}/download`,
  listDatabaseBackups: vi.fn(),
  listDatabaseRestores: vi.fn(),
  databaseLogicalDatabases: vi.fn(),
  listDirectAccesses: vi.fn(),
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

function renderPage() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <MemoryRouter initialEntries={['/databases/1']}>
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
    expect(api.databaseCredentials).toHaveBeenCalledWith(1);
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
