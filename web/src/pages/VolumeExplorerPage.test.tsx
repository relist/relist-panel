// @vitest-environment jsdom
import '../test/dom-setup';

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComposeVolumes, VolumeEntry, VolumeUpload } from '../data/types';
import { VolumeExplorerPage } from './VolumeExplorerPage';

// Monaco needs a real browser; the editor modal is not under test here.
vi.mock('../components/VolumeFileEditor', () => ({ VolumeFileEditor: () => null }));

const mocks = vi.hoisted(() => ({ listVolumeEntries: vi.fn(), loadVolumes: vi.fn() }));
const uploadApi = vi.hoisted(() => ({
  listVolumeUploads: vi.fn(),
  startVolumeUpload: vi.fn(),
  appendVolumeUploadChunk: vi.fn(),
  completeVolumeUpload: vi.fn(),
  volumeUploadStatus: vi.fn(),
  cancelVolumeUpload: vi.fn(),
  writeVolumeFile: vi.fn(),
}));
vi.mock('../data/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../data/api')>();
  return { ...actual, api: { ...actual.api, ...uploadApi } };
});
vi.mock('../data/store', () => {
  // One stable object: the page keys effects on these callbacks and on the tree.
  const panel = {
    createVolumeDirectory: vi.fn(),
    createVolumeFile: vi.fn(),
    deleteVolumeEntry: vi.fn(),
    isPending: () => false,
    listVolumeEntries: mocks.listVolumeEntries,
    loadVolumes: mocks.loadVolumes,
    projects: [
      {
        id: 1,
        name: 'Shop',
        environments: [
          {
            id: 2,
            name: 'Production',
            composes: [{ id: 3, name: 'API', status: { running: 0, total: 1 } }],
          },
        ],
      },
    ],
    readVolumeFile: vi.fn(),
    restoreVolume: vi.fn(),
    writeVolumeFile: vi.fn(),
  };
  return { usePanel: () => panel };
});

const volumes: ComposeVolumes = {
  available: true,
  deployed: true,
  services: [{ service: 'web', user: '1000:1000', source: 'compose', warning: '' }],
  volumes: [
    {
      kind: 'volume',
      logicalName: 'data',
      source: 'shop_data',
      uses: [{ service: 'web', target: '/data', readOnly: false }],
      preflight: { status: 'ok', warning: '' },
    },
  ],
};

function entry(name: string): VolumeEntry {
  return { name, kind: 'file', size: 1, mode: 420, mtime: '2026-10-24T00:00:00Z', uid: 1000, gid: 1000 };
}

async function openListing() {
  render(
    <MemoryRouter initialEntries={['/projects/1/env/2/compose/3/volumes?source=shop_data']}>
      <App>
        <Routes>
          <Route element={<VolumeExplorerPage />} path="/projects/:projectId/env/:environmentId/compose/:composeId/volumes" />
        </Routes>
      </App>
    </MemoryRouter>,
  );
  const input = await screen.findByRole('combobox');
  fireEvent.mouseDown(input.closest('.ant-select')!.querySelector('.ant-select-content')!);
  await waitFor(() => expect(input.getAttribute('aria-expanded')).toBe('true'));
  const dropdown = document.getElementById(`${input.id}_list`)!.closest('.ant-select-dropdown')! as HTMLElement;
  fireEvent.click(within(dropdown).getAllByText('web — 1000:1000').find((el) => el.closest('.ant-select-item-option'))!);
  fireEvent.click(await screen.findByRole('button', { name: 'Confirm' }));
}

beforeEach(() => {
  mocks.loadVolumes.mockResolvedValue(volumes);
  uploadApi.listVolumeUploads.mockResolvedValue([]);
  uploadApi.cancelVolumeUpload.mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe('VolumeExplorerPage listing', () => {
  it('lists the entries of an untruncated listing without a notice', async () => {
    mocks.listVolumeEntries.mockResolvedValue({ entries: [entry('a.txt'), entry('b.txt')], truncated: false });
    await openListing();

    expect(await screen.findByText('a.txt')).toBeTruthy();
    expect(screen.getByText('b.txt')).toBeTruthy();
    expect(screen.queryByText(/Showing entries/)).toBeNull();
  });

  it('shows the range and a Next 10,000 button when the listing is truncated', async () => {
    mocks.listVolumeEntries.mockResolvedValue({ entries: [entry('a.txt')], truncated: true, next: 'a.txt' });
    await openListing();

    expect(await screen.findByText('a.txt')).toBeTruthy();
    expect(screen.getByText('Showing entries 1–10,000.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Next 10,000' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Back to the start' })).toBeNull();
  });

  it('pages by the name cursor and can go back to the start', async () => {
    mocks.listVolumeEntries.mockResolvedValueOnce({ entries: [entry('a.txt')], truncated: true, next: 'a.txt' });
    await openListing();
    await screen.findByText('a.txt');

    mocks.listVolumeEntries.mockResolvedValueOnce({ entries: [entry('b.txt')], truncated: true, next: 'b.txt' });
    fireEvent.click(screen.getByRole('button', { name: 'Next 10,000' }));
    expect(await screen.findByText('b.txt')).toBeTruthy();
    expect(mocks.listVolumeEntries.mock.calls.at(-1)?.[4]).toBe('a.txt');
    expect(screen.getByText('Showing entries 10,001–20,000.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Back to the start' })).toBeTruthy();

    mocks.listVolumeEntries.mockResolvedValueOnce({ entries: [entry('c.txt')], truncated: false });
    fireEvent.click(screen.getByRole('button', { name: 'Next 10,000' }));
    expect(await screen.findByText('c.txt')).toBeTruthy();
    expect(mocks.listVolumeEntries.mock.calls.at(-1)?.[4]).toBe('b.txt');
    expect(screen.getByText('Showing entries 20,001–20,001.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Next 10,000' })).toBeNull();

    mocks.listVolumeEntries.mockResolvedValueOnce({ entries: [entry('a.txt')], truncated: true, next: 'a.txt' });
    fireEvent.click(screen.getByRole('button', { name: 'Back to the start' }));
    expect(await screen.findByText('a.txt')).toBeTruthy();
    expect(mocks.listVolumeEntries.mock.calls.at(-1)?.[4]).toBeUndefined();
    expect(screen.getByText('Showing entries 1–10,000.')).toBeTruthy();
  });
});

function openUpload(over: Partial<VolumeUpload> = {}): VolumeUpload {
  return {
    id: 'old',
    source: 'shop_data',
    path: 'big.bin',
    name: 'big.bin',
    size: 5,
    lastModified: 7,
    offset: 3,
    state: 'uploading',
    errorKind: '',
    error: '',
    updatedAt: new Date().toISOString(),
    ...over,
  };
}

function pickFile(file: File) {
  const input = document.querySelector('input[type="file"]:not([accept])') as HTMLInputElement;
  fireEvent.change(input, { target: { files: [file] } });
}

const bigBin = () => new File(['hello'], 'big.bin', { lastModified: 7 });

describe('VolumeExplorerPage chunked upload', () => {
  beforeEach(() => {
    mocks.listVolumeEntries.mockResolvedValue({ entries: [entry('a.txt')], truncated: false });
  });

  it('asks to resume when the picked file matches an unfinished upload, and resumes from its offset', async () => {
    uploadApi.listVolumeUploads.mockResolvedValue([openUpload()]);
    uploadApi.appendVolumeUploadChunk.mockResolvedValue({ offset: 5, conflict: false });
    uploadApi.completeVolumeUpload.mockResolvedValue(openUpload({ state: 'done', offset: 5 }));
    await openListing();
    await screen.findByText('a.txt');

    pickFile(bigBin());
    expect(await screen.findByText('Resume this upload?')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Resume' }));

    await waitFor(() => expect(uploadApi.completeVolumeUpload).toHaveBeenCalled());
    expect(uploadApi.startVolumeUpload).not.toHaveBeenCalled();
    expect(uploadApi.appendVolumeUploadChunk.mock.calls[0][1]).toBe('old');
    expect(uploadApi.appendVolumeUploadChunk.mock.calls[0][2]).toBe(3);
  });

  it('starts over by discarding the old upload first', async () => {
    uploadApi.listVolumeUploads.mockResolvedValue([openUpload()]);
    uploadApi.startVolumeUpload.mockResolvedValue({ id: 'new', chunkSize: 33554432, offset: 0 });
    uploadApi.appendVolumeUploadChunk.mockResolvedValue({ offset: 5, conflict: false });
    uploadApi.completeVolumeUpload.mockResolvedValue(openUpload({ id: 'new', state: 'done', offset: 5 }));
    await openListing();
    await screen.findByText('a.txt');

    pickFile(bigBin());
    fireEvent.click(await screen.findByRole('button', { name: 'Start over' }));

    await waitFor(() => expect(uploadApi.startVolumeUpload).toHaveBeenCalled());
    expect(uploadApi.cancelVolumeUpload.mock.calls[0][1]).toBe('old');
    expect(uploadApi.cancelVolumeUpload.mock.invocationCallOrder[0]).toBeLessThan(
      uploadApi.startVolumeUpload.mock.invocationCallOrder[0],
    );
  });

  it('lists unfinished uploads with Discard', async () => {
    uploadApi.listVolumeUploads.mockResolvedValue([openUpload({ path: 'dir/x.bin' })]);
    await openListing();

    expect(await screen.findByText('Unfinished uploads')).toBeTruthy();
    expect(screen.getByText('dir/x.bin')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Discard' }));
    await waitFor(() => expect(uploadApi.cancelVolumeUpload).toHaveBeenCalledWith(expect.anything(), 'old'));
  });

  it('shows progress and Cancel while sending, with Upload disabled', async () => {
    uploadApi.startVolumeUpload.mockResolvedValue({ id: 'new', chunkSize: 33554432, offset: 0 });
    uploadApi.appendVolumeUploadChunk.mockReturnValue(new Promise(() => {}));
    await openListing();
    await screen.findByText('a.txt');

    pickFile(bigBin());

    expect(await screen.findByText('Uploading big.bin')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    const uploadButton = screen.getByRole('button', { name: /Upload/ }) as HTMLButtonElement;
    expect(uploadButton.disabled).toBe(true);
  });

  it('offers Retry and Discard when the write fails, and Retry writes again without sending', async () => {
    uploadApi.startVolumeUpload.mockResolvedValue({ id: 'new', chunkSize: 33554432, offset: 0 });
    uploadApi.appendVolumeUploadChunk.mockResolvedValue({ offset: 5, conflict: false });
    uploadApi.completeVolumeUpload
      .mockResolvedValueOnce(openUpload({ id: 'new', state: 'failed', offset: 5, errorKind: 'refused', error: 'need 10' }))
      .mockResolvedValueOnce(openUpload({ id: 'new', state: 'done', offset: 5 }));
    await openListing();
    await screen.findByText('a.txt');

    pickFile(bigBin());
    expect(await screen.findByText('Not enough space in the Volume')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Discard' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));

    await waitFor(() => expect(uploadApi.completeVolumeUpload).toHaveBeenCalledTimes(2));
    expect(uploadApi.appendVolumeUploadChunk).toHaveBeenCalledTimes(1);
  });

  it('does not list the upload being sent as unfinished', async () => {
    uploadApi.listVolumeUploads.mockResolvedValue([openUpload()]);
    uploadApi.appendVolumeUploadChunk.mockReturnValue(new Promise(() => {}));
    await openListing();
    expect(await screen.findByText('Unfinished uploads')).toBeTruthy();

    pickFile(bigBin());
    fireEvent.click(await screen.findByRole('button', { name: 'Resume' }));

    expect(await screen.findByText('Uploading big.bin')).toBeTruthy();
    expect(screen.queryByText('Unfinished uploads')).toBeNull();
  });

  it('writes an empty file directly: a chunked upload needs at least one byte', async () => {
    uploadApi.writeVolumeFile.mockResolvedValue(undefined);
    await openListing();
    await screen.findByText('a.txt');

    pickFile(new File([], '.gitkeep', { lastModified: 1 }));

    await waitFor(() => expect(uploadApi.writeVolumeFile).toHaveBeenCalled());
    expect(uploadApi.writeVolumeFile.mock.calls[0][2]).toBe('.gitkeep');
    expect(uploadApi.startVolumeUpload).not.toHaveBeenCalled();
  });
});
