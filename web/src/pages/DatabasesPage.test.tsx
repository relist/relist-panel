// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { Database, DatabaseEngine } from '../data/types';
import { DatabasesPage } from './DatabasesPage';

const api = vi.hoisted(() => ({
  createDatabase: vi.fn(),
  listDatabaseEngines: vi.fn(),
  listDatabases: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const engines: DatabaseEngine[] = [
  { label: 'MariaDB', name: 'mariadb', port: 3306, versions: [{ default: true, tag: '11.8' }, { default: false, tag: '11.4' }] },
  { label: 'PostgreSQL', name: 'postgres', port: 5432, versions: [{ default: true, tag: '18' }, { default: false, tag: '17' }] },
];

const shop: Database = {
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
  cpus: 0,
};

function renderPage() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter>
        <DatabasesPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

describe('DatabasesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listDatabaseEngines.mockResolvedValue(engines);
    api.listDatabases.mockResolvedValue([shop]);
  });

  it('lists the Databases with engine, status and host', async () => {
    renderPage();
    const link = await screen.findByRole('link', { name: 'Shop' });
    expect(link.getAttribute('href')).toBe('/databases/1');
    const row = link.closest('tr') as HTMLElement;
    expect(within(row).getByText('PostgreSQL 18')).toBeTruthy();
    expect(within(row).getByText('Running')).toBeTruthy();
    expect(within(row).getByText('relist-db-1')).toBeTruthy();
  });

  it('creates a Database with the default engine and its default version', async () => {
    api.createDatabase.mockResolvedValue({ ...shop, name: 'Blog', status: 'creating' });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Create database/ }));
    await user.type(await screen.findByLabelText('Name'), 'Blog');
    await user.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createDatabase).toHaveBeenCalledWith({ engine: 'postgres', name: 'Blog', version: '18' }));
  });

  it("shows the API's refusal in the form", async () => {
    api.createDatabase.mockRejectedValue(new ApiError(409, 'a Database with this name already exists'));
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole('button', { name: /Create database/ }));
    await user.type(await screen.findByLabelText('Name'), 'Shop');
    await user.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findByText('a Database with this name already exists')).toBeTruthy();
  });
});
