// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { ComposeSource } from '../data/types';
import { SourceModal } from './SourceModal';

const api = vi.hoisted(() => ({
  deleteSource: vi.fn(),
  regenerateKey: vi.fn(),
  resetKnownHosts: vi.fn(),
  saveSource: vi.fn(),
  testSource: vi.fn(),
  uploadSource: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const location = { composeId: 7, environmentId: 2, projectId: 1 };
const URL = 'git@github.com:acme/shop.git';

const saved: ComposeSource = {
  branch: '',
  headSha: '',
  kind: 'git',
  lastTest: null,
  publicKey: 'ssh-ed25519 AAAA relist-compose-7',
  repoUrl: URL,
  submodules: false,
  zip: null,
};

function renderModal(source: ComposeSource | null = null, onSaved = vi.fn()) {
  render(<SourceModal location={location} onClose={() => undefined} onRemoved={() => undefined} onSaved={onSaved} open source={source} />);
  return onSaved;
}

beforeEach(() => {
  vi.resetAllMocks();
});

describe('SourceModal', () => {
  it('saves a valid SSH URL, then shows the public key and the hint', async () => {
    api.saveSource.mockResolvedValue(saved);
    const user = userEvent.setup();
    renderModal();

    await user.type(screen.getByLabelText('SSH URL'), URL);
    await user.tab();

    expect(await screen.findByText('ssh-ed25519 AAAA relist-compose-7')).toBeTruthy();
    expect(api.saveSource).toHaveBeenCalledWith(location, { branch: '', kind: 'git', repoUrl: URL, submodules: false });
    expect(screen.getByText('Repository → Settings → Deploy keys → Add deploy key, leave write access off')).toBeTruthy();
    expect(screen.getByRole('button', { name: /copy/i })).toBeTruthy();
  });

  it('fills the Branch select from the test and saves the chosen branch', async () => {
    api.testSource.mockResolvedValue({ branches: ['main', 'dev'], error: '', ok: true });
    api.saveSource.mockResolvedValue({ ...saved, branch: 'dev' });
    const onSaved = vi.fn();
    const user = userEvent.setup();
    renderModal(saved, onSaved);

    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    await waitFor(() => expect(api.testSource).toHaveBeenCalled());
    await user.click(await screen.findByRole('combobox', { name: 'Branch' }));
    expect(await screen.findByTitle('dev')).toBeTruthy();
    await user.click(screen.getByTitle('dev'));
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(api.saveSource).toHaveBeenLastCalledWith(location, { branch: 'dev', kind: 'git', repoUrl: URL, submodules: false });
    expect(onSaved.mock.calls[0][1]).toBe(false);
  });

  it('reports a first save as firstTime', async () => {
    api.testSource.mockResolvedValue({ branches: ['main'], error: '', ok: true });
    api.saveSource.mockResolvedValue({ ...saved, branch: 'main' });
    const user = userEvent.setup();
    const onSaved = renderModal(null);

    await user.type(screen.getByLabelText('SSH URL'), URL);
    await user.tab();
    await user.click(await screen.findByRole('button', { name: 'Test connection' }));
    await user.click(await screen.findByRole('button', { name: 'Save' }));

    await waitFor(() => expect(onSaved.mock.calls.some((c) => c[1] === true)).toBe(true));
  });

  it('shows the error text when the test fails', async () => {
    api.testSource.mockResolvedValue({ branches: [], error: 'Permission denied (publickey)', ok: false });
    const user = userEvent.setup();
    renderModal(saved);

    await user.click(screen.getByRole('button', { name: 'Test connection' }));

    expect(await screen.findByText('Permission denied (publickey)')).toBeTruthy();
  });

  it('uploads a zip and shows what it replaces', async () => {
    api.uploadSource.mockResolvedValue({ ...saved, kind: 'zip', repoUrl: '' });
    const user = userEvent.setup();
    const onSaved = renderModal(null);

    await user.click(screen.getByText('Upload zip'));
    expect(screen.getByText('Replaces all code in the Source')).toBeTruthy();
    const file = new File(['zip'], 'shop.zip', { type: 'application/zip' });
    await user.upload(document.querySelector('input[type="file"]') as HTMLInputElement, file);

    await waitFor(() => expect(api.uploadSource).toHaveBeenCalledWith(location, file));
    await waitFor(() => expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ kind: 'zip' }), true));
  });

  it('shows a 409 from the API in an Alert inside the modal', async () => {
    api.uploadSource.mockRejectedValue(new ApiError(409, 'A deployment is running'));
    const user = userEvent.setup();
    renderModal(null);

    await user.click(screen.getByText('Upload zip'));
    await user.upload(
      document.querySelector('input[type="file"]') as HTMLInputElement,
      new File(['zip'], 'shop.zip', { type: 'application/zip' }),
    );

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('A deployment is running');
    expect(alert.closest('.ant-modal')).not.toBeNull();
  });
});
