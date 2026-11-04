// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { BuildersResponse } from '../data/types';
import { ComposeBuilderSelect } from './ComposeBuilderSelect';

const api = vi.hoisted(() => ({
  getComposeBuilder: vi.fn(),
  listBuilders: vi.fn(),
  setComposeBuilder: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

const location = { composeId: 7, environmentId: 2, projectId: 1 };
const BUILT = 'services:\n  web:\n    build: ./src\n';

const listing: BuildersResponse = {
  buildkitImage: '',
  builders: [
    { cpus: 2, id: 1, lastError: '', maxParallelism: 4, memoryMb: 4096, name: 'large', state: 'running' },
    { cpus: 0.5, id: 2, lastError: '', maxParallelism: null, memoryMb: 1024, name: 'small', state: 'missing' },
  ],
  capacity: { cpus: 16, memoryMb: 31900 },
  defaultBuilderId: 1,
  defaultBuildkitImage: 'moby/buildkit@sha256:x',
};

function show(yaml = BUILT) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, shouldRetryOnError: false }}>
      <App>
        <MemoryRouter>
          <ComposeBuilderSelect location={location} yaml={yaml} />
        </MemoryRouter>
      </App>
    </SWRConfig>,
  );
}

beforeEach(() => {
  vi.resetAllMocks();
  api.listBuilders.mockResolvedValue(listing);
  api.getComposeBuilder.mockResolvedValue({ builderId: 0, mode: 'default' });
});

describe('ComposeBuilderSelect', () => {
  it('offers the panel default by name, Docker default and each Builder, and saves a pick', async () => {
    api.setComposeBuilder.mockResolvedValue(undefined);
    show();
    const select = await screen.findByRole('combobox', { name: 'Builder' });
    await waitFor(() => expect(screen.getByText('Use panel default (large)')).toBeTruthy());
    await userEvent.click(select);
    await screen.findByTitle('Docker default');
    const options = Array.from(document.querySelectorAll('.ant-select-item-option'));
    expect(options.map((o) => o.getAttribute('title'))).toEqual([
      'Use panel default (large)',
      'Docker default',
      'large (2 CPU, 4 GB, 4 parallel steps)',
      'small (0.5 CPU, 1 GB)',
    ]);
    await userEvent.click(screen.getByTitle('small (0.5 CPU, 1 GB)'));
    await waitFor(() => expect(api.setComposeBuilder).toHaveBeenCalledWith(location, { builderId: 2, mode: 'builder' }));
  });

  it('is hidden for a Compose without build:', async () => {
    show('services:\n  web:\n    image: nginx\n');
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole('combobox', { name: 'Builder' })).toBeNull();
    expect(api.listBuilders).not.toHaveBeenCalled();
  });

  it('keeps the saved choice and shows the error when saving fails', async () => {
    api.setComposeBuilder.mockRejectedValue(new ApiError(422, 'builder not found'));
    show();
    const select = await screen.findByRole('combobox', { name: 'Builder' });
    await waitFor(() => expect(screen.getByText('Use panel default (large)')).toBeTruthy());
    await userEvent.click(select);
    await userEvent.click(await screen.findByTitle('Docker default'));
    expect(await screen.findByText('builder not found')).toBeTruthy();
    const shown = select.closest('.ant-select') as HTMLElement;
    expect(within(shown).getByText('Use panel default (large)')).toBeTruthy();
  });
});
