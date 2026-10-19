// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { MigrationProgress } from '../data/migration';
import { MigrationPage } from './MigrationPage';

const mocks = vi.hoisted(() => ({
  migrationProgress: vi.fn(),
  migrationConnect: vi.fn(),
  migrationRecheck: vi.fn(),
  migrationStart: vi.fn(),
  migrationRetry: vi.fn(),
  migrationAbort: vi.fn(),
}));
vi.mock('../data/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../data/api')>();
  return { ...original, api: mocks };
});

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  vi.useRealTimers();
});

const connected: MigrationProgress = {
  phase: 'connected',
  sourceUrl: 'https://old.example.com',
  lockStep: '',
  selected: ['/srv/up'],
  items: [],
  checks: [],
  warnings: [],
  info: {
    protocol: 1,
    schemaVersion: 18,
    dataDir: '/var/lib/relist-panel',
    hostPaths: [
      { path: '/srv/up', system: false, size: 2048, composes: ['store'] },
      { path: '/etc/localtime', system: true, size: 0, composes: ['store'] },
    ],
    volumes: [],
    images: [],
    dataDirSize: 100,
    databaseSize: 10,
    warnings: [],
  },
};

function SignIn() {
  const location = useLocation();
  return <p>sign in from {(location.state as { from?: string } | null)?.from}</p>;
}

function show() {
  return render(
    <MemoryRouter initialEntries={['/migration']}>
      <App>
        <Routes>
          <Route element={<MigrationPage />} path="/migration" />
          <Route element={<SignIn />} path="/signin" />
        </Routes>
      </App>
    </MemoryRouter>,
  );
}

describe('MigrationPage', () => {
  it('connects with the URL and token', async () => {
    mocks.migrationProgress.mockResolvedValue({ ...connected, phase: '', info: null, selected: [] });
    mocks.migrationConnect.mockResolvedValue(connected);
    show();
    fireEvent.change(await screen.findByLabelText('Old panel URL'), { target: { value: 'https://old.example.com' } });
    fireEvent.change(screen.getByLabelText('Migration token'), { target: { value: 'tok' } });
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await waitFor(() => expect(mocks.migrationConnect).toHaveBeenCalledWith('https://old.example.com', 'tok'));
  });

  it('keeps Start disabled while a check blocks, and leaves system paths unticked', async () => {
    mocks.migrationProgress.mockResolvedValue({
      ...connected,
      checks: [{ blocking: true, message: 'RELIST_DATA_DIR differs: set it to /srv/relist on this host' }],
    });
    show();
    expect(await screen.findByText('RELIST_DATA_DIR differs: set it to /srv/relist on this host')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Start migration' }).hasAttribute('disabled')).toBe(true);
    const boxes = screen.getAllByRole('checkbox') as HTMLInputElement[];
    const byPath = (p: string) => boxes.find((b) => b.closest('tr')?.textContent?.includes(p));
    expect(byPath('/srv/up')?.checked).toBe(true);
    expect(byPath('/etc/localtime')?.checked).toBe(false);
  });

  it('keeps polling through network errors while the panel restarts', async () => {
    mocks.migrationProgress
      .mockResolvedValueOnce({ ...connected, phase: 'finalizing' })
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValue({ ...connected, phase: 'done', summary: { deployments: [], warnings: [], dns: ['shop.example.com'] } });
    show();
    expect(await screen.findByText(/Restarting the panel/)).toBeTruthy();
    expect(await screen.findByText('shop.example.com', {}, { timeout: 8000 })).toBeTruthy();
  }, 15000);

  it('sends a signed-out visitor to sign in and back', async () => {
    mocks.migrationProgress.mockRejectedValue(new ApiError(401, 'authentication required'));
    show();
    expect(await screen.findByText('sign in from /migration')).toBeTruthy();
  });
});

describe('MigrationPage edge states', () => {
  it('tells how to bring the panel back if it does not restart', async () => {
    mocks.migrationProgress.mockResolvedValue({ ...connected, phase: 'finalizing' });
    show();
    expect(await screen.findByText(/docker compose up -d/)).toBeTruthy();
  });

  it('retries a preflight that failed after the token was claimed', async () => {
    mocks.migrationProgress.mockResolvedValue({ ...connected, info: null, selected: [], error: 'source busy' });
    mocks.migrationConnect.mockResolvedValue(connected);
    show();
    expect(await screen.findByText('source busy')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Start migration' }).hasAttribute('disabled')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mocks.migrationConnect).toHaveBeenCalledWith('https://old.example.com', ''));
  });
});

describe('MigrationPage while locking', () => {
  it('offers Retry when locking stopped on an error', async () => {
    mocks.migrationProgress.mockResolvedValue({ ...connected, phase: 'locking', error: 'lock the source: invalid migration session' });
    mocks.migrationRetry.mockResolvedValue({ ...connected, phase: 'locking' });
    show();
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mocks.migrationRetry).toHaveBeenCalled());
  });
});
