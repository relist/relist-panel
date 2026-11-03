// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import { TraefikPage } from './TraefikPage';

const api = vi.hoisted(() => ({
  accessLogSettings: vi.fn(),
  listAccessLogs: vi.fn(),
  saveAccessLogSettings: vi.fn(),
  traefikLogs: vi.fn(),
  traefikStatus: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter>
        <TraefikPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.traefikStatus.mockResolvedValue({ accessLogEnabled: true, found: true, trustedIps: '' });
  api.traefikLogs.mockResolvedValue({ found: true, logs: '2026-11-03T07:32:31Z 2026-11-03T07:32:31Z INF up\n' });
  api.listAccessLogs.mockResolvedValue({ files: [], totalSize: 0 });
  api.accessLogSettings.mockResolvedValue({ keepForever: false, lastError: '', lastRotatedAt: null, retentionDays: 90 });
});

describe('TraefikPage', () => {
  it('says plainly when the install has no Traefik container', async () => {
    api.traefikStatus.mockResolvedValue({ accessLogEnabled: false, found: false, trustedIps: '' });
    api.traefikLogs.mockResolvedValue({ found: false, logs: '' });
    show();
    expect(await screen.findByText('No Traefik container found in this install.')).toBeTruthy();
    expect(screen.queryByText(/`/)).toBeNull();
  });

  it('shows a failed status check instead of hiding the banner silently', async () => {
    api.traefikStatus.mockRejectedValue(new ApiError(500, 'could not inspect Traefik'));
    show();
    expect(await screen.findByText('could not inspect Traefik')).toBeTruthy();
  });

  it('keeps the days field empty when cleared and does not save it', async () => {
    const user = userEvent.setup();
    show();
    const days = await screen.findByRole('spinbutton', { name: 'Days to keep' });
    await user.clear(days);
    await user.tab();
    expect((days as HTMLInputElement).value).toBe('');
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('spins Refresh while the data reloads', async () => {
    const user = userEvent.setup();
    show();
    await screen.findByText(/INF up/);
    let release: (value: unknown) => void = () => {};
    api.traefikLogs.mockReturnValue(new Promise((resolve) => (release = resolve)));
    const refresh = screen.getByRole('button', { name: /Refresh/ });
    await user.click(refresh);
    await waitFor(() => expect(refresh.classList.contains('ant-btn-loading')).toBe(true));
    release({ found: true, logs: '' });
    await waitFor(() => expect(refresh.classList.contains('ant-btn-loading')).toBe(false));
  });
});
