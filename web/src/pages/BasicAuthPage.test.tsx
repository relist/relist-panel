// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { AuthGroup, AuthGroupDomain } from '../data/types';
import { BasicAuthPage } from './BasicAuthPage';

const api = vi.hoisted(() => ({
  createAuthGroup: vi.fn(),
  deleteAuthGroup: vi.fn(),
  listAuthGroups: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const staging: AuthGroup = {
  createdAt: '2026-01-02T03:04:05Z',
  domainCount: 2,
  id: 1,
  name: 'Staging',
  updatedAt: '2026-01-02T03:04:05Z',
  userCount: 3,
};

const used: AuthGroupDomain = {
  composeId: 3,
  composeName: 'web',
  environmentId: 2,
  environmentName: 'prod',
  host: 'a.example.com',
  id: 9,
  path: '/',
  projectId: 1,
  projectName: 'Shop',
};

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter>
        <BasicAuthPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('BasicAuthPage', () => {
  it('lists groups with their user and domain counts', async () => {
    api.listAuthGroups.mockResolvedValue([staging]);
    show();

    expect(await screen.findByText('Staging')).toBeTruthy();
    const row = screen.getByText('Staging').closest('tr') as HTMLElement;
    expect(within(row).getByText('3')).toBeTruthy();
    expect(within(row).getByText('2')).toBeTruthy();
    expect(
      screen.getByText(
        'Traefik asks for a username and password of the group before serving a domain that uses it. Changes to users apply immediately.',
      ),
    ).toBeTruthy();
  });

  it('creates a group by name', async () => {
    api.listAuthGroups.mockResolvedValue([]);
    api.createAuthGroup.mockResolvedValue({ ...staging, domainCount: 0, userCount: 0 });
    const user = userEvent.setup();
    show();
    await screen.findByText('No groups yet');

    await user.click(screen.getAllByRole('button', { name: /new group/i })[0]);
    await user.type(await screen.findByLabelText('Group name'), 'Staging');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(api.createAuthGroup).toHaveBeenCalledWith({ name: 'Staging' }));
  });

  it('names the domains that block a delete', async () => {
    api.listAuthGroups.mockResolvedValue([staging]);
    api.deleteAuthGroup.mockRejectedValue(
      new ApiError(409, 'This group is used by a domain.', undefined, { domains: [used] }),
    );
    const user = userEvent.setup();
    show();
    await screen.findByText('Staging');

    await user.click(screen.getByRole('button', { name: 'Delete Staging' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));

    expect(await screen.findByText('a.example.com — Shop / prod / web')).toBeTruthy();
    expect(screen.getByText('This group is used by a domain.')).toBeTruthy();
  });
});
