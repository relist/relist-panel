import type { ReactElement } from 'react';
import { createBrowserRouter } from 'react-router';

import { SetupPage, SignInPage } from '../pages/AuthPages';
import { AccountPage } from '../pages/AccountPage';
import { ComposePage } from '../pages/ComposePage';
import { DeploymentsPage } from '../pages/DeploymentsPage';
import { DockerPage } from '../pages/DockerPage';
import { ImagesPage } from '../pages/ImagesPage';
import { MigrationPage } from '../pages/MigrationPage';
import { MonitoringPage } from '../pages/MonitoringPage';
import { NotFoundPage } from '../pages/NotFoundPage';
import { OverviewPage } from '../pages/OverviewPage';
import { ProjectPage } from '../pages/ProjectPage';
import { ProjectsPage } from '../pages/ProjectsPage';
import { VolumeExplorerPage } from '../pages/VolumeExplorerPage';
import { AppShell } from './AppShell';
import { routePaths } from './paths';
import { RequireAuth } from './RequireAuth';

/**
 * The routed app tree. The auth screens (`/signin`, `/setup`) sit outside the
 * gate and the shell — a signed-out visitor never sees the sider; the gate
 * redirects them to the entry screen the API implies, and sign-in returns them
 * to the deep link that sent them there. Everything else lives under the shell
 * so every screen shares the sider, the header and the content column; an
 * unknown path renders the design's not-found inside the shell (and, signed
 * out, the gate turns even that into the entry flow first).
 */

const elements: Record<string, ReactElement> = {
  '/': <OverviewPage />,
  'account': <AccountPage />,
  'images': <ImagesPage />,
  'docker': <DockerPage />,
  'deployments': <DeploymentsPage />,
  'monitoring': <MonitoringPage />,
  'projects': <ProjectsPage />,
  'projects/:projectId': <ProjectPage />,
  'projects/:projectId/env/:environmentId/:tab': <ProjectPage />,
  'projects/:projectId/env/:environmentId/compose/:composeId/:tab?': <ComposePage />,
  'projects/:projectId/env/:environmentId/compose/:composeId/volumes/explorer': <VolumeExplorerPage />,
};

// Mounted at Vite's base (`/` in the shipped panel). Honouring it — with the
// trailing slash dropped, which is the form `stripBasename` matches against —
// lets the same bundle render under a sub-path prefix; it is a no-op at `/`.
const basename = import.meta.env.BASE_URL.replace(/\/+$/, '') || '/';

export const router = createBrowserRouter(
  [
    {
      element: <RequireAuth />,
      children: [
        {
          element: <AppShell />,
          children: [
            ...routePaths.map((route) =>
              route.path === '/'
                ? { index: true as const, element: elements[route.path] }
                : { path: route.path, element: elements[route.path] },
            ),
            { path: '*', element: <NotFoundPage /> },
          ],
        },
      ],
    },
    { path: 'signin', element: <SignInPage /> },
    { path: 'setup', element: <SetupPage /> },
    // A server migration's new host runs before any admin exists (ADR-0018).
    { path: 'migration', element: <MigrationPage /> },
  ],
  { basename },
);
