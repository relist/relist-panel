// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ServerMigrationCard } from './ServerMigrationCard';

const mocks = vi.hoisted(() => ({
  migrationStatus: vi.fn(),
  createMigrationToken: vi.fn(),
  revokeMigrationToken: vi.fn(),
}));
vi.mock('../data/api', () => ({ api: mocks }));

const idle = { step: '', claimedBy: '', targetUrl: '', lockedAt: null, migratedAt: null, tokenExpiresAt: null };

beforeEach(() => {
  mocks.migrationStatus.mockResolvedValue(idle);
  mocks.createMigrationToken.mockResolvedValue({ token: 'secret-token-value', expiresAt: '2999-01-01T00:00:00Z' });
  mocks.revokeMigrationToken.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function show() {
  return render(
    <MemoryRouter>
      <App>
        <ServerMigrationCard />
      </App>
    </MemoryRouter>,
  );
}

describe('ServerMigrationCard', () => {
  it('creates a 24 hour token by default and shows it once', async () => {
    show();
    const create = await screen.findByRole('button', { name: 'Create migration token' });
    fireEvent.click(create);
    await waitFor(() => expect(mocks.createMigrationToken).toHaveBeenCalledWith('24h'));
    expect(await screen.findByText('secret-token-value')).toBeTruthy();
    expect(screen.getByText('Shown once. Anyone with it can copy this server.')).toBeTruthy();
  });

  it('creates a token with the picked lifetime', async () => {
    show();
    fireEvent.click(await screen.findByText('7 days'));
    fireEvent.click(screen.getByRole('button', { name: 'Create migration token' }));
    await waitFor(() => expect(mocks.createMigrationToken).toHaveBeenCalledWith('168h'));
  });

  it('revokes a live token', async () => {
    mocks.migrationStatus.mockResolvedValue({ ...idle, tokenExpiresAt: '2999-01-01T00:00:00Z' });
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(mocks.revokeMigrationToken).toHaveBeenCalled());
  });

  it('stops offering a token once it has expired unclaimed', async () => {
    mocks.createMigrationToken.mockResolvedValue({ token: 'secret-token-value', expiresAt: '2000-01-01T00:00:00Z' });
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Create migration token' }));
    expect(await screen.findByText('The token expired. Create a new one.')).toBeTruthy();
    expect(screen.queryByText('secret-token-value')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Revoke' })).toBeNull();
  });

  it('shows the terminal command', async () => {
    show();
    expect(await screen.findByText('docker compose exec panel relist-panel migration serve')).toBeTruthy();
  });

  it('offers to migrate another panel into this one', async () => {
    show();
    expect((await screen.findByRole('link', { name: 'Migrate from another panel' })).getAttribute('href')).toBe('/migration');
  });
});
