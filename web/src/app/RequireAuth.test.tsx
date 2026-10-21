// @vitest-environment jsdom
import '../test/dom-setup';
import { act, cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { RequireAuth } from './RequireAuth';

const mocks = vi.hoisted(() => ({ migrationStatus: vi.fn() }));
vi.mock('../data/api', () => ({ api: mocks }));
vi.mock('../data/store', () => ({
  usePanel: () => ({
    auth: { checked: true, setupRequired: false, authenticated: true },
    isPending: () => false,
    loadAuthStatus: vi.fn(),
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
});
