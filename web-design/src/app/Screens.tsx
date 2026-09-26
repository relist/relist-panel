/**
 * The route→page switch, factored out of `main.tsx` (ticket 06) so it has
 * exactly two consumers: the standalone `Root`, where `useRoute` reads the
 * window hash, and a board frame, where `BoardPage` renders this inside a
 * `SceneScopeProvider` and `useRoute` reads the scope's own route. One switch,
 * so a new route cannot land in one mount and not the other.
 *
 * The still board is deliberately not a case here: `Root` handles
 * `route.kind === 'board'` before this component renders (the board is bare,
 * and a board inside a board frame would need a scene to declare
 * `paths.board`, which the registry does not express).
 */

import { NotFoundPage, SetupPage, SignInPage } from '../pages/AuthPages';
import { ComposePage } from '../pages/ComposePage';
import { GuidelinesPage } from '../pages/GuidelinesPage';
import { MonitoringPage } from '../pages/MonitoringPage';
import { OverviewPage } from '../pages/OverviewPage';
import { ProjectPage } from '../pages/ProjectPage';
import { ProjectsPage } from '../pages/ProjectsPage';
import { ScreensGalleryPage } from '../pages/ScreensGalleryPage';
import { StatesGalleryPage } from '../pages/StatesGalleryPage';
import { VolumeExplorerPage } from '../pages/VolumeExplorerPage';
import { useRoute } from './router';

export function Screens({ onSignIn }: { onSignIn: () => void }) {
  const route = useRoute();

  switch (route.kind) {
    case 'overview':
      return <OverviewPage />;
    case 'projects':
      return <ProjectsPage />;
    case 'monitoring':
      return <MonitoringPage />;
    case 'project':
      return <ProjectPage environmentId={route.environmentId} projectId={route.projectId} tab={route.tab} />;
    case 'compose':
      return (
        <ComposePage
          composeId={route.composeId}
          environmentId={route.environmentId}
          projectId={route.projectId}
          tab={route.tab}
        />
      );
    case 'volumeExplorer':
      return (
        <VolumeExplorerPage
          composeId={route.composeId}
          environmentId={route.environmentId}
          path={route.path}
          projectId={route.projectId}
          volumeId={route.volumeId}
        />
      );
    case 'guidelines':
      return <GuidelinesPage />;
    case 'states':
      return <StatesGalleryPage />;
    case 'screensGallery':
      return <ScreensGalleryPage />;
    case 'setup':
      return <SetupPage onComplete={onSignIn} />;
    case 'signin':
      return <SignInPage onSignIn={onSignIn} />;
    default:
      return <NotFoundPage />;
  }
}
