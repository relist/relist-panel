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
  authStatus: vi.fn(),
  createDatabase: vi.fn(),
  listDatabaseEngines: vi.fn(),
  listDatabases: vi.fn(),
  listProjects: vi.fn(),
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
  slug: 'shop',
  settingsOutdated: false,
  upgrades: [],
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

describe('DatabasesPage as a member', () => {
  it('shows their rows without Create database', async () => {
    const { PanelProvider } = await import('../data/store');
    api.authStatus.mockResolvedValue({
      authenticated: true, user: { id: 3, email: 'bob@example.com', isAdmin: false, isOwner: false }, access: { projects: [] },
    });
    api.listProjects.mockResolvedValue([]);
    api.listDatabaseEngines.mockResolvedValue(engines);
    api.listDatabases.mockResolvedValue([{ ...shop, access: { dbUser: 'shop_ro' }, user: 'shop_ro' }]);
    render(
      <SWRConfig value={{ provider: () => new Map() }}>
        <PanelProvider>
          <MemoryRouter>
            <DatabasesPage />
          </MemoryRouter>
        </PanelProvider>
      </SWRConfig>,
    );
    expect(await screen.findByRole('link', { name: 'Shop' })).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: /Create database/ })).toBeNull());
  });
});
