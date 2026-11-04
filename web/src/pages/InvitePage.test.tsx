// @vitest-environment jsdom
import '../test/dom-setup';

import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import { InvitePage } from './InvitePage';

const api = vi.hoisted(() => ({ acceptInvite: vi.fn(), getInvite: vi.fn() }));
const loadAuthStatus = vi.hoisted(() => vi.fn());

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));
vi.mock('../data/store', () => ({ usePanel: () => ({ loadAuthStatus }) }));

function renderInvite() {
  render(
    <App>
      <MemoryRouter initialEntries={['/invite/tok']}>
        <Routes>
          <Route element={<InvitePage />} path="/invite/:token" />
          <Route element={<div>Overview screen</div>} path="/" />
          <Route element={<div>Sign-in screen</div>} path="/signin" />
          <Route element={<div>Account screen</div>} path="/account" />
        </Routes>
      </MemoryRouter>
    </App>,
  );
}

async function fillPasswords(first: string, second: string) {
  await userEvent.type(await screen.findByLabelText('New password'), first);
  await userEvent.type(screen.getByLabelText('Confirm password'), second);
  await userEvent.click(screen.getByRole('button', { name: 'Set password' }));
}

afterEach(cleanup);

describe('InvitePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getInvite.mockResolvedValue({ email: 'bob@example.com', kind: 'invite' });
    loadAuthStatus.mockResolvedValue(undefined);
  });

  it('shows the invite email and title', async () => {
    renderInvite();
    expect(await screen.findByText('Set your password')).toBeTruthy();
    expect((screen.getByLabelText('Email') as HTMLInputElement).value).toBe('bob@example.com');
    expect(api.getInvite).toHaveBeenCalledWith('tok');
  });

  it('shows the reset title', async () => {
    api.getInvite.mockResolvedValue({ email: 'bob@example.com', kind: 'reset' });
    renderInvite();
    expect(await screen.findByText('Reset your password')).toBeTruthy();
  });

  it('shows a mismatch without calling the API', async () => {
    renderInvite();
    await fillPasswords('a brand new password', 'something else here');
    expect(await screen.findByText('Passwords do not match')).toBeTruthy();
    expect(api.acceptInvite).not.toHaveBeenCalled();
  });

  it('shows the server message on 400', async () => {
    api.acceptInvite.mockRejectedValue(new ApiError(400, 'password must be at least 8 characters'));
    renderInvite();
    await fillPasswords('shortpw!', 'shortpw!');
    expect(await screen.findByText('password must be at least 8 characters')).toBeTruthy();
  });

  it('signs an admin in and opens the Overview', async () => {
    api.acceptInvite.mockResolvedValue({ authenticated: true, email: 'bob@example.com', isAdmin: true });
    renderInvite();
    await fillPasswords('a brand new password', 'a brand new password');
    await waitFor(() => expect(screen.getByText('Overview screen')).toBeTruthy());
    expect(api.acceptInvite).toHaveBeenCalledWith('tok', 'a brand new password');
    expect(loadAuthStatus).toHaveBeenCalled();
  });

  it('signs a member in and opens their Account', async () => {
    api.acceptInvite.mockResolvedValue({ authenticated: true, email: 'bob@example.com', isAdmin: false });
    renderInvite();
    await fillPasswords('a brand new password', 'a brand new password');
    await waitFor(() => expect(screen.getByText('Account screen')).toBeTruthy());
  });

  it('shows the invalid link result on 404', async () => {
    api.getInvite.mockRejectedValue(new ApiError(404, 'This link is no longer valid'));
    renderInvite();
    expect(await screen.findByText('This link is no longer valid')).toBeTruthy();
    expect(screen.getByText('Ask an admin for a new link.')).toBeTruthy();
    await userEvent.click(screen.getByRole('button', { name: 'Go to sign in' }));
    expect(await screen.findByText('Sign-in screen')).toBeTruthy();
  });

  it('shows the invalid link result when the link dies before submit', async () => {
    api.acceptInvite.mockRejectedValue(new ApiError(404, 'This link is no longer valid'));
    renderInvite();
    await fillPasswords('a brand new password', 'a brand new password');
    expect(await screen.findByText('Ask an admin for a new link.')).toBeTruthy();
  });
});
