// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { ComposeSource, SshKey } from '../data/types';
import { SourceModal } from './SourceModal';

const api = vi.hoisted(() => ({
  deleteSource: vi.fn(),
  listSshKeys: vi.fn(),
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

const keys: SshKey[] = [
  { createdAt: '2026-10-01T00:00:00Z', id: 1, name: 'ci', publicKey: 'ssh-ed25519 AAAA relist-ci', usedBy: [] },
  { createdAt: '2026-10-01T00:00:00Z', id: 2, name: 'deploy', publicKey: 'ssh-ed25519 BBBB relist-deploy', usedBy: [] },
];

const saved: ComposeSource = {
  branch: '',
  headSha: '',
  kind: 'git',
  lastTest: null,
  sshKey: { id: 1, name: 'ci', publicKey: 'ssh-ed25519 AAAA relist-ci' },
  repoUrl: URL,
  submodules: false,
  zip: null,
};

function renderModal(source: ComposeSource | null = null, onSaved = vi.fn(), onChanged = vi.fn()) {
  render(<SourceModal location={location} onChanged={onChanged} onClose={() => undefined} onRemoved={() => undefined} onSaved={onSaved} open source={source} />);
  return onSaved;
}

/** The name the SSH key Select shows, or null when it shows its placeholder. */
function chosenKeyName(): string | null {
  const select = document.querySelector('#source-ssh-key')?.closest('.ant-select');
  return select?.querySelector('.ant-select-content-has-value')?.getAttribute('title') ?? null;
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listSshKeys.mockResolvedValue(keys);
});

describe('SourceModal', () => {
  it('lists the library keys and saves the chosen one, then shows its public key', async () => {
    api.saveSource.mockResolvedValue({ ...saved, sshKey: null });
    const user = userEvent.setup();
    renderModal({ ...saved, sshKey: null });

    await user.click(await screen.findByRole('combobox', { name: 'SSH key' }));
    expect(await screen.findByTitle('deploy')).toBeTruthy();
    await user.click(screen.getByTitle('ci'));

    await waitFor(() =>
      expect(api.saveSource).toHaveBeenCalledWith(location, { branch: '', kind: 'git', repoUrl: URL, sshKeyId: 1, submodules: false }),
    );
    expect(await screen.findByText('ssh-ed25519 AAAA relist-ci')).toBeTruthy();
    expect(screen.getByRole('button', { name: /copy/i })).toBeTruthy();
    expect(screen.getByText('Repository → Settings → Deploy keys → Add deploy key, leave write access off')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Regenerate key' })).toBeNull();
  });

  it('keeps the chosen key when the URL is edited and blurred', async () => {
    api.saveSource.mockResolvedValue(saved);
    const user = userEvent.setup();
    renderModal(saved);

    const input = screen.getByLabelText('SSH URL');
    await user.clear(input);
    await user.type(input, 'git@github.com:acme/other.git');
    await user.tab();

    await waitFor(() =>
      expect(api.saveSource).toHaveBeenCalledWith(location, { branch: '', kind: 'git', repoUrl: 'git@github.com:acme/other.git', sshKeyId: 1, submodules: false }),
    );
  });

  it('reloads the keys and clears the choice when the API says the key is unknown', async () => {
    api.saveSource.mockRejectedValue(new ApiError(400, 'unknown SSH key'));
    const user = userEvent.setup();
    renderModal({ ...saved, sshKey: null });

    await user.click(await screen.findByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('ci'));

    await waitFor(() => expect(api.listSshKeys).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('unknown SSH key')).toBeTruthy();
    expect(screen.queryByText('ssh-ed25519 AAAA relist-ci')).toBeNull();
    expect(chosenKeyName()).toBeNull();
  });

  it('drops a key deleted elsewhere when Test connection says to choose a key', async () => {
    api.testSource.mockRejectedValue(new ApiError(400, 'Choose an SSH key first'));
    const user = userEvent.setup();
    renderModal(saved);
    await waitFor(() => expect(chosenKeyName()).toBe('ci'));
    api.listSshKeys.mockResolvedValue([keys[1]]);

    await user.click(screen.getByRole('button', { name: 'Test connection' }));

    await waitFor(() => expect(api.listSshKeys).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('Choose an SSH key first')).toBeTruthy();
    await waitFor(() => expect(chosenKeyName()).toBeNull());
    await waitFor(() => expect((screen.getByRole('button', { name: /Test connection/ }) as HTMLButtonElement).disabled).toBe(true));
  });

  it('never shows a raw id when the loaded keys do not contain the chosen one', async () => {
    api.listSshKeys.mockResolvedValue([keys[1]]);
    renderModal(saved);

    await waitFor(() => expect(api.listSshKeys).toHaveBeenCalled());
    await waitFor(() => expect((screen.getByRole('button', { name: 'Test connection' }) as HTMLButtonElement).disabled).toBe(true));
    expect(chosenKeyName()).toBeNull();
    expect(screen.queryByText('1')).toBeNull();
  });

  it('shows the load error with a Reload button that recovers', async () => {
    api.listSshKeys.mockRejectedValueOnce(new Error('could not list SSH keys')).mockResolvedValueOnce(keys);
    const user = userEvent.setup();
    renderModal({ ...saved, sshKey: null });

    expect(await screen.findByText('could not list SSH keys')).toBeTruthy();
    expect(document.querySelector('.ant-select-loading')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Reload' }));

    await waitFor(() => expect(api.listSshKeys).toHaveBeenCalledTimes(2));
    await user.click(await screen.findByRole('combobox', { name: 'SSH key' }));
    expect(await screen.findByTitle('deploy')).toBeTruthy();
    expect(screen.queryByText('could not list SSH keys')).toBeNull();
  });

  it('reverts the choice when saving it fails for another reason', async () => {
    api.saveSource.mockRejectedValue(new ApiError(409, 'A deployment is running'));
    const user = userEvent.setup();
    renderModal(saved);
    await waitFor(() => expect(chosenKeyName()).toBe('ci'));

    await user.click(screen.getByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('deploy'));

    expect(await screen.findByText('A deployment is running')).toBeTruthy();
    await waitFor(() => expect(chosenKeyName()).toBe('ci'));
  });

  it('clears the shown test result once a key change is saved', async () => {
    api.testSource.mockResolvedValue({ branches: ['main', 'dev'], error: '', ok: true });
    api.saveSource.mockResolvedValue({ ...saved, sshKey: { id: 2, name: 'deploy', publicKey: 'ssh-ed25519 BBBB relist-deploy' } });
    const user = userEvent.setup();
    renderModal(saved);

    await user.click(screen.getByRole('button', { name: 'Test connection' }));
    expect(await screen.findByText('Connected')).toBeTruthy();
    await user.click(screen.getByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('deploy'));

    await waitFor(() => expect(screen.queryByText('Connected')).toBeNull());
    await user.click(screen.getByRole('combobox', { name: 'Branch' }));
    // Only the current branch is left to choose: the tested list is gone.
    await waitFor(() => expect(document.querySelectorAll('[id^="source-branch_list_"]')).toHaveLength(1));
  });

  it('points to the SSH keys page when the library is empty, and reloads on request', async () => {
    api.listSshKeys.mockResolvedValue([]);
    const user = userEvent.setup();
    renderModal({ ...saved, sshKey: null });

    expect(await screen.findByText('No SSH keys yet')).toBeTruthy();
    const link = screen.getByRole('link', { name: 'Create one in SSH keys' });
    expect(link.getAttribute('href')).toBe('/ssh-keys');
    expect(link.getAttribute('target')).toBe('_blank');

    api.listSshKeys.mockResolvedValue(keys);
    await user.click(screen.getByRole('button', { name: 'Reload' }));
    expect(api.listSshKeys).toHaveBeenCalledTimes(2);
    expect(await screen.findByRole('combobox', { name: 'SSH key' })).toBeTruthy();
  });

  it('enables Test connection only once the URL is saved and a key is chosen', async () => {
    api.saveSource.mockResolvedValue(saved);
    const user = userEvent.setup();
    renderModal({ ...saved, sshKey: null });

    const test = screen.getByRole('button', { name: 'Test connection' }) as HTMLButtonElement;
    expect(test.disabled).toBe(true);
    await user.click(await screen.findByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('ci'));
    await waitFor(() => expect(test.disabled).toBe(false));
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
    expect(api.saveSource).toHaveBeenLastCalledWith(location, { branch: 'dev', kind: 'git', repoUrl: URL, sshKeyId: 1, submodules: false });
    expect(onSaved.mock.calls[0][1]).toBe(false);
  });

  it('reports a first save as firstTime', async () => {
    api.testSource.mockResolvedValue({ branches: ['main'], error: '', ok: true });
    api.saveSource.mockResolvedValue({ ...saved, branch: 'main' });
    const user = userEvent.setup();
    const onSaved = renderModal(null);

    await user.type(screen.getByLabelText('SSH URL'), URL);
    await user.tab();
    await user.click(await screen.findByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('ci'));
    await user.click(await screen.findByRole('button', { name: 'Test connection' }));
    await waitFor(() => expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(false));
    await user.click(screen.getByRole('button', { name: 'Save' }));

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

  it('refreshes the page after a test, pass or fail', async () => {
    api.testSource.mockResolvedValue({ branches: [], error: 'Permission denied (publickey)', ok: false });
    const onChanged = vi.fn();
    const user = userEvent.setup();
    renderModal(saved, vi.fn(), onChanged);

    await user.click(screen.getByRole('button', { name: 'Test connection' }));

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
  });
});
