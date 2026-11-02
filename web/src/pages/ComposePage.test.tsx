// @vitest-environment jsdom
import '../test/dom-setup';

import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { useEffect } from 'react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { paths } from '../app/paths';
import { api, ZeroDowntimeRefusedError } from '../data/api';
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
  sshKey: { id: 1, name: 'ci', publicKey: 'ssh-ed25519 AAAA relist-ci' },
  repoUrl: 'git@github.com:acme/old.git',
  submodules: false,
  zip: null,
};

function LoadProjectTree() {
  const { loadProjects } = usePanel();
  useEffect(() => { void loadProjects(); }, [loadProjects]);
  return null;
}

function renderPage(composeId = 7, tab?: string) {
  const router = createMemoryRouter([
    { element: <ComposePage />, path: '/projects/:projectId/env/:environmentId/compose/:composeId/:tab?' },
  ], { initialEntries: [paths.compose(1, 2, composeId, tab)] });
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
  vi.spyOn(api, 'listComposes').mockResolvedValue([compose(7), compose(8)]);
  vi.spyOn(api, 'listVariables').mockResolvedValue([]);
  vi.spyOn(api, 'listComposeVariables').mockResolvedValue([]);
  vi.spyOn(api, 'listServices').mockResolvedValue([]);
  vi.spyOn(api, 'listVolumes').mockResolvedValue({ available: false, services: [], volumes: [] });
  vi.spyOn(api, 'listOrphanVolumes').mockResolvedValue({ orphans: [] });
  vi.spyOn(api, 'listDeployments').mockResolvedValue([]);
  vi.spyOn(api, 'listZeroDowntime').mockResolvedValue([]);
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

describe('Source card key warning', () => {
  it('warns when a git Source has no SSH key', async () => {
    vi.spyOn(api, 'getSource').mockResolvedValue({ ...source, sshKey: null });
    renderPage();
    expect(await screen.findByText('No SSH key — choose one before deploying.')).toBeTruthy();
  });

  it('shows no warning when the Source has a key', async () => {
    vi.spyOn(api, 'getSource').mockResolvedValue(source);
    renderPage();
    await screen.findByText(/acme\/old/);
    expect(screen.queryByText('No SSH key — choose one before deploying.')).toBeNull();
  });
});

describe('Fresh volumes confirmation', () => {
  it('warns before submitting that deletion survives failure and requires a backup', async () => {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    vi.mocked(api.listVolumes).mockResolvedValue({ available: true, services: [], volumes: [] });
    const enqueue = vi.spyOn(api, 'enqueueDeployment');
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole('heading', { name: 'Compose 7' });
    await user.click(screen.getByRole('button', { name: 'More deploy options' }));
    await user.click(await screen.findByRole('menuitem', { name: /Deploy with fresh volumes/ }));
    const dialog = await screen.findByRole('dialog', { name: 'Deploy with fresh volumes?' });
    expect(within(dialog).getByText('Named volume data is permanently deleted before deployment.')).toBeTruthy();
    expect(within(dialog).getByText(/If deployment fails, the deleted data is not restored/)).toBeTruthy();
    expect(within(dialog).getByText(/Back up the volumes before continuing/)).toBeTruthy();
    expect(enqueue).not.toHaveBeenCalled();
    expect((within(dialog).getByRole('button', { name: 'Deploy with fresh volumes' }) as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('Zero-downtime refusal', () => {
  const problems = [{ service: 'api', reasons: ['has no healthcheck'] }];

  async function deployRefused(error: Error) {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    vi.spyOn(api, 'enqueueDeployment').mockRejectedValue(error);
    const user = userEvent.setup();
    const router = renderPage();
    await screen.findByRole('heading', { name: 'Compose 7' });
    await user.click(screen.getByRole('button', { name: /Deploy$/ }));
    return { router, user };
  }

  it('opens the refusal modal instead of the error Alert', async () => {
    await deployRefused(new ZeroDowntimeRefusedError("zero-downtime deploy is on for services that can't be rolled out", problems));
    const dialog = await screen.findByRole('dialog', { name: "Can't deploy with zero downtime" });
    expect(within(dialog).getByText('api — has no healthcheck')).toBeTruthy();
    expect(screen.queryByText("zero-downtime deploy is on for services that can't be rolled out")).toBeNull();
  });

  it('still shows any other refusal as the error Alert', async () => {
    await deployRefused(new Error('compose is blank'));
    expect(await screen.findByText('compose is blank')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens the Domains tab, then the Editor tab', async () => {
    const { router, user } = await deployRefused(new ZeroDowntimeRefusedError('refused', problems));
    await user.click(await screen.findByRole('button', { name: 'Open domains' }));
    await waitFor(() => expect(router.state.location.pathname).toBe(paths.compose(1, 2, 7, 'domains')));
    vi.mocked(api.enqueueDeployment).mockRejectedValue(new ZeroDowntimeRefusedError('refused', problems));
    await user.click(screen.getByRole('button', { name: /Deploy$/ }));
    await user.click(await screen.findByRole('button', { name: 'Open editor' }));
    await waitFor(() => expect(router.state.location.pathname).toBe(paths.compose(1, 2, 7)));
  });
});

describe('Compose variables on a cold deep link', () => {
  it('loads them once the Project tree arrives instead of failing before it', async () => {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    vi.spyOn(api, 'listVariableSets').mockResolvedValue([]);
    const composes = deferred<Compose[]>();
    vi.mocked(api.listComposes).mockReturnValue(composes.promise);
    vi.mocked(api.listComposeVariables).mockResolvedValue([
      { composeId: 7, id: 1, key: 'GREETING', secret: false, value: 'hello' },
    ]);
    renderPage(7, 'variables');

    await act(async () => { composes.resolve([compose(7), compose(8)]); });
    await screen.findByText('GREETING');
    expect(screen.queryByText(/Could not load this Compose's variables/)).toBeNull();
  });
});

describe('Zero-downtime switches', () => {
  // Each read runs `docker compose config` on the host.
  it('reads them once for the Domains tab', async () => {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    vi.spyOn(api, 'listDomains').mockResolvedValue({ domains: [], services: ['api'] });
    const list = vi.mocked(api.listZeroDowntime).mockResolvedValue([{ service: 'api', enabled: false, reasons: [] }]);
    renderPage(7, 'domains');
    await screen.findByRole('switch', { name: 'Zero-downtime deploy for api' });
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 50)); });
    expect(list).toHaveBeenCalledTimes(1);
  });
});

describe('Fresh volumes and zero-downtime', () => {
  async function openFresh() {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    vi.mocked(api.listVolumes).mockResolvedValue({ available: true, services: [], volumes: [] });
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole('heading', { name: 'Compose 7' });
    await user.click(screen.getByRole('button', { name: 'More deploy options' }));
    await user.click(await screen.findByRole('menuitem', { name: /Deploy with fresh volumes/ }));
    return screen.findByRole('dialog', { name: 'Deploy with fresh volumes?' });
  }

  it('says zero-downtime deploy does not apply when a switch is on', async () => {
    vi.spyOn(api, 'listZeroDowntime').mockResolvedValue([{ service: 'api', enabled: true, reasons: [] }]);
    const dialog = await openFresh();
    expect(within(dialog).getByText("Zero-downtime deploy doesn't apply to this deploy.")).toBeTruthy();
  });

  it('leaves the line out when no switch is on', async () => {
    vi.spyOn(api, 'listZeroDowntime').mockResolvedValue([{ service: 'api', enabled: false, reasons: [] }]);
    const dialog = await openFresh();
    expect(within(dialog).queryByText("Zero-downtime deploy doesn't apply to this deploy.")).toBeNull();
  });
});

describe('Delete confirmation', () => {
  async function openDelete() {
    vi.spyOn(api, 'getSource').mockResolvedValue(null);
    const user = userEvent.setup();
    renderPage();
    await screen.findByRole('heading', { name: 'Compose 7' });
    await user.click(screen.getByRole('button', { name: 'more' }));
    await user.click(await screen.findByRole('menuitem', { name: /Delete Compose/ }));
    return { dialog: await screen.findByRole('dialog', { name: 'Delete this Compose?' }), user };
  }

  it('lists the volumes removed, the shared volumes kept and the directory before the typed confirmation', async () => {
    const plan = vi.spyOn(api, 'removalPlan').mockResolvedValue({
      composes: [{
        composeId: 7,
        directory: '/data/.composes/7',
        keepVolumes: [{ name: 'shared-db', reason: 'declared by Compose Other' }],
        name: 'Compose 7',
        removeVolumes: ['app-data'],
      }],
    });
    const { dialog } = await openDelete();
    expect(await within(dialog).findByText('app-data')).toBeTruthy();
    expect(within(dialog).getByText('shared-db')).toBeTruthy();
    expect(within(dialog).getByText('declared by Compose Other')).toBeTruthy();
    expect(within(dialog).getByText('/data/.composes/7')).toBeTruthy();
    expect(plan).toHaveBeenCalledWith('/api/projects/1/environments/2/composes/7/removal-plan');
  });

  it('keeps the typed confirmation usable when the plan cannot be loaded', async () => {
    vi.spyOn(api, 'removalPlan').mockRejectedValue(new Error('Request failed (500)'));
    const deleteCompose = vi.spyOn(api, 'deleteCompose').mockResolvedValue(undefined);
    const { dialog, user } = await openDelete();
    expect(await within(dialog).findByText('Could not load what this delete removes.')).toBeTruthy();
    expect(within(dialog).getByText('Directory removed')).toBeTruthy();
    await user.type(within(dialog).getByPlaceholderText('Compose 7'), 'Compose 7');
    const confirm = within(dialog).getByRole('button', { name: 'Delete Compose' }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(false);
    await user.click(confirm);
    expect(deleteCompose).toHaveBeenCalled();
  });
});

describe('Compose vulnerability badges', () => {
  it('renders vulnerability badges in the header and per Service', async () => {
    vi.spyOn(api, 'listServices').mockResolvedValue([
      { service: 'api', containerId: 'c1', containerName: 'x-api-1', image: 'shop-web:latest', state: 'running', health: 'none', ports: [] },
    ]);
    vi.spyOn(api, 'vulnSummary').mockResolvedValue({
      enabled: true,
      counts: { critical: 3, high: 8, medium: 0, low: 0, unknown: 0, fixable: 5 },
      services: [{ service: 'api', imageId: 'sha256:a', counts: { critical: 1, high: 2, medium: 0, low: 0, unknown: 0, fixable: 1 } }],
    });
    renderPage(7);
    const header = await screen.findByRole('link', { name: '3 Critical, 8 High, 5 fixable vulnerabilities' });
    expect(header.getAttribute('href')).toBe('/vulnerabilities?tab=cves&composeId=7');
    expect(await screen.findByText('1 Critical')).toBeTruthy();
    expect(api.vulnSummary).toHaveBeenCalledWith({ composeId: 7 });
  });

  it('hides vulnerability badges when scanning is off', async () => {
    vi.spyOn(api, 'vulnSummary').mockResolvedValue({ enabled: false });
    renderPage(8);
    await waitFor(() => expect(api.vulnSummary).toHaveBeenCalledWith({ composeId: 8 }));
    expect(screen.queryByText(/ Critical$/)).toBeNull();
  });
});
