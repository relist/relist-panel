// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { SshKey } from '../data/types';
import { SshKeysPage } from './SshKeysPage';

const api = vi.hoisted(() => ({
  createSshKey: vi.fn(),
  deleteSshKey: vi.fn(),
  listSshKeys: vi.fn(),
  renameSshKey: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const DUPLICATE = 'An SSH key with this name already exists';
const FULL_KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOnlyTheBodyWXYZ relist-ci';

const unused: SshKey = { createdAt: '2026-01-02T03:04:05Z', id: 1, name: 'ci', publicKey: FULL_KEY, usedBy: [] };
const inUse: SshKey = {
  createdAt: '2026-01-03T03:04:05Z',
  id: 2,
  name: 'shared',
  publicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIShared1234 relist-shared',
  usedBy: [
    { composeId: 7, composeName: 'shop', environmentId: 2, projectId: 1, projectName: 'Acme' },
    { composeId: 8, composeName: 'blog', environmentId: 2, projectId: 1, projectName: 'Acme' },
  ],
};

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter>
        <SshKeysPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('SshKeysPage', () => {
  it('lists name, short key, usage links and date', async () => {
    api.listSshKeys.mockResolvedValue([unused, inUse]);
    show();

    expect(await screen.findByText('ci')).toBeTruthy();
    expect(screen.getByText('ssh-ed25519 AAAA…WXYZ relist-ci')).toBeTruthy();
    expect(screen.getByText('Not used')).toBeTruthy();
    const shop = screen.getByRole('link', { name: 'shop' });
    expect(shop.getAttribute('href')).toBe('/projects/1/env/2/compose/7');
    expect(screen.getByRole('link', { name: 'blog' }).getAttribute('href')).toBe('/projects/1/env/2/compose/8');
  });

  it('shows an empty state with a Generate key button', async () => {
    api.listSshKeys.mockResolvedValue([]);
    show();

    expect(await screen.findByText('No SSH keys yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /generate key/i }).length).toBeGreaterThan(1);
  });

  it('generates a key, shows it with the hint, and reloads on close', async () => {
    api.listSshKeys.mockResolvedValue([]);
    api.createSshKey.mockResolvedValue(unused);
    const user = userEvent.setup();
    show();
    await screen.findByText('No SSH keys yet');

    await user.click(screen.getAllByRole('button', { name: /generate key/i })[0]);
    await user.type(await screen.findByLabelText('Name'), 'ci');
    await user.click(screen.getByRole('button', { name: 'Generate' }));

    expect(await screen.findByText(FULL_KEY)).toBeTruthy();
    expect(api.createSshKey).toHaveBeenCalledWith('ci');
    expect(
      screen.getByText(
        'Repository → Settings → Deploy keys → Add deploy key, leave write access off. To reuse it, add the same key to each repository.',
      ),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: /copy/i })).toBeTruthy();

    const calls = api.listSshKeys.mock.calls.length;
    api.listSshKeys.mockResolvedValue([unused]);
    await user.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(api.listSshKeys.mock.calls.length).toBeGreaterThan(calls));
  });

  it('shows a duplicate name under the Name field when generating', async () => {
    api.listSshKeys.mockResolvedValue([]);
    api.createSshKey.mockRejectedValue(new ApiError(409, DUPLICATE));
    const user = userEvent.setup();
    show();
    await screen.findByText('No SSH keys yet');

    await user.click(screen.getAllByRole('button', { name: /generate key/i })[0]);
    await user.type(await screen.findByLabelText('Name'), 'ci');
    await user.click(screen.getByRole('button', { name: 'Generate' }));

    expect(await screen.findByText(DUPLICATE)).toBeTruthy();
  });

  it('renames a key from a prefilled modal and shows a 409 under the field', async () => {
    api.listSshKeys.mockResolvedValue([unused]);
    api.renameSshKey.mockRejectedValueOnce(new ApiError(409, DUPLICATE)).mockResolvedValueOnce({ ...unused, name: 'deploy' });
    const user = userEvent.setup();
    show();
    await screen.findByText('ci');

    await user.click(screen.getByRole('button', { name: /rename/i }));
    const input = (await screen.findByLabelText('Name')) as HTMLInputElement;
    expect(input.value).toBe('ci');

    await user.clear(input);
    await user.type(input, 'deploy');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(DUPLICATE)).toBeTruthy();
    expect(api.renameSshKey).toHaveBeenCalledWith(1, 'deploy');

    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.renameSshKey).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(api.listSshKeys).toHaveBeenCalledTimes(2));
  });

  it('deletes an unused key after confirmation', async () => {
    api.listSshKeys.mockResolvedValue([unused]);
    api.deleteSshKey.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show();
    await screen.findByText('ci');

    await user.click(screen.getByRole('button', { name: /delete/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByText(/can't deploy/)).toBeNull();
    await user.type(within(dialog).getByPlaceholderText('ci'), 'ci');
    await user.click(within(dialog).getByRole('button', { name: 'Delete key' }));

    await waitFor(() => expect(api.deleteSshKey).toHaveBeenCalledWith(1));
  });

  it('lists the Composes and the warning when deleting a key in use', async () => {
    api.listSshKeys.mockResolvedValue([inUse]);
    const user = userEvent.setup();
    show();
    await screen.findByText('shared');

    await user.click(screen.getByRole('button', { name: /delete/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText("These Sources will have no SSH key and can't deploy until you choose another.")).toBeTruthy();
    expect(within(dialog).getByText('shop')).toBeTruthy();
    expect(within(dialog).getByText('blog')).toBeTruthy();
  });
});
