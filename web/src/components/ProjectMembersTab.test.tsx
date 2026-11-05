// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../data/api';
import { ProjectMembersTab } from './ProjectMembersTab';

afterEach(() => { vi.restoreAllMocks(); });

const project = { id: 1, name: 'shop', environments: [{ id: 11, name: 'production' }] };

describe('ProjectMembersTab', () => {
  it('adds a member and saves the whole list in one call', async () => {
    vi.spyOn(api, 'listProjectGrants').mockResolvedValue([{ userId: 2, email: 'bob@example.com', projectId: 1, environmentId: null, role: 'viewer' }]);
    vi.spyOn(api, 'listUsers').mockResolvedValue([
      { id: 1, email: 'owner@example.com', isAdmin: true, isOwner: true, status: 'active', twoFactorEnabled: false, lastLoginAt: null, createdAt: '' },
      { id: 2, email: 'bob@example.com', isAdmin: false, isOwner: false, status: 'active', twoFactorEnabled: false, lastLoginAt: null, createdAt: '' },
      { id: 3, email: 'carol@example.com', isAdmin: false, isOwner: false, status: 'active', twoFactorEnabled: false, lastLoginAt: null, createdAt: '' },
    ]);
    const put = vi.spyOn(api, 'putProjectGrants').mockResolvedValue([]);
    const user = userEvent.setup();
    render(<App><ProjectMembersTab project={project} /></App>);
    expect(await screen.findByText('bob@example.com')).toBeTruthy();

    await user.click(screen.getByRole('combobox', { name: 'User to add' }));
    expect(screen.queryByTitle('owner@example.com')).toBeNull();
    await user.click(await screen.findByTitle('carol@example.com'));
    await user.click(screen.getByRole('button', { name: /Add member/ }));
    expect(await screen.findByRole('button', { name: 'Remove carol@example.com' })).toBeTruthy();

    await user.click(screen.getByRole('button', { name: /Save members/ }));
    await waitFor(() => expect(put).toHaveBeenCalledWith(1, [
      { userId: 2, environmentId: null, role: 'viewer' },
      { userId: 3, environmentId: null, role: 'viewer' },
    ]));
  });
});
