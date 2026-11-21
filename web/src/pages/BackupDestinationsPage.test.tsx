// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { BackupDestination } from '../data/types';
import { BackupDestinationsPage } from './BackupDestinationsPage';

const api = vi.hoisted(() => ({
  createBackupDestination: vi.fn(),
  deleteBackupDestination: vi.fn(),
  listBackupDestinations: vi.fn(),
  testBackupDestination: vi.fn(),
  updateBackupDestination: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const minio: BackupDestination = {
  id: 1,
  lastTestAt: null,
  lastTestError: '',
  lastTestOk: false,
  name: 'minio',
  s3: { accessKeyId: 'AKIA', bucket: 'backups', endpoint: 'http://minio:9000', pathStyle: true, prefix: 'relist', region: '' },
  type: 's3',
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
        name: 'minio',
        s3: { accessKeyId: 'AKIA', bucket: 'backups', endpoint: 'https://s3.example.com', pathStyle: true, prefix: 'relist', region: '' },
        secret: 's3cret',
        type: 's3',
      }),
    );
  });

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
});
