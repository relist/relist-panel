// @vitest-environment jsdom
import '../test/dom-setup';
import { cleanup, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { SignInPage } from './AuthPages';

vi.mock('../data/store', () => ({
  usePanel: () => ({
    auth: { checked: true, authenticated: false },
    isPending: () => false,
    signIn: vi.fn(),
  }),
}));

afterEach(cleanup);

describe('SignInPage', () => {
  // The admin comes from relist-panel create-admin: the web never offers to make one.
  it('offers no way to create the admin account', async () => {
    render(
      <App>
        <MemoryRouter>
          <SignInPage />
        </MemoryRouter>
      </App>,
    );
    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeTruthy();
    expect(screen.queryByText('First run?')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Create the admin account' })).toBeNull();
  });
});
