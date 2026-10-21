// @vitest-environment jsdom
import '../test/dom-setup';
import { act, cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import { RequireAuth } from './RequireAuth';

const mocks = vi.hoisted(() => ({ migrationStatus: vi.fn(), loadAuthStatus: vi.fn() }));
vi.mock('../data/api', async (importOriginal) => {
  const original = await importOriginal<typeof import('../data/api')>();
  return { ...original, api: { migrationStatus: mocks.migrationStatus } };
});
vi.mock('../data/store', () => ({
  usePanel: () => ({
    auth: { checked: true, setupRequired: false, authenticated: true },
    isPending: () => false,
    loadAuthStatus: mocks.loadAuthStatus,
  }),
}));

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  vi.useRealTimers();
});

const idle = { step: '', claimedBy: '', targetUrl: '', lockedAt: null, migratedAt: null, tokenExpiresAt: null };

describe('RequireAuth under a Migration lock', () => {
  it('leaves the lock page by itself once the server is resumed from the terminal', async () => {
    vi.useFakeTimers();
    mocks.migrationStatus.mockResolvedValueOnce({ ...idle, step: 'locked' }).mockResolvedValue(idle);
    render(
      <App>
        <MemoryRouter>
          <Routes>
            <Route element={<RequireAuth />}>
              <Route element={<p>the app</p>} path="/" />
            </Route>
          </Routes>
        </MemoryRouter>
      </App>,
    );
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(screen.getByText('This server is being migrated')).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.queryByText('This server is being migrated')).toBeNull();
    expect(screen.getByText('the app')).toBeTruthy();
  });

  it('keeps following the lock through a failed read', async () => {
    vi.useFakeTimers();
    mocks.migrationStatus
      .mockResolvedValueOnce({ ...idle, step: 'locked' })
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValue(idle);
    render(
      <App>
        <MemoryRouter>
          <Routes>
            <Route element={<RequireAuth />}>
              <Route element={<p>the app</p>} path="/" />
            </Route>
          </Routes>
        </MemoryRouter>
      </App>,
    );
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(screen.getByText('This server is being migrated')).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(10000));
    expect(screen.getByText('the app')).toBeTruthy();
  });

  it('stops polling and rechecks the sign-in once the session has ended', async () => {
    vi.useFakeTimers();
    mocks.loadAuthStatus.mockResolvedValue(undefined);
    mocks.migrationStatus
      .mockResolvedValueOnce({ ...idle, step: 'migrated' })
      .mockRejectedValue(new ApiError(401, 'authentication required'));
    render(
      <App>
        <MemoryRouter>
          <Routes>
            <Route element={<RequireAuth />}>
              <Route element={<p>the app</p>} path="/" />
            </Route>
          </Routes>
        </MemoryRouter>
      </App>,
    );
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(mocks.loadAuthStatus).toHaveBeenCalledTimes(1);
    await act(() => vi.advanceTimersByTimeAsync(20000));
    expect(mocks.migrationStatus).toHaveBeenCalledTimes(2);
  });
});
