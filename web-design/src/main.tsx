import { StyleProvider } from '@ant-design/cssinjs';
import { App as AntApp, ConfigProvider } from 'antd';
import { StrictMode, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import './monaco';

import { AppShell } from './app/AppShell';
import { useRoute } from './app/router';
import { buildTheme, ThemeModeContext } from './app/theme';
import type { ThemeMode } from './app/theme';
import { PanelProvider, useAuthMock } from './data/store';
import { NotFoundPage, SetupPage, SignInPage } from './pages/AuthPages';
import { ComposePage } from './pages/ComposePage';
import { GuidelinesPage } from './pages/GuidelinesPage';
import { MonitoringPage } from './pages/MonitoringPage';
import { OverviewPage } from './pages/OverviewPage';
import { ProjectPage } from './pages/ProjectPage';
import { ProjectsPage } from './pages/ProjectsPage';
import { StatesGalleryPage } from './pages/StatesGalleryPage';

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
    case 'guidelines':
      return <GuidelinesPage />;
    case 'states':
      return <StatesGalleryPage />;
    case 'setup':
      return <SetupPage onComplete={onSignIn} />;
    case 'signin':
      return <SignInPage onSignIn={onSignIn} />;
    default:
      return <NotFoundPage />;
  }
}

function Root() {
  const [mode, setMode] = useState<ThemeMode>('light');
  const route = useRoute();
  const auth = useAuthMock();

  // Mock auth: the prototype starts signed in so every screen is one click away.
  // Signing out (user menu) or opening #/signin shows the auth screens.
  const signedIn = auth.signedIn;
  const showAuth = route.kind === 'signin' || route.kind === 'setup' || !signedIn;

  return (
    <ThemeModeContext.Provider value={mode}>
      <ConfigProvider theme={buildTheme(mode)}>
        <AntApp>
          <PanelProvider>
            {showAuth ? (
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
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <Root />
    </StyleProvider>
  </StrictMode>,
);
