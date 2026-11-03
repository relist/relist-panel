// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App as AntApp } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { BuildersResponse } from '../data/types';
import { BuildersPage, CPU_HELP, DOCKER_DEFAULT_DESCRIPTION } from './BuildersPage';

const api = vi.hoisted(() => ({
  builderDiskUsage: vi.fn(),
  createBuilder: vi.fn(),
  deleteBuilder: vi.fn(),
  listBuilders: vi.fn(),
  pruneBuilder: vi.fn(),
  setBuildKitImage: vi.fn(),
  setDefaultBuilder: vi.fn(),
  startBuilder: vi.fn(),
  updateBuilder: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const DEFAULT_REF = 'moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe';

function listing(overrides: Partial<BuildersResponse> = {}): BuildersResponse {
  return {
    buildkitImage: '',
    builders: [
      { cpus: 2, id: 1, lastError: '', maxParallelism: 4, memoryMb: 4096, name: 'large', state: 'running', usedBy: ['web'] },
    ],
    capacity: { cpus: 16, memoryMb: 31900 },
    defaultBuilderId: 1,
    defaultBuildkitImage: DEFAULT_REF,
    ...overrides,
  };
}

function show() {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <AntApp>
        <MemoryRouter>
          <BuildersPage />
        </MemoryRouter>
      </AntApp>
    </SWRConfig>,
  );
}

function row(name: string): HTMLElement {
  const cell = screen.getAllByText(name).find((el) => el.closest('tr[data-row-key]'));
  if (!cell) throw new Error(`no row for ${name}`);
  return cell.closest('tr') as HTMLElement;
}

beforeEach(() => {
  vi.resetAllMocks();
  api.builderDiskUsage.mockResolvedValue({ bytes: 4_172_000_000 });
});

describe('BuildersPage', () => {
  it('lists Docker default first, read-only, with what it does not do', async () => {
    api.listBuilders.mockResolvedValue(listing());
    show();
    await screen.findByText('large');
    const rows = document.querySelectorAll('tbody tr[data-row-key]');
    expect(within(rows[0] as HTMLElement).getByText('Docker default')).toBeTruthy();
    const docker = row('Docker default');
    expect(within(docker).getByText(DOCKER_DEFAULT_DESCRIPTION)).toBeTruthy();
    expect(within(docker).queryByRole('button', { name: /Edit/ })).toBeNull();
    expect(within(docker).queryByRole('button', { name: /Delete/ })).toBeNull();
    expect(within(row('large')).getByText('2 CPU, 4 GB, 4 parallel steps')).toBeTruthy();
  });

  it('tags the panel default and sets another', async () => {
    api.listBuilders.mockResolvedValue(listing());
    api.setDefaultBuilder.mockResolvedValue(undefined);
    show();
    await screen.findByText('large');
    expect(within(row('large')).getByText('Default')).toBeTruthy();
    expect(within(row('large')).queryByRole('button', { name: /Set .* as default/ })).toBeNull();
    await userEvent.click(within(row('Docker default')).getByRole('button', { name: 'Set Docker default as default' }));
    await waitFor(() => expect(api.setDefaultBuilder).toHaveBeenCalledWith(0));
  });

  it('shows a refused delete on the page', async () => {
    api.listBuilders.mockResolvedValue(listing({ defaultBuilderId: 0 }));
    api.deleteBuilder.mockRejectedValue(new ApiError(409, 'These Composes use this Builder: web'));
    show();
    await screen.findByText('large');
    await userEvent.click(within(row('large')).getByRole('button', { name: 'Delete large' }));
    const dialog = await screen.findByRole('dialog');
    await userEvent.type(within(dialog).getByRole('textbox'), 'large');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete Builder' }));
    expect(await screen.findByText('These Composes use this Builder: web')).toBeTruthy();
    expect(api.deleteBuilder).toHaveBeenCalledWith(1);
  });

  it('renders the table before cache sizes arrive', async () => {
    api.listBuilders.mockResolvedValue(listing());
    let resolve: (value: { bytes: number }) => void = () => {};
    api.builderDiskUsage.mockReturnValue(new Promise((r) => (resolve = r)));
    show();
    expect(await screen.findByText('large')).toBeTruthy();
    resolve({ bytes: 4_172_000_000 });
    expect(await within(row('large')).findByText(/GB/)).toBeTruthy();
    expect(api.builderDiskUsage).toHaveBeenCalledWith(1);
  });

  it('explains the CPU limit and rejects a CPU under 0.1 before saving', async () => {
    api.listBuilders.mockResolvedValue(listing());
    show();
    await screen.findByText('large');
    await userEvent.click(screen.getByRole('button', { name: /Add Builder/ }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(CPU_HELP)).toBeTruthy();
    await userEvent.type(within(dialog).getByLabelText('Name'), 'small');
    await userEvent.type(within(dialog).getByLabelText('CPU'), '0.05');
    await userEvent.type(within(dialog).getByLabelText('Memory'), '1024');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add' }));
    expect(await within(dialog).findByText('CPU must be between 0.1 and 16, in steps of 0.1')).toBeTruthy();
    expect(api.createBuilder).not.toHaveBeenCalled();
  });

  it('creates a Builder with an empty max parallel steps as null', async () => {
    api.listBuilders.mockResolvedValue(listing());
    api.createBuilder.mockResolvedValue({ cpus: 1, id: 2, lastError: '', maxParallelism: null, memoryMb: 1024, name: 'small' });
    show();
    await screen.findByText('large');
    await userEvent.click(screen.getByRole('button', { name: /Add Builder/ }));
    const dialog = await screen.findByRole('dialog');
    await userEvent.type(within(dialog).getByLabelText('Name'), 'small');
    await userEvent.type(within(dialog).getByLabelText('CPU'), '1');
    await userEvent.type(within(dialog).getByLabelText('Memory'), '1024');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Add' }));
    await waitFor(() =>
      expect(api.createBuilder).toHaveBeenCalledWith({ cpus: 1, maxParallelism: null, memoryMb: 1024, name: 'small' }),
    );
  });

  it('saves the BuildKit image, uses the default, and shows a failed pull', async () => {
    api.listBuilders.mockResolvedValue(listing());
    api.setBuildKitImage.mockRejectedValueOnce(new ApiError(502, 'Could not pull x@sha256:1: manifest unknown'));
    api.setBuildKitImage.mockResolvedValue(undefined);
    show();
    await screen.findByText('large');
    const card = screen.getByText('BuildKit image').closest('.ant-card') as HTMLElement;
    expect(within(card).getByText('Default')).toBeTruthy();
    const input = within(card).getByRole('textbox');
    expect(input.getAttribute('placeholder')).toBe(DEFAULT_REF);
    await userEvent.type(input, 'x@sha256:1');
    await userEvent.click(within(card).getByRole('button', { name: /Save/ }));
    expect(await within(card).findByText('Could not pull x@sha256:1: manifest unknown')).toBeTruthy();
    await userEvent.click(within(card).getByRole('button', { name: 'Use default' }));
    expect((input as HTMLInputElement).value).toBe('');
    await userEvent.click(within(card).getByRole('button', { name: /Save/ }));
    await waitFor(() => expect(api.setBuildKitImage).toHaveBeenLastCalledWith(''));
  });
});
