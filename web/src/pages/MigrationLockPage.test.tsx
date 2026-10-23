// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { MigrationLockPage } from './MigrationLockPage';

// The page makes no API call: an empty api makes any call throw.
vi.mock('../data/api', () => ({ api: {} }));

afterEach(cleanup);

const base = { claimedBy: 'https://new.example.com', targetUrl: '', lockedAt: '2026-10-19T00:00:00Z', migratedAt: null, tokenExpiresAt: null };
const resume = 'docker compose exec panel relist-panel migration resume';

function show(status: Parameters<typeof MigrationLockPage>[0]['status']) {
  return render(
    <App>
      <MigrationLockPage status={status} />
    </App>,
  );
}

describe('MigrationLockPage', () => {
  it('says who is copying the locked server, names the resume command and offers no button', () => {
    show({ ...base, step: 'stopping' });
    expect(screen.getByText(/Being copied by/)).toBeTruthy();
    expect(screen.getByText(resume)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Cancel migration|Resume this server/ })).toBeNull();
  });

  it('shows where a migrated server went and names the resume command', () => {
    show({ ...base, step: 'migrated', targetUrl: 'https://new.example.com', migratedAt: '2026-10-19T01:00:00Z' });
    expect(screen.getByText(/Migrated to/)).toBeTruthy();
    expect(screen.getByText(resume)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Resume this server' })).toBeNull();
  });

  it('links only to an http or https target', () => {
    show({ ...base, step: 'migrated', targetUrl: 'javascript:alert(1)', migratedAt: '2026-10-19T01:00:00Z' });
    expect(screen.getByText('javascript:alert(1)')).toBeTruthy();
    expect(screen.queryByRole('link')).toBeNull();
  });
});
