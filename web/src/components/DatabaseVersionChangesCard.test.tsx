// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Database, VersionChange } from '../data/types';
import { DatabaseVersionChangesCard } from './DatabaseVersionChangesCard';

const api = vi.hoisted(() => ({
  listVersionChanges: vi.fn(),
  deletePreviousData: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const db = { id: 1, name: 'Shop', engine: 'postgres', version: '18' } as Database;

const done: VersionChange = {
  id: 3,
  fromVersion: '16',
  toVersion: '18',
  method: 'dump_restore',
  status: 'succeeded',
  error: '',
  log: '→ Pull postgres:18\n→ Switch to postgres:18\n',
  previousData: 'relist-db-1',
  startedAt: '2026-11-17T10:00:00Z',
  finishedAt: '2026-11-17T10:05:00Z',
};

const failed: VersionChange = {
  ...done,
  id: 2,
  status: 'failed',
  error: 'Restore legacy: the restore command exited with code 3',
  log: '✗ Restore legacy: boom\n',
  previousData: '',
};

function renderCard() {
  return render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <App>
        <DatabaseVersionChangesCard db={db} />
      </App>
    </SWRConfig>,
  );
}

describe('DatabaseVersionChangesCard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listVersionChanges.mockResolvedValue([done, failed]);
    api.deletePreviousData.mockResolvedValue(undefined);
  });

  it('renders nothing before any change', async () => {
    api.listVersionChanges.mockResolvedValue([]);
    const { container } = renderCard();
    await waitFor(() => expect(api.listVersionChanges).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });

  it('lists the changes with their status and error', async () => {
    renderCard();
    expect(await screen.findByText('Version changes')).toBeTruthy();
    expect(screen.getAllByText('16 → 18')).toHaveLength(2);
    expect(screen.getAllByText('Dump and restore')).toHaveLength(2);
    expect(screen.getByText('Succeeded')).toBeTruthy();
    expect(screen.getByText('Failed')).toBeTruthy();
    expect(screen.getByText('Restore legacy: the restore command exited with code 3')).toBeTruthy();
  });

  it('shows previous version data in its own column, not under Error', async () => {
    renderCard();
    await screen.findByText('Version changes');
    const headers = screen.getAllByRole('columnheader').map((th) => th.textContent);
    expect(headers).toContain('Previous version data');
    const cell = screen.getByText('relist-db-1').closest('td') as HTMLTableCellElement;
    expect(headers[cell.cellIndex]).toBe('Previous version data');
    expect(within(cell).getByRole('button', { name: 'Delete' })).toBeTruthy();
  });

  it('shows the log of an expanded row', async () => {
    const user = userEvent.setup();
    renderCard();
    await screen.findByText('Version changes');
    await user.click(screen.getAllByRole('button', { name: /expand row/i })[1]);
    expect(await screen.findByText(/✗ Restore legacy: boom/)).toBeTruthy();
  });

  it('deletes previous version data only after its volume name is typed', async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Deletes the 16 data kept from before the change. It cannot be undone.')).toBeTruthy();
    const ok = within(dialog).getByRole('button', { name: 'Delete previous data' }) as HTMLButtonElement;
    expect(ok.disabled).toBe(true);
    await user.type(within(dialog).getByLabelText('Type relist-db-1 to confirm'), 'relist-db-1');
    await waitFor(() => expect(ok.disabled).toBe(false));
    await user.click(ok);
    await waitFor(() => expect(api.deletePreviousData).toHaveBeenCalledWith(1, 3));
  });

  it('keeps a failed delete in the dialog', async () => {
    api.deletePreviousData.mockRejectedValue(new Error('the volume is in use'));
    const user = userEvent.setup();
    renderCard();
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Type relist-db-1 to confirm'), 'relist-db-1');
    await user.click(within(dialog).getByRole('button', { name: 'Delete previous data' }));
    expect(await within(dialog).findByText('the volume is in use')).toBeTruthy();
  });
});
