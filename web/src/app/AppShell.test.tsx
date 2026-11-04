// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../data/api';
import { PanelProvider } from '../data/store';
import { AppShell } from './AppShell';

afterEach(() => { vi.restoreAllMocks(); });
beforeEach(() => {
  vi.spyOn(api, 'hostMetrics').mockResolvedValue({
    window: '15m', from: 'ring', intervalSeconds: 10, partial: true,
    sources: { diskSpace: { available: true } }, samples: [{ at: Date.now(), diskUsed: 800, diskTotal: 1000 } as never],
  });
});

describe('Deployments navigation', () => {
  it('opens mobile navigation over the page and closes it from the backdrop or navigation', async () => {
    const originalMatchMedia = window.matchMedia;
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({ ...originalMatchMedia(query), matches: query.includes('max-width: 991.98px') }));
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'admin@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([]);
    const router = createMemoryRouter([{ element: <AppShell />, children: [
      { path: '/', element: <h1>Home</h1> },
      { path: '/monitoring', element: <h1>Host monitoring</h1> },
    ] }]);
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('button', { name: 'Open navigation' }));
    expect(screen.getByRole('heading', { name: 'Home' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Close navigation backdrop' }));
    expect(screen.getByRole('button', { name: 'Open navigation' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Open navigation' }));
    await user.click(screen.getByRole('menuitem', { name: /Monitoring/ }));
    expect(await screen.findByRole('heading', { name: 'Host monitoring' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Open navigation' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: 'Open navigation' }));
    await user.click(screen.getByRole('link', { name: 'Open Monitoring' }));
    expect(screen.getByRole('button', { name: 'Open navigation' })).toBeTruthy();
  });

  it('shows the queued count and opens the global page with selected menu and breadcrumb', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'admin@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([
      { status: 'running' } as never, { status: 'queued' } as never, { status: 'queued' } as never,
    ]);
    const router = createMemoryRouter([{ element: <AppShell />, children: [
      { path: '/', element: <h1>Home</h1> },
      { path: '/deployments', element: <h1>All deployments</h1> },
    ] }]);
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    const item = await screen.findByRole('menuitem', { name: /Deployments/ });
    expect(await within(item).findByText('2')).toBeTruthy();
    await userEvent.setup().click(item);
    expect(await screen.findByRole('heading', { name: 'All deployments' })).toBeTruthy();
    expect(router.state.location.pathname).toBe('/deployments');
    expect(item.className).toContain('ant-menu-item-selected');
    expect(screen.getByText('Deployments', { selector: '.ant-breadcrumb-link' })).toBeTruthy();
    expect(api.globalDeploymentQueue).toHaveBeenCalledTimes(1);
    expect(screen.getByText('Panel data disk is nearly full')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open Monitoring' }).getAttribute('href')).toBe('/monitoring');
    expect(api.hostMetrics).toHaveBeenCalledExactlyOnceWith('15m');
  });
  it('shows the host Critical count on Vulnerabilities and hides it when scanning is off', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'admin@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([]);
    const summary = vi.spyOn(api, 'vulnSummary').mockResolvedValue({
      enabled: true, counts: { critical: 4, high: 9, medium: 0, low: 0, unknown: 0, fixable: 2 },
    });
    const router = createMemoryRouter([{ element: <AppShell />, children: [
      { path: '/', element: <h1>Home</h1> },
      { path: '/vulnerabilities', element: <h1>All vulnerabilities</h1> },
    ] }]);
    const { unmount } = render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    const item = await screen.findByRole('menuitem', { name: /Vulnerabilities/ });
    expect(await within(item).findByText('4')).toBeTruthy();
    await userEvent.setup().click(item);
    expect(await screen.findByRole('heading', { name: 'All vulnerabilities' })).toBeTruthy();
    expect(item.className).toContain('ant-menu-item-selected');
    expect(screen.getByText('Vulnerabilities', { selector: '.ant-breadcrumb-link' })).toBeTruthy();
    unmount();

    summary.mockResolvedValue({ enabled: false });
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={createMemoryRouter([{ element: <AppShell />, children: [{ path: '/', element: <h1>Home</h1> }] }])} /></PanelProvider></App></SWRConfig>);
    const quiet = await screen.findByRole('menuitem', { name: /Vulnerabilities/ });
    await waitFor(() => expect(summary).toHaveBeenCalledTimes(2));
    expect(within(quiet).queryByText('4')).toBeNull();
    expect(quiet.querySelector('.ant-badge-count')).toBeNull();
  });
});

describe('Panel version footer', () => {
  it('shows the running version under the sider', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'admin@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([]);
    vi.spyOn(api, 'panelUpdateStatus').mockResolvedValue({ version: '0.1.0-rc.1', development: false, available: null } as never);
    const router = createMemoryRouter([{ element: <AppShell />, children: [{ path: '/', element: <h1>Home</h1> }] }]);
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    const link = await screen.findByRole('link', { name: 'Relist Panel 0.1.0-rc.1' });
    expect(link.getAttribute('href')).toBe('/panel-update');
  });
});

describe('Users and non-admins', () => {
  const member = { id: 3, email: 'bob@example.com', isAdmin: false, isOwner: false };

  function renderShell(path: string) {
    const router = createMemoryRouter(
      [{ element: <AppShell />, children: [
        { path: '/', element: <h1>Home</h1> },
        { path: '/account', element: <h1>Account screen</h1> },
        { path: '/projects', element: <h1>Projects screen</h1> },
        { path: '/docker', element: <h1>Docker screen</h1> },
      ] }],
      { initialEntries: [path] },
    );
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    return router;
  }

  beforeEach(() => {
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'bob@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([]);
    vi.spyOn(api, 'vulnSummary').mockResolvedValue({ enabled: false } as never);
    vi.spyOn(api, 'panelUpdateStatus').mockResolvedValue({} as never);
  });

  it('shows a member Projects and Deployments only, and lands them on Projects', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member, access: { projects: [] } });
    const router = renderShell('/');
    expect(await screen.findByRole('heading', { name: 'Projects screen' })).toBeTruthy();
    expect(router.state.location.pathname).toBe('/projects');
    const items = screen.getAllByRole('menuitem').map((item) => item.textContent);
    expect(items).toEqual(['Projects', 'Deployments']);
  });

  it('still opens the Account screen for a member', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member, access: { projects: [] } });
    renderShell('/account');
    expect(await screen.findByRole('heading', { name: 'Account screen' })).toBeTruthy();
  });

  it('shows a member the 403 result on a host-wide page', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member, access: { projects: [] } });
    renderShell('/docker');
    expect(await screen.findByText("You don't have permission")).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Docker screen' })).toBeNull();
  });

  it('loads the tree and the queue for a member, and makes no admin-only request', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member, access: { projects: [] } });
    renderShell('/projects');
    await screen.findByRole('heading', { name: 'Projects screen' });
    await waitFor(() => expect(api.listProjects).toHaveBeenCalled());
    await waitFor(() => expect(api.globalDeploymentQueue).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(api.hostMetrics).not.toHaveBeenCalled();
    expect(api.vulnSummary).not.toHaveBeenCalled();
    expect(api.panelUpdateStatus).not.toHaveBeenCalled();
  });

  it('shows a member no host disk card, and their own initial', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member, access: { projects: [] } });
    renderShell('/projects');
    await screen.findByRole('heading', { name: 'Projects screen' });
    expect(screen.queryByText('Docker host')).toBeNull();
    expect(within(screen.getByRole('button', { name: 'Account' })).getByText('B')).toBeTruthy();
  });

  it('shows an admin the Users menu item', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: { ...member, isAdmin: true } });
    renderShell('/');
    expect(await screen.findByRole('menuitem', { name: /Users/ })).toBeTruthy();
  });
});

describe('Mobile navigation trigger', () => {
  it('sits in the header bar, clear of the page title, and the header leaves room for it', async () => {
    const originalMatchMedia = window.matchMedia;
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({ ...originalMatchMedia(query), matches: query.includes('max-width: 991.98px') }));
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true });
    vi.spyOn(api, 'account').mockResolvedValue({ email: 'admin@example.com', passwordFromEnv: false, twoFactorEnabled: false });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    vi.spyOn(api, 'globalDeploymentQueue').mockResolvedValue([]);
    const router = createMemoryRouter([{ element: <AppShell />, children: [{ path: '/', element: <h1>Home</h1> }] }]);
    render(<SWRConfig value={{ provider: () => new Map() }}><App><PanelProvider><RouterProvider router={router} /></PanelProvider></App></SWRConfig>);
    const trigger = (await screen.findByRole('button', { name: 'Open navigation' })).closest('.ant-layout-sider-zero-width-trigger') as HTMLElement;
    // The header is 56px tall and the trigger 40px: centred in it, never below it.
    expect(trigger.style.top).toBe('8px');
    const header = document.querySelector('.ant-layout-header') as HTMLElement;
    expect(header.style.paddingInlineStart).toBe('56px');
  });
});
