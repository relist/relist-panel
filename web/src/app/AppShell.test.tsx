// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from 'antd';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { SWRConfig } from 'swr';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../data/api';
import { PanelProvider } from '../data/store';
import { AppShell } from './AppShell';

afterEach(() => { vi.restoreAllMocks(); });

describe('Deployments navigation', () => {
  it('shows the queued count and opens the global page with selected menu and breadcrumb', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({ authenticated: true, setupRequired: false });
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
  });
});
