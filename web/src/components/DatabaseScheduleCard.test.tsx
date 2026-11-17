// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SWRConfig } from 'swr';
import { App } from 'antd';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, DatabaseScheduleState } from '../data/types';
import { DatabaseScheduleCard } from './DatabaseScheduleCard';

const api = vi.hoisted(() => ({
  databaseLogicalDatabases: vi.fn(),
  listBackupDestinations: vi.fn(),
  getDatabaseSchedule: vi.fn(),
  saveDatabaseSchedule: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const shop = { deleted: false, engine: 'postgres', id: 1, job: '', status: 'running' } as Database;

function state(over: Partial<DatabaseScheduleState> = {}): DatabaseScheduleState {
  return {
    all: true,
    databases: [],
    days: ['mon', 'thu'],
    enabled: false,
    lastError: '',
    lastRunAt: null,
    localMode: 'simple',
    localKeepCount: 7,
    localKeepDays: null,
    localGfsDays: null,
    localGfsWeeks: null,
    localGfsMonths: null,
    localGfsYears: null,
    minute: 150,
    next: null,
    remoteMode: 'simple',
    remoteKeepCount: 30,
    remoteKeepDays: null,
    remoteGfsDays: null,
    remoteGfsWeeks: null,
    remoteGfsMonths: null,
    remoteGfsYears: null,
    destinationId: null,
    timeZone: 'UTC (UTC+00:00)',
    ...over,
  };
}

function renderCard() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseScheduleCard db={shop} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseScheduleCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getDatabaseSchedule.mockResolvedValue(state());
    api.saveDatabaseSchedule.mockResolvedValue(state({ enabled: true }));
    api.databaseLogicalDatabases.mockResolvedValue({ databases: ['a', 'app'] });
    api.listBackupDestinations.mockResolvedValue([
      { id: 1, lastTestAt: null, lastTestError: '', lastTestOk: false, name: 'minio', s3: {}, type: 's3' },
    ]);
  });

  it('saves the schedule it shows, with the days, the time and the local keep rules', async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole('switch', { name: 'Scheduled backups' }));
    await user.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() =>
      expect(api.saveDatabaseSchedule).toHaveBeenCalledWith(1, {
        all: true,
        databases: [],
        days: ['mon', 'thu'],
        enabled: true,
        localMode: 'simple',
        localKeepCount: 7,
        localKeepDays: null,
        localGfsDays: null,
        localGfsWeeks: null,
        localGfsMonths: null,
        localGfsYears: null,
        minute: 150,
        remoteMode: 'simple',
        remoteKeepCount: 30,
        remoteKeepDays: null,
        remoteGfsDays: null,
        remoteGfsWeeks: null,
        remoteGfsMonths: null,
        remoteGfsYears: null,
        destinationId: null,
      }),
    );
    expect(screen.getByText('Panel time zone: UTC (UTC+00:00)')).toBeTruthy();
  });

  it('sends null for a keep field that is cleared', async () => {
    const user = userEvent.setup();
    renderCard();
    const field = await screen.findByRole('spinbutton', { name: 'Last N backups' });
    await user.clear(field);
    await user.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() => expect(api.saveDatabaseSchedule).toHaveBeenCalled());
    expect(api.saveDatabaseSchedule.mock.calls[0][1].localKeepCount).toBeNull();
  });

  it('shows the last error as a warning and the next run', async () => {
    api.getDatabaseSchedule.mockResolvedValue(
      state({ enabled: true, lastError: 'skipped: the Database was not running', next: '2026-11-16T03:00:00Z' }),
    );
    renderCard();
    expect(await screen.findByText('skipped: the Database was not running')).toBeTruthy();
    expect(screen.getByText(/Next run/)).toBeTruthy();
  });

  it('shows the destination keep fields only when a destination is chosen, and saves them', async () => {
    api.getDatabaseSchedule.mockResolvedValue(state({ destinationId: 1, remoteKeepCount: 30 }));
    const user = userEvent.setup();
    renderCard();
    const field = await screen.findByRole('spinbutton', { name: 'Destination: last N backups' });
    expect((field as HTMLInputElement).value).toBe('30');
    await user.clear(field);
    await user.type(field, '12');
    await user.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() => expect(api.saveDatabaseSchedule).toHaveBeenCalled());
    const sent = api.saveDatabaseSchedule.mock.calls[0][1];
    expect(sent.destinationId).toBe(1);
    expect(sent.remoteKeepCount).toBe(12);
    expect(sent.remoteKeepDays).toBeNull();
  });

  it('hides the destination keep fields when copying to None', async () => {
    renderCard();
    await screen.findByRole('button', { name: 'Save schedule' });
    expect(screen.queryByRole('spinbutton', { name: 'Destination: last N backups' })).toBeNull();
    expect(screen.getByText('Copy to')).toBeTruthy();
  });

  it('shows a GFS schedule and switching to Simple clears the tiers', async () => {
    api.getDatabaseSchedule.mockResolvedValue(state({ localGfsDays: 7, localKeepCount: 3, localMode: 'gfs' }));
    const user = userEvent.setup();
    renderCard();
    const gfs = await screen.findByRole('radio', { name: 'GFS' });
    expect((gfs as HTMLInputElement).checked).toBe(true);
    const daily = screen.getByRole('spinbutton', { name: 'Daily for D days' });
    expect((daily as HTMLInputElement).value).toBe('7');
    await user.click(screen.getByRole('radio', { name: 'Simple' }));
    expect(screen.queryByRole('spinbutton', { name: 'Daily for D days' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() => expect(api.saveDatabaseSchedule).toHaveBeenCalled());
    const sent = api.saveDatabaseSchedule.mock.calls[0][1];
    expect(sent.localMode).toBe('simple');
    expect(sent.localGfsDays).toBeNull();
  });

  it('says a blank GFS field keeps nothing, not "no limit"', async () => {
    api.getDatabaseSchedule.mockResolvedValue(state({ localKeepCount: 3, localMode: 'gfs' }));
    renderCard();
    const daily = await screen.findByRole('spinbutton', { name: 'Daily for D days' });
    expect((daily as HTMLInputElement).placeholder).toBe('Keep none');
    expect((screen.getByRole('spinbutton', { name: 'Last N backups' }) as HTMLInputElement).placeholder).toBe('Keep none');
  });

  it('saves a GFS set with its tiers and no keep-days', async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole('radio', { name: 'GFS' }));
    const daily = screen.getByRole('spinbutton', { name: 'Daily for D days' });
    await user.clear(daily);
    await user.type(daily, '7');
    await user.click(screen.getByRole('button', { name: 'Save schedule' }));
    await waitFor(() => expect(api.saveDatabaseSchedule).toHaveBeenCalled());
    const sent = api.saveDatabaseSchedule.mock.calls[0][1];
    expect(sent.localMode).toBe('gfs');
    expect(sent.localGfsDays).toBe(7);
    expect(sent.localKeepDays).toBeNull();
  });

  it('disables an SFTP destination that has no trusted host key yet', async () => {
    const sftp = { auth: 'key', host: 'box', path: '', port: 22, publicKey: '', sshKeyId: 5, sshKeyName: 'k', user: 'u' };
    api.listBackupDestinations.mockResolvedValue([
      { id: 2, lastTestAt: null, lastTestError: '', lastTestOk: false, name: 'fresh', sftp: { ...sftp, hostKey: '' }, type: 'sftp' },
      { id: 3, lastTestAt: null, lastTestError: '', lastTestOk: true, name: 'trusted', sftp: { ...sftp, hostKey: 'ssh-ed25519 SHA256:x' }, type: 'sftp' },
    ]);
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole('combobox', { name: 'Copy to' }));
    const fresh = await screen.findByTitle('fresh (Test this destination first)');
    expect(fresh.getAttribute('aria-disabled')).toBe('true');
    expect(screen.getByTitle('trusted').getAttribute('aria-disabled')).not.toBe('true');
  });
});
