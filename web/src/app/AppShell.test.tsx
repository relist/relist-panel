// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, within } from '@testing-library/react';
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
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, setupRequired: false });
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
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, setupRequired: false });
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
});
