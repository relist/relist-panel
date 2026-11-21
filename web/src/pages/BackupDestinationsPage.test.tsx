// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { BackupDestination, SshKey } from '../data/types';
import { BackupDestinationsPage } from './BackupDestinationsPage';

const api = vi.hoisted(() => ({
  createBackupDestination: vi.fn(),
  revealDestinationPassphrase: vi.fn(),
  deleteBackupDestination: vi.fn(),
  listBackupDestinations: vi.fn(),
  listSshKeys: vi.fn(),
  resetBackupDestinationHostKey: vi.fn(),
  testBackupDestination: vi.fn(),
  updateBackupDestination: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const minio: BackupDestination = {
  encrypt: false,
  id: 1,
  lastTestAt: null,
  lastTestError: '',
  lastTestOk: false,
  name: 'minio',
  s3: { accessKeyId: 'AKIA', bucket: 'backups', endpoint: 'http://minio:9000', pathStyle: true, prefix: 'relist', region: '' },
  type: 's3',
};

const deployKey: SshKey = {
  createdAt: '2026-10-01T00:00:00Z',
  id: 5,
  name: 'deploy',
  publicKey: 'ssh-ed25519 AAAAdeploy relist-deploy',
  usedBy: [],
  usedByDestinations: [],
};

const box: BackupDestination = {
  encrypt: false,
  id: 2,
  lastTestAt: null,
  lastTestError: '',
  lastTestOk: false,
  name: 'box',
  sftp: {
    auth: 'key',
    host: 'box.example',
    hostKey: 'ssh-ed25519 SHA256:abc123',
    path: 'b',
    port: 23,
    publicKey: 'ssh-ed25519 AAAAdeploy relist-deploy',
    sshKeyId: 5,
    sshKeyName: 'deploy',
    user: 'u',
  },
  type: 'sftp',
};

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <MemoryRouter>
        <BackupDestinationsPage />
      </MemoryRouter>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listBackupDestinations.mockResolvedValue([minio]);
  api.listSshKeys.mockResolvedValue([deployKey]);
});

describe('BackupDestinationsPage', () => {
  it('lists the destination with its endpoint, bucket and prefix', async () => {
    show();
    expect(await screen.findByText('minio')).toBeTruthy();
    expect(screen.getByText('http://minio:9000')).toBeTruthy();
    expect(screen.getByText('backups/relist')).toBeTruthy();
    expect(screen.getByText('Not tested')).toBeTruthy();
  });

  it('creates a destination with the exact body', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    api.createBackupDestination.mockResolvedValue(minio);
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'minio');
    await user.type(within(dialog).getByLabelText('Endpoint'), 'https://s3.example.com');
    await user.type(within(dialog).getByLabelText('Bucket'), 'backups');
    await user.type(within(dialog).getByLabelText('Prefix'), 'relist');
    await user.type(within(dialog).getByLabelText('Access key ID'), 'AKIA');
    await user.type(within(dialog).getByLabelText('Secret access key'), 's3cret');
    await user.click(within(dialog).getByLabelText('Path-style addressing'));
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.createBackupDestination).toHaveBeenCalledWith({
        encrypt: false,
        name: 'minio',
        passphrase: '',
        s3: { accessKeyId: 'AKIA', bucket: 'backups', endpoint: 'https://s3.example.com', pathStyle: true, prefix: 'relist', region: '' },
        secret: 's3cret',
        type: 's3',
      }),
    );
  }, 15_000);

  it('warns that an http endpoint is unencrypted', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByText('The secret and backups travel unencrypted.')).toBeNull();
    await user.type(within(dialog).getByLabelText('Endpoint'), 'http://minio:9000');
    expect(await within(dialog).findByText('The secret and backups travel unencrypted.')).toBeTruthy();
  });

  it('edits with a blank secret and sends secret as an empty string', async () => {
    api.updateBackupDestination.mockResolvedValue(minio);
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit minio' }));
    const dialog = await screen.findByRole('dialog');
    expect((within(dialog).getByLabelText('Secret access key') as HTMLInputElement).placeholder).toBe('Leave blank to keep');
    await user.clear(within(dialog).getByLabelText('Prefix'));
    await user.type(within(dialog).getByLabelText('Prefix'), 'other');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.updateBackupDestination).toHaveBeenCalledWith(1, {
        encrypt: false,
        passphrase: '',
        name: 'minio',
        s3: { ...minio.s3, prefix: 'other' },
        secret: '',
        type: 's3',
      }),
    );
  });

  it('shows the secret error under the field when the endpoint changed without a secret', async () => {
    api.updateBackupDestination.mockRejectedValue(
      new ApiError(422, 'enter the secret access key again when changing the endpoint', 'secret'),
    );
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit minio' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await within(dialog).findByText('enter the secret access key again when changing the endpoint')).toBeTruthy();
  });

  it('asks to confirm the copies that stay in the bucket, and sends the number', async () => {
    api.deleteBackupDestination
      .mockRejectedValueOnce(new ApiError(409, '2 backup copies stay in the bucket', undefined, { copies: 2 }))
      .mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Delete minio' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('2 backup copies stay in the bucket. The panel stops tracking them.')).toBeTruthy();
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Delete destination' }));
    await waitFor(() => expect(api.deleteBackupDestination).toHaveBeenLastCalledWith(1, 2));
  });

  it('shows the schedules that use a destination when it cannot be deleted', async () => {
    api.deleteBackupDestination.mockRejectedValue(new ApiError(409, 'used by the schedules of: Shop', undefined, { schedules: ['Shop'] }));
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Delete minio' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    expect(await screen.findByText('used by the schedules of: Shop')).toBeTruthy();
  });

  it('records a failed test with its error', async () => {
    const failed = { ...minio, lastTestAt: '2026-11-14T10:00:00Z', lastTestError: 'bucket missing', lastTestOk: false };
    api.testBackupDestination.mockImplementation(async () => {
      api.listBackupDestinations.mockResolvedValue([failed]); // the server stored the result
      return failed;
    });
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Test minio' }));
    expect(await screen.findByText('Failed')).toBeTruthy();
    expect(api.testBackupDestination).toHaveBeenCalledWith(1);
  });
  it('adds an SFTP destination with a key', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    api.createBackupDestination.mockResolvedValue(box);
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByText('SFTP'));
    await user.type(within(dialog).getByLabelText('Name'), 'box');
    await user.type(within(dialog).getByLabelText('Host'), 'box.example');
    expect((within(dialog).getByLabelText('Port') as HTMLInputElement).value).toBe('22');
    await user.type(within(dialog).getByLabelText('User'), 'u');
    await user.type(within(dialog).getByLabelText('Base path'), 'backups');
    await user.click(await within(dialog).findByRole('combobox', { name: 'SSH key' }));
    await user.click(await screen.findByTitle('deploy'));
    expect(await within(dialog).findByText('ssh-ed25519 AAAAdeploy relist-deploy')).toBeTruthy();
    expect(within(dialog).getByText('Add this line to ~/.ssh/authorized_keys on the server.')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(api.createBackupDestination).toHaveBeenCalledWith({
        encrypt: false,
        name: 'box',
        passphrase: '',
        secret: '',
        sftp: { auth: 'key', host: 'box.example', path: 'backups', port: 22, sshKeyId: 5, user: 'u' },
        type: 'sftp',
      }),
    );
  });

  it('asks for the password of an SFTP destination before calling the API', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByText('SFTP'));
    await user.type(within(dialog).getByLabelText('Name'), 'box');
    await user.type(within(dialog).getByLabelText('Host'), 'box.example');
    await user.type(within(dialog).getByLabelText('User'), 'u');
    await user.click(within(dialog).getByRole('radio', { name: 'Password' }));
    await user.click(within(dialog).getByRole('button', { name: 'Save' }));
    expect(await within(dialog).findByText('Enter the password')).toBeTruthy();
    expect(api.createBackupDestination).not.toHaveBeenCalled();
  });

  it('shows the host key and resets it after confirmation', async () => {
    api.listBackupDestinations.mockResolvedValue([box]);
    api.resetBackupDestinationHostKey.mockResolvedValue({ ...box, sftp: { ...box.sftp, hostKey: '' } });
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit box' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('ssh-ed25519 SHA256:abc123')).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Reset host key' }));
    expect(await screen.findByText('Forget this host key? The next Test trusts whatever key the server presents.')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'OK' }));
    await waitFor(() => expect(api.resetBackupDestinationHostKey).toHaveBeenCalledWith(2));
  });

  it('says a destination with no host key is not trusted yet', async () => {
    api.listBackupDestinations.mockResolvedValue([{ ...box, sftp: { ...box.sftp, hostKey: '' } }]);
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit box' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('Not trusted yet. The next Test trusts the key the server presents.')).toBeTruthy();
  });

  it('shows where an SFTP destination is, leaving out port 22', async () => {
    api.listBackupDestinations.mockResolvedValue([
      { ...box, id: 2, name: 'box' },
      { ...box, id: 3, name: 'plain', sftp: { ...box.sftp, host: 'box', port: 22 } },
    ]);
    show();
    expect(await screen.findByText('sftp://u@box.example:23/b')).toBeTruthy();
    expect(screen.getByText('sftp://u@box/b')).toBeTruthy();
  });
});

describe('encryption', () => {
  const vault: BackupDestination = { ...minio, encrypt: true, id: 7, name: 'vault' };

  it('tags an encrypting destination as Encrypted', async () => {
    api.listBackupDestinations.mockResolvedValue([vault]);
    show();
    expect(await screen.findByText('Encrypted')).toBeTruthy();
  });

  it('creates an encrypting destination with a generated passphrase, warning about loss', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    api.createBackupDestination.mockResolvedValue(vault);
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'vault');
    await user.type(within(dialog).getByLabelText('Endpoint'), 'http://minio:9000');
    await user.type(within(dialog).getByLabelText('Bucket'), 'backups');
    await user.type(within(dialog).getByLabelText('Access key ID'), 'AKIA');
    await user.type(within(dialog).getByLabelText('Secret access key'), 'sec');
    await user.click(within(dialog).getByLabelText('Encrypt copies with a passphrase'));
    expect(within(dialog).getByText(/The panel cannot help if this server is lost/)).toBeTruthy();
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }));
    const field = within(dialog).getByLabelText('Passphrase') as HTMLInputElement;
    expect(field.value).toHaveLength(24);
    await user.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.createBackupDestination).toHaveBeenCalled());
    const sent = api.createBackupDestination.mock.calls[0][0];
    expect(sent.encrypt).toBe(true);
    expect(sent.passphrase).toHaveLength(24);
  });

  it('shows the server error under the passphrase field', async () => {
    api.listBackupDestinations.mockResolvedValue([]);
    api.createBackupDestination.mockRejectedValue(
      new ApiError(422, 'the passphrase must be at least 20 characters', 'passphrase'),
    );
    const user = userEvent.setup();
    show();
    await user.click((await screen.findAllByRole('button', { name: /Add destination/ }))[0]);
    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('Name'), 'vault');
    await user.type(within(dialog).getByLabelText('Endpoint'), 'http://minio:9000');
    await user.type(within(dialog).getByLabelText('Bucket'), 'backups');
    await user.type(within(dialog).getByLabelText('Access key ID'), 'AKIA');
    await user.type(within(dialog).getByLabelText('Secret access key'), 'sec');
    await user.click(within(dialog).getByLabelText('Encrypt copies with a passphrase'));
    await user.type(within(dialog).getByLabelText('Passphrase'), 'short-pass');
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await within(dialog).findByText('the passphrase must be at least 20 characters')).toBeTruthy();
  });

  it('reveals the passphrase of an encrypting destination', async () => {
    api.listBackupDestinations.mockResolvedValue([vault]);
    api.revealDestinationPassphrase.mockResolvedValue({ passphrase: 'p-0123456789-0123456789' });
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit vault' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByRole('button', { name: 'Reveal passphrase' }));
    await waitFor(() => expect(api.revealDestinationPassphrase).toHaveBeenCalledWith(7));
    expect(await within(dialog).findByText('p-0123456789-0123456789')).toBeTruthy();
  });

  it('asks before forgetting the passphrase when encryption is turned off', async () => {
    api.listBackupDestinations.mockResolvedValue([vault]);
    api.updateBackupDestination.mockResolvedValue({ ...vault, encrypt: false });
    const user = userEvent.setup();
    show();
    await user.click(await screen.findByRole('button', { name: 'Edit vault' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(within(dialog).getByLabelText('Encrypt copies with a passphrase'));
    await user.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText(/Existing copies keep the old passphrase and the panel forgets it/)).toBeTruthy();
    expect(api.updateBackupDestination).not.toHaveBeenCalled();
    await user.click(screen.getByRole('button', { name: 'Forget the passphrase' }));
    await waitFor(() => expect(api.updateBackupDestination).toHaveBeenCalled());
    expect(api.updateBackupDestination.mock.calls[0][1].encrypt).toBe(false);
  });
});
