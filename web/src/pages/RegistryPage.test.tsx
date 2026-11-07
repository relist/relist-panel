// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { ExternalRegistry } from '../data/types';
import { RegistryPage } from './RegistryPage';

const api = vi.hoisted(() => ({
  createExternalRegistry: vi.fn(),
  deleteExternalRegistry: vi.fn(),
  listExternalRegistries: vi.fn(),
  testExternalRegistry: vi.fn(),
  updateExternalRegistry: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const ghcr: ExternalRegistry = {
  host: 'ghcr.io',
  id: 1,
  lastTestAt: '2026-11-06T00:00:00Z',
  lastTestError: '',
  lastTestOk: true,
  username: 'ci',
};
const hub: ExternalRegistry = {
  host: 'docker.io',
  id: 2,
  lastTestAt: '2026-11-06T00:00:00Z',
  lastTestError: 'unauthorized: incorrect username or password',
  lastTestOk: false,
  username: 'me',
};

function show(path = '/registry') {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter initialEntries={[path]}>
        <RegistryPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listExternalRegistries.mockResolvedValue([ghcr, hub]);
});

describe('RegistryPage External registries', () => {
  it('lists host, username and the last Test result', async () => {
    show();
    const row = (await screen.findByText('ghcr.io')).closest('tr')!;
    expect(within(row).getByText('ci')).toBeTruthy();
    expect(within(row).getByText('Passed')).toBeTruthy();
    const failed = screen.getByText('docker.io').closest('tr')!;
    expect(within(failed).getByText('Failed')).toBeTruthy();
  });

  it('adds a registry with the pull-only hint on the form', async () => {
    api.createExternalRegistry.mockResolvedValue(ghcr);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getAllByRole('button', { name: /Add registry/ })[0]);
    expect(screen.getByText('Use a token that can only pull, such as a GitHub PAT with read:packages.')).toBeTruthy();
    await user.type(screen.getByLabelText('Host'), 'quay.io');
    await user.type(screen.getByLabelText('Username'), 'bot');
    await user.type(screen.getByLabelText('Token or password'), 'tok');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.createExternalRegistry).toHaveBeenCalledWith({ host: 'quay.io', secret: 'tok', username: 'bot' }));
  });

  it('puts a duplicate host error under the Host field', async () => {
    api.createExternalRegistry.mockRejectedValue(new ApiError(409, 'an External registry for this host already exists'));
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getAllByRole('button', { name: /Add registry/ })[0]);
    await user.type(screen.getByLabelText('Host'), 'ghcr.io');
    await user.type(screen.getByLabelText('Username'), 'bot');
    await user.type(screen.getByLabelText('Token or password'), 'tok');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('an External registry for this host already exists')).toBeTruthy();
  });

  it('edits without showing the stored secret and keeps it when left empty', async () => {
    api.updateExternalRegistry.mockResolvedValue(ghcr);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Edit ghcr.io' }));
    const secret = screen.getByLabelText('Token or password') as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.placeholder).toBe('Unchanged');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.updateExternalRegistry).toHaveBeenCalledWith(1, { host: 'ghcr.io', secret: '', username: 'ci' }));
  });

  it('tests a login and shows why it failed', async () => {
    api.testExternalRegistry.mockResolvedValue({ error: 'denied: denied', ok: false });
    api.listExternalRegistries
      .mockResolvedValueOnce([ghcr, hub])
      .mockResolvedValue([{ ...ghcr, lastTestError: 'denied: denied', lastTestOk: false }, hub]);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Test ghcr.io' }));
    await waitFor(() => expect(api.testExternalRegistry).toHaveBeenCalledWith(1));
    expect(await screen.findByText('denied: denied')).toBeTruthy();
  });

  it('deletes a registry after confirming', async () => {
    api.deleteExternalRegistry.mockResolvedValue(undefined);
    const user = userEvent.setup();
    show();
    await screen.findByText('ghcr.io');
    await user.click(screen.getByRole('button', { name: 'Delete ghcr.io' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.deleteExternalRegistry).toHaveBeenCalledWith(1));
  });

  it('shows an empty state with an Add registry button', async () => {
    api.listExternalRegistries.mockResolvedValue([]);
    show();
    expect(await screen.findByText('No External registries yet')).toBeTruthy();
    expect(screen.getAllByRole('button', { name: /Add registry/ }).length).toBe(2);
  });
});
