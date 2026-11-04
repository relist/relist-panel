// @vitest-environment jsdom
import '../test/dom-setup';

import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { PanelUser } from '../data/api';
import { UsersPage } from './UsersPage';

const api = vi.hoisted(() => ({
  createUser: vi.fn(),
  deleteUser: vi.fn(),
  listUsers: vi.fn(),
  resetUser: vi.fn(),
  updateUser: vi.fn(),
}));
const panel = vi.hoisted(() => ({
  auth: { checked: true, authenticated: true, user: { id: 2, email: 'second@example.com', isAdmin: true, isOwner: false } },
  loadAuthStatus: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));
vi.mock('../data/store', () => ({ usePanel: () => panel }));

const base = { twoFactorEnabled: false, lastLoginAt: null, createdAt: '2026-11-01T00:00:00Z' };
const ownerRow: PanelUser = { ...base, id: 1, email: 'owner@example.com', isAdmin: true, isOwner: true, status: 'active', twoFactorEnabled: true };
const secondRow: PanelUser = { ...base, id: 2, email: 'second@example.com', isAdmin: true, isOwner: false, status: 'active' };
const bobRow: PanelUser = { ...base, id: 3, email: 'bob@example.com', isAdmin: false, isOwner: false, status: 'active' };
const invitedRow: PanelUser = { ...base, id: 4, email: 'new@example.com', isAdmin: false, isOwner: false, status: 'invited' };

function show() {
  return render(
    <App>
      <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
        <MemoryRouter>
          <UsersPage />
        </MemoryRouter>
      </SWRConfig>
    </App>,
  );
}

async function row(email: string) {
  return (await screen.findByText(email)).closest('tr') as HTMLElement;
}

/** Opens a row's ⋯ menu and returns the names of its items. */
async function openActions(email: string) {
  await userEvent.click(await screen.findByRole('button', { name: `Actions for ${email}` }));
  const items = await screen.findAllByRole('menuitem');
  return items.map((item) => item.textContent);
}

async function choose(name: string) {
  await userEvent.click(await screen.findByRole('menuitem', { name }));
}

afterEach(cleanup);

beforeEach(() => {
  vi.resetAllMocks();
  api.listUsers.mockResolvedValue([ownerRow, secondRow, bobRow, invitedRow]);
});

describe('UsersPage', () => {
  it('lists users with status tags and the Owner tag', async () => {
    show();
    const owner = await row('owner@example.com');
    expect(within(owner).getByText('Owner')).toBeTruthy();
    expect(within(owner).getByText('On')).toBeTruthy();
    expect(within(await row('second@example.com')).getByText('Admin')).toBeTruthy();
    expect(within(await row('bob@example.com')).getByText('Member')).toBeTruthy();
    expect(within(await row('new@example.com')).getByText('invited')).toBeTruthy();
    expect(within(await row('bob@example.com')).getByText('Never')).toBeTruthy();
  });

  it('keeps each email on one line', async () => {
    show();
    const email = await screen.findByText('owner@example.com');
    expect(email.closest('td')?.style.whiteSpace).toBe('nowrap');
  });

  it('invites and shows the copyable link once', async () => {
    api.createUser.mockResolvedValue({ user: invitedRow, token: 'tok123', expiresAt: '2026-11-11T00:00:00Z', kind: 'invite' });
    show();
    await userEvent.click(await screen.findByRole('button', { name: /Invite user/ }));
    await userEvent.type(screen.getByLabelText('Email'), 'new@example.com');
    await userEvent.click(screen.getByRole('switch', { name: 'Admin' }));
    await userEvent.click(screen.getByRole('button', { name: 'Create invite link' }));

    expect(api.createUser).toHaveBeenCalledWith('new@example.com', true);
    expect(await screen.findByText(`${window.location.origin}/invite/tok123`)).toBeTruthy();
    expect(screen.getByText(/It is shown only now\./)).toBeTruthy();
  });

  it('shows a 409 under Email', async () => {
    api.createUser.mockRejectedValue(new ApiError(409, 'A user with this email already exists'));
    show();
    await userEvent.click(await screen.findByRole('button', { name: /Invite user/ }));
    await userEvent.type(screen.getByLabelText('Email'), 'bob@example.com');
    await userEvent.click(screen.getByRole('button', { name: 'Create invite link' }));
    expect(await screen.findByText('A user with this email already exists')).toBeTruthy();
  });

  it('links each email to the user page', async () => {
    show();
    await row('bob@example.com');
    expect(screen.getByRole('link', { name: 'bob@example.com' }).getAttribute('href')).toBe('/users/3');
  });

  it('gives the Owner row no actions menu', async () => {
    show();
    await row('owner@example.com');
    expect(screen.queryByRole('button', { name: 'Actions for owner@example.com' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Actions for bob@example.com' })).toBeTruthy();
  });

  it('disables a user and reloads', async () => {
    api.updateUser.mockResolvedValue({ ...bobRow, status: 'disabled' });
    show();
    await openActions('bob@example.com');
    await choose('Disable');
    expect(api.updateUser).toHaveBeenCalledWith(3, { disabled: true });
    await waitFor(() => expect(api.listUsers).toHaveBeenCalledTimes(2));
  });

  it('shows a reset link for an active user', async () => {
    api.resetUser.mockResolvedValue({ token: 'reset1', expiresAt: '2026-11-05T00:00:00Z', kind: 'reset' });
    show();
    await openActions('bob@example.com');
    await choose('Send reset link');
    expect(api.resetUser).toHaveBeenCalledWith(3);
    expect(await screen.findByText(`${window.location.origin}/invite/reset1`)).toBeTruthy();
  });

  it('labels the link action of an invited user as a new invite', async () => {
    show();
    expect(await openActions('new@example.com')).toContain('New invite link');
  });

  it('offers no disable or reset link on your own row', async () => {
    show();
    expect(await openActions('second@example.com')).toEqual(['Remove admin', 'Delete']);
  });

  it('asks before deleting a user', async () => {
    api.deleteUser.mockResolvedValue(undefined);
    show();
    await openActions('bob@example.com');
    await choose('Delete');
    expect(api.deleteUser).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getAllByText('Delete bob@example.com?').length).toBeGreaterThan(0);
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.deleteUser).toHaveBeenCalledWith(3));
  });

  it('rechecks the sign-in after removing its own admin', async () => {
    api.updateUser.mockResolvedValue({ ...secondRow, isAdmin: false });
    show();
    await openActions('second@example.com');
    await choose('Remove admin');
    await waitFor(() => expect(panel.loadAuthStatus).toHaveBeenCalled());
  });
});
