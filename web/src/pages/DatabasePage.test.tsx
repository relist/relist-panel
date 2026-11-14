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
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const shop: Database = {
  cpus: 0,
  createdAt: '2026-11-14T00:00:00Z',
  defaultDatabase: 'postgres',
  desired: 'running',
  engine: 'postgres',
  host: 'relist-db-1',
  hostReadable: 'shop.db.internal',
  id: 1,
  image: 'postgres:18',
  lastError: '',
  memoryMb: 0,
  name: 'Shop',
  port: 5432,
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
    await waitFor(() => expect(api.deleteDatabase).toHaveBeenCalledWith(1, 'Shop'));
  });

  it('says the data volume is gone and offers no Repair', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, status: 'volume_missing' });
    renderPage();
    expect(await screen.findByText('The data volume is gone')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Repair' })).toBeNull();
  });

  it('offers Repair on drift', async () => {
    api.getDatabase.mockResolvedValue({ ...shop, status: 'drift' });
    renderPage();
    expect(await screen.findByRole('button', { name: 'Repair' })).toBeTruthy();
  });
});
