// @vitest-environment jsdom
import '../test/dom-setup';

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { App } from 'antd';
import dayjs from 'dayjs';
import { MemoryRouter } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { DeploymentLogTarget } from '../components/DeploymentLogDrawer';
import type { DeploymentHistoryQuery, GlobalDeployment, GlobalDeploymentPage, GlobalQueuedDeployment } from '../data/types';
import { DeploymentsPage } from './DeploymentsPage';

const mocks = vi.hoisted(() => ({
  api: { globalDeploymentQueue: vi.fn(), listGlobalDeployments: vi.fn() },
  drawer: vi.fn(),
}));
vi.mock('../data/api', () => ({ api: mocks.api }));
vi.mock('../data/store', () => ({
  usePanel: () => ({
    projects: [
      { id: 1, name: 'Shop', environments: [
        { id: 11, name: 'Production', composes: [{ id: 111, name: 'API' }, { id: 112, name: 'Worker' }] },
        { id: 12, name: 'Staging', composes: [{ id: 121, name: 'Preview' }] },
      ] },
      { id: 2, name: 'Docs', environments: [{ id: 21, name: 'Live', composes: [{ id: 211, name: 'Site' }] }] },
    ],
  }),
}));
// The shared Drawer has its own polling/cancellation lifecycle regression tests.
// This test double inspects this page's full-path selection and refresh boundary.
vi.mock('../components/DeploymentLogDrawer', () => ({
  DeploymentLogDrawer: (props: { target?: DeploymentLogTarget; context?: string; onClose: () => void; onCanceled?: () => void }) => {
    mocks.drawer(props);
    if (!props.target) return null;
    return <div role="dialog" aria-label={props.context}>
      <button onClick={props.onClose}>Close logs</button>
      <button onClick={props.onCanceled}>Cancel selected deployment</button>
    </div>;
  },
}));

function deployment(over: Partial<GlobalDeployment> = {}): GlobalDeployment {
  return {
    id: 91, number: 3, mode: 'build', noCache: false, freshVolumes: false,
    status: 'succeeded', step: 'deploy', steps: ['fetch', 'build:api', 'deploy'], failedStep: '', reason: '',
    sourceRef: 'a1b2c3d4', sourceSubject: 'Fix checkout', warnings: [],
    queuedAt: '2026-10-12T10:00:00Z', startedAt: '2026-10-12T10:00:01Z', finishedAt: '2026-10-12T10:01:00Z',
    projectId: 1, projectName: 'Shop', environmentId: 11, environmentName: 'Production', composeId: 111, composeName: 'API',
    ...over,
  };
}

function page(items: GlobalDeployment[], query: DeploymentHistoryQuery = {}, total = items.length): GlobalDeploymentPage {
  return { items, total, page: query.page ?? 1, pageSize: query.pageSize ?? 20 };
}

function queued(over: Partial<GlobalQueuedDeployment> = {}): GlobalQueuedDeployment {
  return { composeId: 211, composeName: 'Site', number: 3, status: 'running', step: 'build:site',
    projectId: 2, projectName: 'Docs', environmentId: 21, environmentName: 'Live', ...over };
}

function renderPage() {
  return render(<SWRConfig value={{ provider: () => new Map(), revalidateOnFocus: false, revalidateOnReconnect: false }}>
    <MemoryRouter><App><DeploymentsPage /></App></MemoryRouter>
  </SWRConfig>);
}

async function choose(label: string, option: string) {
  const input = screen.getByRole('combobox', { name: label });
  fireEvent.mouseDown(input.closest('.ant-select')!.querySelector('.ant-select-content')!);
  await waitFor(() => expect(input.getAttribute('aria-expanded')).toBe('true'));
  const dropdown = document.getElementById(`${input.id}_list`)!.closest('.ant-select-dropdown')! as HTMLElement;
  // Virtual Select exposes only its focused neighbors as accessible options.
  fireEvent.click(within(dropdown).getAllByText(option).find((el) => el.closest('.ant-select-item-option'))!);
  if (label !== 'Page Size') await waitFor(() => expect(input.getAttribute('aria-expanded')).toBe('false'));
  else fireEvent.keyDown(input, { key: 'Escape', keyCode: 27 });
}

beforeEach(() => {
  mocks.api.globalDeploymentQueue.mockResolvedValue([]);
  mocks.api.listGlobalDeployments.mockImplementation(async (query: DeploymentHistoryQuery) => page([deployment()], query, 45));
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.resetAllMocks();
  vi.restoreAllMocks();
});

describe('Global Deployments', () => {
  it('shows full context, retained history and an empty host queue', async () => {
    renderPage();
    expect(await screen.findByText('No deployments in the queue')).toBeTruthy();
    expect(await screen.findByRole('link', { name: 'API' })).toHaveProperty('href', expect.stringContaining('/projects/1/env/11/compose/111/deployments'));
    expect(screen.getByText(/latest 20 deployments per Compose/i)).toBeTruthy();
    expect(mocks.api.listGlobalDeployments).toHaveBeenCalledWith({ page: 1, pageSize: 20 });
  });

  it('requests server pagination and resets the page when status changes', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'API' });
    fireEvent.click(screen.getByTitle('Next Page'));
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 2, pageSize: 20 }));
    await choose('Status', 'failed');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20, status: 'failed' }));
  });

  it('requests a chosen server page size and returns to the first page', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'API' });
    fireEvent.click(screen.getByTitle('Next Page'));
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 2, pageSize: 20 }));
    await choose('Page Size', '50 / page');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 50 }));
    await choose('Page Size', '100 / page');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 100 }));
  });

  it('returns to a valid server page when active filtered history shrinks during polling', async () => {
    vi.useFakeTimers();
    let shrunk = false;
    mocks.api.globalDeploymentQueue.mockResolvedValue([queued()]);
    mocks.api.listGlobalDeployments.mockImplementation(async (query: DeploymentHistoryQuery) => {
      const items = query.page === 2
        ? shrunk ? [] : [deployment({ id: 92, composeName: 'Last queued row', status: 'queued' })]
        : [deployment({ composeName: 'First queued row', status: 'queued' })];
      return page(items, query, shrunk ? 20 : 21);
    });
    renderPage();
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const statusInput = screen.getByRole('combobox', { name: 'Status' });
    fireEvent.mouseDown(statusInput.closest('.ant-select')!.querySelector('.ant-select-content')!);
    fireEvent.click(document.querySelector('.ant-select-item-option[title="queued"]')!);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    fireEvent.click(screen.getByTitle('Next Page'));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByRole('link', { name: 'Last queued row' })).toBeTruthy();
    shrunk = true;
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    // Returning to a cached SWR key schedules its revalidation on the next frame.
    await act(async () => { await vi.advanceTimersByTimeAsync(50); });
    expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20, status: 'queued' });
    expect(screen.getByRole('link', { name: 'First queued row' })).toBeTruthy();
    expect(screen.queryByText('No matching deployments')).toBeNull();
  });

  it('preserves running-first FIFO queue order and shows the step and position', async () => {
    mocks.api.globalDeploymentQueue.mockResolvedValue([
      queued(), queued({ composeId: 112, composeName: 'Worker', projectId: 1, projectName: 'Shop', environmentId: 11,
        environmentName: 'Production', status: 'queued', step: '', number: 4 }),
      queued({ composeId: 121, composeName: 'Preview', projectId: 1, projectName: 'Shop', environmentId: 12,
        environmentName: 'Staging', status: 'queued', step: '', number: 5 }),
    ]);
    renderPage();
    await screen.findByRole('link', { name: 'Worker' });
    const rows = within(screen.getAllByRole('table')[0]).getAllByRole('row').slice(1);
    expect(within(rows[0]).getByRole('link', { name: 'Site' })).toBeTruthy();
    expect(within(rows[0]).getByText('Build site')).toBeTruthy();
    expect(within(rows[1]).getByRole('link', { name: 'Worker' })).toBeTruthy();
    expect(within(rows[1]).getByText('Position 2 · 1 ahead')).toBeTruthy();
    expect(within(rows[2]).getByRole('link', { name: 'Preview' })).toBeTruthy();
    expect(within(rows[2]).getByText('Position 3 · 2 ahead')).toBeTruthy();
  });

  it('preserves archive filenames while shortening only Git hashes in Source', async () => {
    const originalMatchMedia = window.matchMedia;
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({ ...originalMatchMedia(query), matches: !query.includes('max-width') }));
    mocks.api.listGlobalDeployments.mockResolvedValue(page([
      deployment({ sourceRef: 'release-build-2026-10-12.zip', sourceSubject: '' }),
      deployment({ id: 92, number: 4, sourceRef: 'a'.repeat(40), sourceSubject: 'Fix cart' }),
    ]));
    renderPage();
    expect(await screen.findByText('release-build-2026-10-12.zip')).toBeTruthy();
    expect(screen.getByText('aaaaaaa · Fix cart')).toBeTruthy();
  });

  it('keeps the source visible with context and status on narrow screens', async () => {
    mocks.api.listGlobalDeployments.mockResolvedValue(page([deployment({ sourceRef: 'release-2026.zip', sourceSubject: '' })]));
    renderPage();
    expect(await screen.findByText('release-2026.zip')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'API' })).toBeTruthy();
    expect(screen.getByText('succeeded')).toBeTruthy();
  });

  it('clears child filters when a parent changes and keeps the queue independent', async () => {
    mocks.api.globalDeploymentQueue.mockResolvedValue([queued()]);
    renderPage();
    await screen.findByRole('link', { name: 'Site' });
    await choose('Project', 'Shop');
    await choose('Environment', 'Production');
    await choose('Compose', 'API');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20, projectId: 1, environmentId: 11, composeId: 111 }));
    await choose('Environment', 'Staging');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20, projectId: 1, environmentId: 12 }));
    await choose('Project', 'Docs');
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20, projectId: 2 }));
    expect(screen.getByRole('link', { name: 'Site' })).toBeTruthy();
    expect(mocks.api.globalDeploymentQueue).toHaveBeenCalledTimes(1);
  });

  it('applies inclusive queuedAt day bounds', async () => {
    renderPage();
    await screen.findByRole('link', { name: 'API' });
    const start = screen.getByPlaceholderText('From date');
    const end = screen.getByPlaceholderText('To date');
    fireEvent.change(start, { target: { value: '2026-10-10' } });
    fireEvent.keyDown(start, { key: 'Enter' });
    fireEvent.change(end, { target: { value: '2026-10-12' } });
    fireEvent.keyDown(end, { key: 'Enter' });
    fireEvent.blur(end);
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 1, pageSize: 20,
      from: dayjs('2026-10-10').startOf('day').toISOString(), to: dayjs('2026-10-12').endOf('day').toISOString(),
    }));
  });

  it('opens distinct targets for the same local number in two Composes', async () => {
    mocks.api.globalDeploymentQueue.mockResolvedValue([queued()]);
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'View logs for Docs / Live / Site deployment #3' }));
    expect(mocks.drawer.mock.lastCall?.[0]).toMatchObject({ target: { location: { projectId: 2, environmentId: 21, composeId: 211 }, number: 3 }, follow: true });
    fireEvent.click(await screen.findByRole('button', { name: 'View logs for Shop / Production / API deployment #3' }));
    expect(mocks.drawer.mock.lastCall?.[0]).toMatchObject({ target: { location: { projectId: 1, environmentId: 11, composeId: 111 }, number: 3 }, context: 'Shop / Production / API', follow: true });
    fireEvent.click(screen.getByRole('button', { name: 'Close logs' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('refreshes both lists after Cancel in the shared Drawer', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: 'View logs for Shop / Production / API deployment #3' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel selected deployment' }));
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenCalledTimes(2));
    expect(mocks.api.globalDeploymentQueue).toHaveBeenCalledTimes(2);
  });

  it('hides the previous query and ignores its late response after filters change', async () => {
    let resolveOld!: (result: GlobalDeploymentPage) => void;
    let resolveNew!: (result: GlobalDeploymentPage) => void;
    mocks.api.listGlobalDeployments.mockImplementation((query: DeploymentHistoryQuery) => {
      if (!query.status) return new Promise((resolve) => { resolveOld = resolve; });
      return new Promise((resolve) => { resolveNew = resolve; });
    });
    renderPage();
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenCalledTimes(1));
    await choose('Status', 'failed');
    await act(async () => resolveNew(page([deployment({ id: 93, composeName: 'New filtered row', status: 'failed' })], { status: 'failed' })));
    expect(await screen.findByRole('link', { name: 'New filtered row' })).toBeTruthy();
    await act(async () => resolveOld(page([deployment({ composeName: 'Old unfiltered row' })])));
    expect(screen.queryByRole('link', { name: 'Old unfiltered row' })).toBeNull();
    expect(screen.getByRole('link', { name: 'New filtered row' })).toBeTruthy();
  });

  it('does not clamp a newer filter page from a late shrinking response', async () => {
    let resolveOld!: (result: GlobalDeploymentPage) => void;
    mocks.api.listGlobalDeployments.mockImplementation((query: DeploymentHistoryQuery) => {
      if (query.status === 'queued' && query.page === 2) return new Promise((resolve) => { resolveOld = resolve; });
      return Promise.resolve(page([deployment({ composeName: query.page === 2 ? 'Filtered page two' : 'First row', status: query.status ?? 'succeeded' })], query, 45));
    });
    renderPage();
    await screen.findByRole('link', { name: 'First row' });
    await choose('Status', 'queued');
    fireEvent.click(screen.getByTitle('Next Page'));
    await waitFor(() => expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 2, pageSize: 20, status: 'queued' }));
    await choose('Status', 'failed');
    await screen.findByRole('link', { name: 'First row' });
    fireEvent.click(screen.getByTitle('Next Page'));
    await screen.findByRole('link', { name: 'Filtered page two' });
    await act(async () => resolveOld(page([], { page: 2, pageSize: 20, status: 'queued' }, 20)));
    expect(mocks.api.listGlobalDeployments).toHaveBeenLastCalledWith({ page: 2, pageSize: 20, status: 'failed' });
    expect(screen.getByRole('link', { name: 'Filtered page two' })).toBeTruthy();
  });

  it('does not relabel cached history while the next query loads', async () => {
    mocks.api.listGlobalDeployments.mockResolvedValueOnce(page([deployment()])).mockImplementation(() => new Promise(() => {}));
    renderPage();
    await screen.findByRole('link', { name: 'API' });
    await choose('Status', 'failed');
    expect(screen.queryByRole('link', { name: 'API' })).toBeNull();
    expect(screen.queryByText('No matching deployments')).toBeNull();
  });

  it('shows both failures with explicit retries and recovers', async () => {
    mocks.api.globalDeploymentQueue.mockRejectedValueOnce(new Error('Queue unavailable')).mockResolvedValue([]);
    mocks.api.listGlobalDeployments.mockRejectedValueOnce(new Error('History unavailable')).mockResolvedValue(page([]));
    renderPage();
    expect(await screen.findByText('Queue unavailable')).toBeTruthy();
    expect(await screen.findByText('History unavailable')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry queue' }));
    fireEvent.click(screen.getByRole('button', { name: 'Retry history' }));
    expect(await screen.findByText('No deployments in the queue')).toBeTruthy();
    expect(await screen.findByText('No deployment history')).toBeTruthy();
    expect(screen.queryByText('Queue unavailable')).toBeNull();
    expect(screen.queryByText('History unavailable')).toBeNull();
  });

  it('distinguishes empty retained history from filters matching no deployments', async () => {
    mocks.api.listGlobalDeployments.mockResolvedValue(page([]));
    renderPage();
    expect(await screen.findByText('No deployment history')).toBeTruthy();
    await choose('Status', 'failed');
    expect(await screen.findByText('No matching deployments')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(await screen.findByText('No deployment history')).toBeTruthy();
  });

  it('refreshes history while work runs and after it finishes, then stops on unmount', async () => {
    vi.useFakeTimers();
    mocks.api.globalDeploymentQueue.mockResolvedValueOnce([queued()]).mockResolvedValue([]);
    mocks.api.listGlobalDeployments.mockResolvedValueOnce(page([deployment({ status: 'running', finishedAt: null })]))
      .mockResolvedValue(page([deployment()]));
    const view = renderPage();
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getAllByText('running').length).toBeGreaterThan(0);
    await act(async () => { await vi.advanceTimersByTimeAsync(2500); });
    expect(screen.getByText('succeeded')).toBeTruthy();
    expect(screen.getByText('No deployments in the queue')).toBeTruthy();
    const beforeIdle = mocks.api.listGlobalDeployments.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(mocks.api.listGlobalDeployments.mock.calls.length).toBeGreaterThan(beforeIdle);
    view.unmount();
    const beforeUnmount = mocks.api.listGlobalDeployments.mock.calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(mocks.api.listGlobalDeployments).toHaveBeenCalledTimes(beforeUnmount);
  });
});
