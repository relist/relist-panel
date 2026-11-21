// @vitest-environment jsdom
import '../test/dom-setup';

import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, DatabaseBackup } from '../data/types';
import { DatabaseRecreateModal } from './DatabaseRecreateModal';

const api = vi.hoisted(() => ({ listDatabaseBackups: vi.fn(), recreateDatabase: vi.fn() }));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const db = { engine: 'postgres', id: 1, name: 'Shop' } as Database;

const backup = (id: number, database: string): DatabaseBackup =>
  ({
    asBackedUp: 'app', database, error: '', finishedAt: '2026-11-20T10:00:00Z', id, localRemoved: false, log: '', pinned: false,
    sha256: 'x', size: 1024, startedAt: '2026-11-20T10:00:00Z', status: 'succeeded', trigger: 'manual', upload: null,
  }) as DatabaseBackup;

describe('DatabaseRecreateModal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // The second load has a new Backup, as a scheduled one taken meanwhile.
    api.listDatabaseBackups
      .mockResolvedValueOnce([backup(1, 'app'), backup(2, 'shop')])
      .mockResolvedValue([backup(1, 'app'), backup(2, 'shop'), backup(3, 'other')]);
  });

  it('keeps the Admin\'s picks when the Backups reload', async () => {
    render(
      <SWRConfig value={{ dedupingInterval: 0, focusThrottleInterval: 0, provider: () => new Map() }}>
        <App>
          <DatabaseRecreateModal db={db} onClose={() => {}} open />
        </App>
      </SWRConfig>,
    );
    const app = (await screen.findByLabelText('Restore app')) as HTMLInputElement;
    expect(app.checked).toBe(true);
    await userEvent.setup().click(app);
    expect(app.checked).toBe(false);

    act(() => {
      window.dispatchEvent(new Event('focus'));
    });
    await screen.findByLabelText('Restore other');
    expect((screen.getByLabelText('Restore app') as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText('Restore shop') as HTMLInputElement).checked).toBe(true);
  });
});
