// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
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
  mocks.createMigrationToken.mockResolvedValue({ token: 'secret-token-value', expiresAt: '2026-10-20T00:00:00Z' });
  mocks.revokeMigrationToken.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function show() {
  return render(
    <App>
      <ServerMigrationCard />
    </App>,
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
    mocks.migrationStatus.mockResolvedValue({ ...idle, tokenExpiresAt: '2026-10-20T00:00:00Z' });
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    await waitFor(() => expect(mocks.revokeMigrationToken).toHaveBeenCalled());
  });
});
