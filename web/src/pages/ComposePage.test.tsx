// @vitest-environment jsdom
import '../test/dom-setup';

import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { useEffect } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { paths } from '../app/paths';
import { api } from '../data/api';
import { PanelProvider, usePanel } from '../data/store';
import type { Compose, ComposeSource } from '../data/types';
import { ComposePage } from './ComposePage';

// Monaco's workers need a browser; the Source card, modal, and controls remain real.
vi.mock('../components/YamlEditor', () => ({ YamlEditor: () => null }));
vi.mock('@xterm/xterm', () => ({ Terminal: vi.fn() }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve;
    reject = onReject;
  });
  return { promise, reject, resolve };
}

function compose(id: number): Compose {
  return {
    createdAt: '2026-10-12T00:00:00Z',
    environmentId: 2,
    envSlug: 'production',
    id,
    name: `Compose ${id}`,
    projectSlug: 'project',
    status: { running: 0, total: 0 },
    updatedAt: '2026-10-12T00:00:00Z',
    yamlContent: 'services:\n  api:\n    image: nginx\n',
  };
}

const source: ComposeSource = {
  branch: 'main',
  headSha: '',
  kind: 'git',
  lastTest: null,
  publicKey: 'ssh-ed25519 AAAA relist-compose-7',
  repoUrl: 'git@github.com:acme/old.git',
  submodules: false,
  zip: null,
};

function LoadProjectTree() {
  const { loadProjects } = usePanel();
  useEffect(() => { void loadProjects(); }, [loadProjects]);
  return null;
}

function renderPage(composeId = 7) {
  const router = createMemoryRouter([
    { element: <ComposePage />, path: '/projects/:projectId/env/:environmentId/compose/:composeId/:tab?' },
  ], { initialEntries: [paths.compose(1, 2, composeId)] });
  render(
    <App>
      <PanelProvider>
        <LoadProjectTree />
        <RouterProvider router={router} />
      </PanelProvider>
    </App>,
  );
  return router;
}

beforeEach(() => {
  vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, setupRequired: false });
  vi.spyOn(api, 'listProjects').mockResolvedValue([{
    createdAt: '2026-10-12T00:00:00Z', description: '', environmentCount: 1, environments: [], id: 1,
    lastUpdated: '2026-10-12T00:00:00Z', name: 'Project',
  }]);
  vi.spyOn(api, 'listEnvironments').mockResolvedValue([{
    composes: [], createdAt: '2026-10-12T00:00:00Z', id: 2, name: 'production', projectId: 1, variables: [],
  }]);
  vi.spyOn(api, 'listComposes').mockResolvedValue([compose(7), compose(8)]);
  vi.spyOn(api, 'listVariables').mockResolvedValue([]);
  vi.spyOn(api, 'listComposeVariables').mockResolvedValue([]);
  vi.spyOn(api, 'listServices').mockResolvedValue([]);
  vi.spyOn(api, 'listVolumes').mockResolvedValue({ available: false, services: [], volumes: [] });
  vi.spyOn(api, 'listOrphanVolumes').mockResolvedValue({ orphans: [] });
  vi.spyOn(api, 'listDeployments').mockResolvedValue([]);
});

afterEach(() => { vi.restoreAllMocks(); });

describe('Compose Source loading', () => {
  it('keeps the current Compose error and Retry after an old Compose succeeds', async () => {
    const oldRead = deferred<ComposeSource | null>();
    const currentRead = deferred<ComposeSource | null>();
    vi.spyOn(api, 'getSource')
      .mockImplementation(({ composeId }) => composeId === 7 ? oldRead.promise : currentRead.promise);
    const user = userEvent.setup();
    const router = renderPage();
    await screen.findByRole('heading', { name: 'Compose 7' });

    await act(async () => { await router.navigate(paths.compose(1, 2, 8)); });
    await act(async () => { currentRead.reject(new Error('Current Source denied')); });
    expect(screen.getByText(/The Source could not be loaded: Current Source denied/)).toBeTruthy();
    expect((screen.getByRole('button', { name: /Deploy$/ }) as HTMLButtonElement).disabled).toBe(true);

    await act(async () => { oldRead.resolve(source); });
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
    expect(screen.getByText(/The Source could not be loaded: Current Source denied/)).toBeTruthy();

    vi.mocked(api.getSource).mockResolvedValue(null);
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect((screen.getByRole('button', { name: /Deploy$/ }) as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });
});
