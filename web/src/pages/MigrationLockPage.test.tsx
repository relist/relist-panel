// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { MigrationLockPage } from './MigrationLockPage';

const mocks = vi.hoisted(() => ({ resumeMigration: vi.fn() }));
vi.mock('../data/api', () => ({ api: mocks }));

afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

const base = { claimedBy: 'https://new.example.com', targetUrl: '', lockedAt: '2026-10-19T00:00:00Z', migratedAt: null, tokenExpiresAt: null };

describe('MigrationLockPage', () => {
  it('says who is copying the locked server and cancels only after RESUME is typed', async () => {
    mocks.resumeMigration.mockResolvedValue(undefined);
    const onResumed = vi.fn();
    render(
      <App>
        <MigrationLockPage onResumed={onResumed} status={{ ...base, step: 'ready' }} />
      </App>,
    );
    expect(screen.getByText(/Being copied by/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel migration' }));
    const dialog = screen.getByRole('dialog');
    const ok = within(dialog).getByRole('button', { name: 'Cancel migration' });
    expect(ok.hasAttribute('disabled')).toBe(true);
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'RESUME' } });
    expect(ok.hasAttribute('disabled')).toBe(false);
    fireEvent.click(ok);
    await waitFor(() => expect(mocks.resumeMigration).toHaveBeenCalledWith('RESUME'));
    await waitFor(() => expect(onResumed).toHaveBeenCalled());
  });

  it('shows where a migrated server went and warns before resuming it', () => {
    render(
      <App>
        <MigrationLockPage
          onResumed={() => undefined}
          status={{ ...base, step: 'migrated', targetUrl: 'https://new.example.com', migratedAt: '2026-10-19T01:00:00Z' }}
        />
      </App>,
    );
    expect(screen.getByText(/Migrated to/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Resume this server' }));
    expect(screen.getByText('Data written on the new server after the migration is not here.')).toBeTruthy();
  });
});

describe('MigrationLockPage target link', () => {
  it('links only to an http or https target', () => {
    render(
      <App>
        <MigrationLockPage
          onResumed={() => undefined}
          status={{ ...base, step: 'migrated', targetUrl: 'javascript:alert(1)', migratedAt: '2026-10-19T01:00:00Z' }}
        />
      </App>,
    );
    expect(screen.getByText('javascript:alert(1)')).toBeTruthy();
    expect(screen.queryByRole('link')).toBeNull();
  });
});

describe('MigrationLockPage resume warnings', () => {
  it('unlocks and shows which Composes did not start', async () => {
    mocks.resumeMigration.mockResolvedValue({ warning: 'store did not start, deploy it again' });
    const onResumed = vi.fn();
    render(
      <App>
        <MigrationLockPage onResumed={onResumed} status={{ ...base, step: 'ready' }} />
      </App>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel migration' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'RESUME' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel migration' }));
    expect(await screen.findByText('store did not start, deploy it again')).toBeTruthy();
    expect(onResumed).toHaveBeenCalled();
  });
});
