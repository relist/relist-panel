import { StyleProvider } from '@ant-design/cssinjs';
import { App as AntApp, ConfigProvider } from 'antd';
import { StrictMode, useEffect, useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import './monaco';

import { AppShell } from './app/AppShell';
import { useRoute } from './app/router';
import { buildTheme, initialThemeMode, ThemeModeContext, ThemeModeControlContext } from './app/theme';
import type { ThemeMode } from './app/theme';
import { PanelProvider, useAuthMock } from './data/store';
import { NotFoundPage, SetupPage, SignInPage } from './pages/AuthPages';
import { ComposePage } from './pages/ComposePage';
import { GuidelinesPage } from './pages/GuidelinesPage';
import { MonitoringPage } from './pages/MonitoringPage';
import { OverviewPage } from './pages/OverviewPage';
import { ProjectPage } from './pages/ProjectPage';
import { ProjectsPage } from './pages/ProjectsPage';
import { ScreensGalleryPage } from './pages/ScreensGalleryPage';
import { StatesGalleryPage } from './pages/StatesGalleryPage';
import { VolumeExplorerPage } from './pages/VolumeExplorerPage';

function Screens({ onSignIn }: { onSignIn: () => void }) {
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

function Root() {
  const [mode, setMode] = useState<ThemeMode>(initialThemeMode);
  const route = useRoute();
  const auth = useAuthMock();

  /**
   * The screens gallery drives this document's mode from the outside, because it
   * renders every screen as an iframe and one control has to flip all of them.
   * The receiver checks the shape rather than trusting the sender: the payload
   * is one word, and a stray message would be a silent theme change.
   */
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      const data = event.data as { mode?: unknown; type?: unknown } | null;
      if (data?.type === 'relist:theme' && (data.mode === 'light' || data.mode === 'dark')) {
        setMode(data.mode);
      }
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, []);

  const themeControl = useMemo(() => ({ mode, setMode }), [mode]);

  // Mock auth: the prototype starts signed in so every screen is one click away.
  // Signing out (user menu) or opening #/signin shows the auth screens.
  const signedIn = auth.signedIn;
  const showAuth = route.kind === 'signin' || route.kind === 'setup' || !signedIn;
  /**
   * The gallery is the one screen that renders without the shell: the shell is
   * part of what each of its frames is showing, so wrapping it would nest one
   * panel inside another.
   */
  const bare = showAuth || route.kind === 'screensGallery';

  return (
    <ThemeModeControlContext.Provider value={themeControl}>
      <ThemeModeContext.Provider value={mode}>
        <ConfigProvider theme={buildTheme(mode)}>
          <AntApp>
            <PanelProvider>
              {bare ? (
                <Screens onSignIn={auth.signIn} />
              ) : (
                <AppShell onToggleTheme={() => setMode((current) => (current === 'light' ? 'dark' : 'light'))} themeMode={mode}>
                  <Screens onSignIn={auth.signIn} />
                </AppShell>
              )}
            </PanelProvider>
          </AntApp>
        </ConfigProvider>
      </ThemeModeContext.Provider>
    </ThemeModeControlContext.Provider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <Root />
    </StyleProvider>
  </StrictMode>,
);
