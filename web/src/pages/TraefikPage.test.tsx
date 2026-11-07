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
  ackTraefikApply: vi.fn(),
  listAccessLogs: vi.fn(),
  saveAccessLogSettings: vi.fn(),
  traefikConfig: vi.fn(),
  traefikApplyStatus: vi.fn(),
  traefikConfigHistory: vi.fn(),
  traefikLogs: vi.fn(),
  traefikStatus: vi.fn(),
}));

// The editor is Monaco; nothing here edits YAML.
vi.mock('../components/YamlEditor', () => ({ YamlEditor: () => null }));
vi.mock('../components/DiffViewer', () => ({ DiffViewer: () => null }));

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
  api.traefikConfigHistory.mockResolvedValue([]);
  api.traefikConfig.mockResolvedValue({
    base: '',
    effective: '',
    form: { accessLog: false, acmeEmail: '', logLevel: 'INFO', readTimeout: '3600s', trustCloudflare: false, trustedIps: [] },
    lastApply: null,
    override: '',
    pendingRestart: false,
    revision: 'rev1',
    warnings: [],
  });
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

  it('opens the result of a Traefik apply nobody has seen yet', async () => {
    const lastApply = { acked: false, id: 'run1', kind: 'apply', reason: 'no /healthz', startedAt: '2026-11-04T10:00:00Z', state: 'rolled-back' };
    api.traefikConfig.mockResolvedValue({ ...(await api.traefikConfig()), lastApply });
    api.traefikApplyStatus.mockResolvedValue(lastApply);
    show();
    expect(await screen.findByText('no /healthz', {}, { timeout: 4000 })).toBeTruthy();
  });

  it('splits the page into Logs, Access logs and Configuration', async () => {
    show();
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Logs', 'Access logs', 'Configuration']);
  });

  it('points to Settings when the Access log is off', async () => {
    const user = userEvent.setup();
    api.traefikStatus.mockResolvedValue({ accessLogEnabled: false, found: true, trustedIps: '' });
    show();
    await user.click(screen.getByRole('tab', { name: 'Access logs' }));
    expect(await screen.findByText(/Access log is off\./)).toBeTruthy();
    expect(screen.queryByText(/run a Panel update/)).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Turn it on in Settings' }));
    expect(screen.getByRole('tab', { name: 'Configuration' }).getAttribute('aria-selected')).toBe('true');
    expect(await screen.findByRole('textbox', { name: 'ACME email' })).toBeTruthy();
  });

  it('keeps retention under 90 days without a legal warning', async () => {
    const user = userEvent.setup();
    show();
    await user.click(screen.getByRole('tab', { name: 'Access logs' }));
    const days = await screen.findByRole('spinbutton', { name: 'Days to keep' });
    await user.clear(days);
    await user.type(days, '30');
    expect(screen.queryByText(/Thai law/)).toBeNull();
  });

  it('keeps the days field empty when cleared and does not save it', async () => {
    const user = userEvent.setup();
    show();
    await user.click(screen.getByRole('tab', { name: 'Access logs' }));
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
