// @vitest-environment jsdom
import '../test/dom-setup';

import { render, screen, waitFor } from '@testing-library/react';
import { App } from 'antd';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api } from '../data/api';
import { PanelProvider } from '../data/store';
import { ProjectsPage } from './ProjectsPage';

afterEach(() => { vi.restoreAllMocks(); });

describe('Roles on the Projects page', () => {
  it('keeps New Project for admins', async () => {
    vi.spyOn(api, 'authStatus').mockResolvedValue({
      authenticated: true, user: { id: 5, email: 'm@example.com', isAdmin: false, isOwner: false }, access: { projects: [] },
    });
    vi.spyOn(api, 'listProjects').mockResolvedValue([]);
    render(<App><PanelProvider><RouterProvider router={createMemoryRouter([{ path: '/', element: <ProjectsPage /> }])} /></PanelProvider></App>);
    await screen.findByRole('button', { name: /New Project/ });
    await waitFor(() => expect((screen.getByRole('button', { name: /New Project/ }) as HTMLButtonElement).disabled).toBe(true));
  });
});
