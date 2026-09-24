import type { ReactElement } from 'react';
import { createBrowserRouter } from 'react-router';

import { ComposePage } from '../pages/ComposePage';
import { MonitoringPage } from '../pages/MonitoringPage';
import { NotFoundPage } from '../pages/NotFoundPage';
import { OverviewPage } from '../pages/OverviewPage';
import { ProjectPage } from '../pages/ProjectPage';
import { ProjectsPage } from '../pages/ProjectsPage';
import { AppShell } from './AppShell';
import { routePaths } from './paths';

/**
 * The routed app tree: one screen per pattern in `routePaths`, all under the
 * shell so every screen shares the sider, the header and the content column.
 * An unknown path renders the design's not-found inside the shell.
 */

const elements: Record<string, ReactElement> = {
  '/': <OverviewPage />,
  'monitoring': <MonitoringPage />,
  'projects': <ProjectsPage />,
  'projects/:projectId': <ProjectPage />,
  'projects/:projectId/env/:environmentId/:tab': <ProjectPage />,
  'projects/:projectId/env/:environmentId/compose/:composeId': <ComposePage />,
  'projects/:projectId/env/:environmentId/compose/:composeId/:tab': <ComposePage />,
};

// Mounted at Vite's base (`/` in the shipped panel). Honouring it — with the
// trailing slash dropped, which is the form `stripBasename` matches against —
// lets the same bundle render under a sub-path prefix; it is a no-op at `/`.
const basename = import.meta.env.BASE_URL.replace(/\/+$/, '') || '/';

export const router = createBrowserRouter(
  [
    {
      element: <AppShell />,
      children: [
        ...routePaths.map((route) =>
          route.path === '/' ? { index: true as const, element: elements[route.path] } : { path: route.path, element: elements[route.path] },
        ),
        { path: '*', element: <NotFoundPage /> },
      ],
    },
  ],
  { basename },
);
