// @vitest-environment jsdom
import '../test/dom-setup';

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from 'antd';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ComposeVolumes, VolumeEntry } from '../data/types';
import { VolumeExplorerPage } from './VolumeExplorerPage';

// Monaco needs a real browser; the editor modal is not under test here.
vi.mock('../components/VolumeFileEditor', () => ({ VolumeFileEditor: () => null }));

const mocks = vi.hoisted(() => ({ listVolumeEntries: vi.fn(), loadVolumes: vi.fn() }));
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
