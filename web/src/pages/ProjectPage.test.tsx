// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { useEffect } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { paths } from '../app/paths';
import { api, ZeroDowntimeRefusedError } from '../data/api';
import { PanelProvider, usePanel } from '../data/store';
import type { Compose } from '../data/types';
import { ProjectPage } from './ProjectPage';

vi.mock('../components/YamlEditor', () => ({ YamlEditor: () => null }));

function compose(id: number, name: string): Compose {
  return {
    createdAt: '2026-10-12T00:00:00Z',
    environmentId: 2,
    envSlug: 'production',
    id,
    name,
    projectSlug: 'project',
    status: { running: 0, total: 0 },
    updatedAt: '2026-10-12T00:00:00Z',
    yamlContent: 'services:\n  web:\n    image: nginx\n',
  };
}

function LoadProjectTree() {
  const { loadProjects } = usePanel();
  useEffect(() => { void loadProjects(); }, [loadProjects]);
  return null;
}

function renderPage() {
  const router = createMemoryRouter([
    { element: <ProjectPage />, path: '/projects/:projectId' },
    { element: <ProjectPage />, path: '/projects/:projectId/env/:environmentId/:tab' },
    { element: <div>Compose page</div>, path: '/projects/:projectId/env/:environmentId/compose/:composeId/:tab?' },
  ], { initialEntries: ['/projects/1'] });
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
  vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
  vi.spyOn(api, 'listProjects').mockResolvedValue([{
    createdAt: '2026-10-12T00:00:00Z', description: '', environmentCount: 1, environments: [], id: 1,
    lastUpdated: '2026-10-12T00:00:00Z', name: 'Project',
  }]);
  vi.spyOn(api, 'listEnvironments').mockResolvedValue([{
    composes: [], createdAt: '2026-10-12T00:00:00Z', id: 2, name: 'production', projectId: 1, variables: [],
  }]);
  vi.spyOn(api, 'listComposes').mockResolvedValue([compose(7, 'Compose A'), compose(8, 'Compose B')]);
  vi.spyOn(api, 'listVariables').mockResolvedValue([]);
  vi.spyOn(api, 'getSource').mockResolvedValue(null);
});

afterEach(() => { vi.restoreAllMocks(); });

describe('ProjectPage deploy', () => {
  it('continues after a zero-downtime refusal', async () => {
    const enqueue = vi.spyOn(api, 'enqueueDeployment')
      .mockRejectedValueOnce(new ZeroDowntimeRefusedError('refused', [{ service: 'web', reasons: ['has no healthcheck'] }]))
      .mockResolvedValueOnce({ number: 5 });
    const user = userEvent.setup();
    const router = renderPage();
    const rowA = (await screen.findByText('Compose A')).closest('tr')!;
    await user.click(within(rowA).getByRole('button', { name: /Deploy$/ }));
    const dialog = await screen.findByRole('dialog', { name: "Can't deploy with zero downtime" });
    expect(within(dialog).getByText('web — has no healthcheck')).toBeTruthy();
    expect(within(dialog).queryByRole('button', { name: 'Open editor' })).toBeNull();
    expect(screen.queryByText('refused')).toBeNull();

    await user.click(within(dialog).getByRole('button', { name: 'Close' }));
    const rowB = screen.getByText('Compose B').closest('tr')!;
    await user.click(within(rowB).getByRole('button', { name: /Deploy$/ }));
    await waitFor(() => expect(enqueue).toHaveBeenCalledTimes(2));
    expect(enqueue.mock.calls[1][0]).toEqual({ projectId: 1, environmentId: 2, composeId: 8 });
    expect(await screen.findByText('Deployment #5 queued')).toBeTruthy();
    expect(router.state.location.pathname).not.toContain('compose');
  });

  it('opens the refused Compose from the modal', async () => {
    vi.spyOn(api, 'enqueueDeployment')
      .mockRejectedValueOnce(new ZeroDowntimeRefusedError('refused', [{ service: 'web', reasons: ['has no healthcheck'] }]));
    const user = userEvent.setup();
    const router = renderPage();
    const rowA = (await screen.findByText('Compose A')).closest('tr')!;
    await user.click(within(rowA).getByRole('button', { name: /Deploy$/ }));
    await user.click(await screen.findByRole('button', { name: 'Open Compose A' }));
    await waitFor(() => expect(router.state.location.pathname).toBe(paths.compose(1, 2, 7)));
  });
});

describe('Roles on the Project page', () => {
  const member = { id: 5, email: 'm@example.com', isAdmin: false, isOwner: false };
  const as = (projectRole: 'editor' | null, environmentRole: 'viewer' | 'operator' | 'editor') =>
    vi.mocked(api.authStatus).mockResolvedValue({
      authenticated: true, user: member,
      access: { projects: [{ id: 1, role: projectRole, environments: [{ id: 2, role: environmentRole }] }] },
    });
  const disabled = (name: RegExp | string) => (screen.getByRole('button', { name }) as HTMLButtonElement).disabled;

  it('lets a Project Editor add an Environment and edit details', async () => {
    as('editor', 'editor');
    renderPage();
    await screen.findByText('Compose A');
    await waitFor(() => expect(disabled(/New Environment/)).toBe(false));
    expect(disabled(/Edit details/)).toBe(false);
  });

  it('keeps an Environment Editor from Project-level changes, but not from Composes', async () => {
    as(null, 'editor');
    renderPage();
    await screen.findByText('Compose A');
    await waitFor(() => expect(disabled(/New Environment/)).toBe(true));
    expect(disabled(/Edit details/)).toBe(true);
    expect(disabled(/New Compose/)).toBe(false);
  });

  it('lets a Viewer open Composes only', async () => {
    as(null, 'viewer');
    renderPage();
    await screen.findByText('Compose A');
    await waitFor(() => expect(disabled(/New Compose/)).toBe(true));
    for (const button of screen.getAllByRole('button', { name: /Deploy$/ })) {
      expect((button as HTMLButtonElement).disabled).toBe(true);
    }
    expect(screen.getAllByRole('button', { name: /Open$/ }).every((button) => !(button as HTMLButtonElement).disabled)).toBe(true);
  });
});
