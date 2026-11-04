// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { useEffect } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { api, ApiError } from '../data/api';
import { PanelProvider, usePanel } from '../data/store';
import { UserPage } from './UserPage';

function LoadProjectTree() {
  const { loadProjects } = usePanel();
  useEffect(() => { void loadProjects(); }, [loadProjects]);
  return null;
}

function renderPage(userId: number) {
  render(
    <App>
      <PanelProvider>
        <LoadProjectTree />
        <RouterProvider router={createMemoryRouter([{ path: '/users/:userId', element: <UserPage /> }], { initialEntries: [`/users/${userId}`] })} />
      </PanelProvider>
    </App>,
  );
}

const users = [
  { id: 1, email: 'owner@example.com', isAdmin: true, isOwner: true, status: 'active' as const, twoFactorEnabled: false, lastLoginAt: null, createdAt: '' },
  { id: 2, email: 'bob@example.com', isAdmin: false, isOwner: false, status: 'active' as const, twoFactorEnabled: false, lastLoginAt: null, createdAt: '' },
];

beforeEach(() => {
  vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: { id: 1, email: 'owner@example.com', isAdmin: true, isOwner: true }, access: null });
  vi.spyOn(api, 'listProjects').mockResolvedValue([{ createdAt: '', description: '', environmentCount: 1, environments: [], id: 1, lastUpdated: '', name: 'shop' }]);
  vi.spyOn(api, 'listEnvironments').mockResolvedValue([{ composes: [], createdAt: '', id: 11, name: 'production', projectId: 1, variables: [] }]);
  vi.spyOn(api, 'listComposes').mockResolvedValue([]);
  vi.spyOn(api, 'listUsers').mockResolvedValue(users);
});

afterEach(() => { vi.restoreAllMocks(); });

describe('UserPage', () => {
  it('saves the Grant tree', async () => {
    vi.spyOn(api, 'listUserGrants').mockResolvedValue([{ userId: 2, email: 'bob@example.com', projectId: 1, environmentId: null, role: 'viewer' }]);
    const put = vi.spyOn(api, 'putUserGrants').mockResolvedValue([]);
    renderPage(2);
    expect(await screen.findByRole('heading', { name: 'bob@example.com' })).toBeTruthy();
    await screen.findByRole('combobox', { name: 'Role on shop' });
    await userEvent.setup().click(screen.getByRole('button', { name: /Save access/ }));
    await waitFor(() => expect(put).toHaveBeenCalledWith(2, [{ projectId: 1, environmentId: null, role: 'viewer' }]));
  });

  it('shows a refused save', async () => {
    vi.spyOn(api, 'listUserGrants').mockResolvedValue([]);
    vi.spyOn(api, 'putUserGrants').mockRejectedValue(new ApiError(400, 'environment 31 does not belong to project 1'));
    renderPage(2);
    await screen.findByRole('combobox', { name: 'Role on shop' });
    await userEvent.setup().click(screen.getByRole('button', { name: /Save access/ }));
    expect(await screen.findByText('environment 31 does not belong to project 1')).toBeTruthy();
  });

  it('shows an admin the tree read-only', async () => {
    vi.spyOn(api, 'listUserGrants').mockResolvedValue([]);
    renderPage(1);
    expect(await screen.findByText('Admins see every Project.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Save access/ })).toBeNull();
  });
});
