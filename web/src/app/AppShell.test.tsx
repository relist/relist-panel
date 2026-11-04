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

  it('shows a non-admin no menu items and the no-access page', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member });
    renderShell('/projects');
    expect(await screen.findByText('No access yet')).toBeTruthy();
    expect(screen.getByText('An admin has not given you access to any Project.')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'Projects screen' })).toBeNull();
    expect(screen.queryAllByRole('menuitem')).toHaveLength(0);
  });

  it('still opens the Account screen for a non-admin', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member });
    renderShell('/account');
    expect(await screen.findByRole('heading', { name: 'Account screen' })).toBeTruthy();
  });

  it('makes no admin-only request for a non-admin', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: member });
    renderShell('/');
    await screen.findByText('No access yet');
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(api.listProjects).not.toHaveBeenCalled();
    expect(api.globalDeploymentQueue).not.toHaveBeenCalled();
    expect(api.hostMetrics).not.toHaveBeenCalled();
    expect(api.vulnSummary).not.toHaveBeenCalled();
    expect(api.panelUpdateStatus).not.toHaveBeenCalled();
  });

  it('shows an admin the Users menu item', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, user: { ...member, isAdmin: true } });
    renderShell('/');
    expect(await screen.findByRole('menuitem', { name: /Users/ })).toBeTruthy();
  });
});
