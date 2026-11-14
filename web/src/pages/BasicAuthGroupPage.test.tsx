// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AuthGroupDetail } from '../data/types';
import { BasicAuthGroupPage } from './BasicAuthGroupPage';

const api = vi.hoisted(() => ({
  addAuthGroupUser: vi.fn(),
  deleteAuthGroupUser: vi.fn(),
  getAuthGroup: vi.fn(),
  renameAuthGroup: vi.fn(),
  revealAuthGroupPassword: vi.fn(),
  updateAuthGroupUser: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const group: AuthGroupDetail = {
  createdAt: '2026-01-02T03:04:05Z',
  domainCount: 1,
  domains: [
    {
      composeId: 3,
      composeName: 'web',
      environmentId: 2,
      environmentName: 'prod',
      host: 'a.example.com',
      id: 9,
      path: '/api',
      projectId: 1,
      projectName: 'Shop',
    },
  ],
  id: 5,
  name: 'Staging',
  updatedAt: '2026-01-02T03:04:05Z',
  userCount: 1,
  users: [{ createdAt: '2026-01-02T03:04:05Z', id: 7, updatedAt: '2026-01-02T03:04:05Z', username: 'alice' }],
};

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter initialEntries={['/basic-auth/5']}>
        <Routes>
          <Route element={<BasicAuthGroupPage />} path="/basic-auth/:groupId" />
        </Routes>
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('BasicAuthGroupPage', () => {
  it('masks passwords until the eye is clicked', async () => {
    api.getAuthGroup.mockResolvedValue(group);
    api.revealAuthGroupPassword.mockResolvedValue('s3cret-value');
    const user = userEvent.setup();
    show();

    expect(await screen.findByText('alice')).toBeTruthy();
    expect(screen.getByText('••••••••')).toBeTruthy();
    expect(api.revealAuthGroupPassword).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: 'Show password of alice' }));

    expect(await screen.findByText('s3cret-value')).toBeTruthy();
    expect(api.revealAuthGroupPassword).toHaveBeenCalledWith(5, 7);
  });

  it('adds a user with a generated password', async () => {
    api.getAuthGroup.mockResolvedValue(group);
    api.addAuthGroupUser.mockResolvedValue(group.users[0]);
    const user = userEvent.setup();
    show();
    await screen.findByText('alice');

    await user.click(screen.getByRole('button', { name: /add user/i }));
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Username'), 'bob');
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }));
    await user.click(within(dialog).getByRole('button', { name: 'Add' }));

    await waitFor(() => expect(api.addAuthGroupUser).toHaveBeenCalled());
    const [id, input] = api.addAuthGroupUser.mock.calls[0] as [number, { username: string; password: string }];
    expect(id).toBe(5);
    expect(input.username).toBe('bob');
    expect(input.password).toMatch(/^[A-Za-z0-9]{24}$/);
  });

  it('edits a username without sending a password', async () => {
    api.getAuthGroup.mockResolvedValue(group);
    api.updateAuthGroupUser.mockResolvedValue(group.users[0]);
    const user = userEvent.setup();
    show();
    await screen.findByText('alice');

    await user.click(screen.getByRole('button', { name: 'Edit alice' }));
    const dialog = await screen.findByRole('dialog');
    const name = within(dialog).getByLabelText('Username');
    await user.clear(name);
    await user.type(name, 'al');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(api.updateAuthGroupUser).toHaveBeenCalledWith(5, 7, { username: 'al' }));
    expect(Object.keys(api.updateAuthGroupUser.mock.calls[0][2])).toEqual(['username']);
  });

  it('lists the domains that use the group, linked to their Domains tab', async () => {
    api.getAuthGroup.mockResolvedValue(group);
    show();

    const link = await screen.findByRole('link', { name: 'a.example.com/api' });
    expect(link.getAttribute('href')).toBe('/projects/1/env/2/compose/3/domains');
    expect(screen.getByText('Shop / prod / web')).toBeTruthy();
  });

  it('says so when no domain uses the group', async () => {
    api.getAuthGroup.mockResolvedValue({ ...group, domainCount: 0, domains: [] });
    show();

    expect(await screen.findByText('No domain uses this group yet.')).toBeTruthy();
  });

  it('renames the group inline', async () => {
    api.getAuthGroup.mockResolvedValue(group);
    api.renameAuthGroup.mockResolvedValue({ ...group, name: 'Prod' });
    const user = userEvent.setup();
    show();
    await screen.findByText('alice');

    await user.click(screen.getByRole('button', { name: 'Edit' }));
    const [input] = await screen.findAllByDisplayValue('Staging');
    await user.clear(input);
    await user.type(input, 'Prod');
    await user.tab();

    await waitFor(() => expect(api.renameAuthGroup).toHaveBeenCalledWith(5, 'Prod'));
  });
});
