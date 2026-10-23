// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SetupPage } from './AuthPages';

vi.mock('../data/store', () => ({
  usePanel: () => ({
    auth: { checked: true, setupRequired: true, authenticated: false },
    isPending: () => false,
    signUp: vi.fn(),
  }),
}));

afterEach(cleanup);

describe('SetupPage', () => {
  it('names the pull command instead of linking to a migration page', async () => {
    render(
      <App>
        <MemoryRouter>
          <SetupPage />
        </MemoryRouter>
      </App>,
    );
    expect(await screen.findByText('docker compose exec panel relist-panel migration pull')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Migrate from another panel' })).toBeNull();
  });
});
