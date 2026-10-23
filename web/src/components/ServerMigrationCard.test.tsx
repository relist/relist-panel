// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ServerMigrationCard } from './ServerMigrationCard';

// The card makes no API call: an empty api makes any call throw.
vi.mock('../data/api', () => ({ api: {} }));

afterEach(cleanup);

function show() {
  return render(
    <App>
      <ServerMigrationCard />
    </App>,
  );
}

describe('ServerMigrationCard', () => {
  it('names the command for each server', () => {
    show();
    expect(screen.getByText('docker compose exec panel relist-panel migration serve')).toBeTruthy();
    expect(screen.getByText('docker compose exec panel relist-panel migration pull')).toBeTruthy();
  });

  it('offers no token from the web', () => {
    show();
    expect(screen.queryByRole('button', { name: 'Create migration token' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Revoke' })).toBeNull();
  });
});
