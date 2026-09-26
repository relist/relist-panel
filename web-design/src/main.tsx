/**
 * The scene harness is imported first, and the order is load-bearing rather than
 * cosmetic. `data/volumeMocks.ts` calls `mtime()` at module scope, through
 * `file()`/`dir()`/`symlink()`, so the Volume Explorer's fixture timestamps are
 * computed while the module graph is still evaluating — before any statement of
 * this file runs. Imported first, `app/scene` has installed the frozen `?clock=`
 * (and the `?ui=` overrides, and the `?scene=` id) by the time the fixtures read
 * the wall clock. A clock installed anywhere later — in this file's body, in a
 * provider, in an effect — is already too late for that one call site.
 */
import './app/scene';

import { StyleProvider } from '@ant-design/cssinjs';
import { App as AntApp, ConfigProvider } from 'antd';
import { StrictMode, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import './monaco';

import { AppShell } from './app/AppShell';
import { RouteContext, paths, useRoute } from './app/router';
import { sceneAfter, sceneOverrides } from './app/scene';
import { getSceneAdapter, SceneAdapterBridge } from './app/sceneAdapters';
import { SceneReadiness } from './app/sceneReady';
import { buildTheme, initialThemeMode, ThemeModeContext, ThemeModeControlContext } from './app/theme';
import type { ThemeMode } from './app/theme';
import { useUiState } from './app/uiState';
import { PanelProvider, useAuthMock } from './data/store';
import { SceneScopeProvider } from './scenes/scope';
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

  // TEMP — removed by ticket 06. Dev-only harness for ticket 10: proves two
  // `SceneScopeProvider` frames in one document do not leak (route, toast,
  // Monaco models, error boundary). Not a screen; not in the router's union.
  if (import.meta.env.DEV && route.kind === 'not-found' && route.path === '/design/scope-dev') {
    return <ScopeDevHarness />;
  }

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

/**
 * The standalone `?after=` runner. A board frame runs its scene's named
 * transition through the scoped AfterRunner (`scenes/scope`); an *Open live*
 * link has no frame, so without this the `after:` scenes opened as the plain
 * screen. One run per mount — the ref guard keeps StrictMode's double effect
 * from firing the transition twice — and it sits after `PanelProvider` in the
 * tree, so its passive effect runs after the screens' own mount effects and
 * after the bridge's.
 */
function StandaloneAfterRunner() {
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;
    const name = sceneAfter();
    if (name === null) return;
    try {
      getSceneAdapter(name)?.();
    } catch (error) {
      // `getSceneAdapter` throws in dev on a name nothing registered; there is
      // no frame error boundary to catch it here, so this is the loud form.
      console.error(error);
    }
  }, []);

  return null;
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
            {/* The adapters' two standalone seams (ticket 02). The bridge
                publishes this `<AntApp>`'s message/notification handles for the
                toast adapters; the runner drives a `?after=` name once, mounted
                *after* `PanelProvider` so every screen-published handle is in
                place when an adapter reads it. Both render nothing, and with no
                `?after=` the runner is a no-op. */}
            <SceneAdapterBridge />
            <PanelProvider overrides={sceneOverrides()}>
              {/* Publishes `data-scene-ready` for the capture pipeline; renders nothing. */}
              <SceneReadiness />
              {bare ? (
                <Screens onSignIn={auth.signIn} />
              ) : (
                <AppShell onToggleTheme={() => setMode((current) => (current === 'light' ? 'dark' : 'light'))} themeMode={mode}>
                  <Screens onSignIn={auth.signIn} />
                </AppShell>
              )}
            </PanelProvider>
            <StandaloneAfterRunner />
          </AntApp>
        </ConfigProvider>
      </ThemeModeContext.Provider>
    </ThemeModeControlContext.Provider>
  );
}

// TEMP — removed by ticket 06. The per-frame isolation harness (#/design/scope-dev, dev only).
//
// Two `SceneScopeProvider` frames of the *same* Compose, in one document, on purpose: shared
// route, shared store module, shared Monaco — everything a board of a hundred frames would
// share. What must not leak between them:
//
//   A · `dev.compose-a` — forced `ui` value, sticky toast, frozen clock, and a probe that
//       navigates the frame to the Monitor tab on a timer.
//   B · `dev.compose-b` — no forced state, and a nested component that throws on a timer.
//
// Expected, at a glance: A shows the Monitor tab and its own toast while B stays on the Editor
// tab with none; the two Monaco models carry distinct `scene:<id>:` prefixes for the same file;
// after the timer, B is a red card and A is untouched; every overlay (toast, modal, dropdown)
// stays inside its own frame's box.

/** A frame-local readout of the scoped route and the forced `ui` value, plus A's timed navigation. */
function ScopeDevProbe({ label, navigateTo }: { label: string; navigateTo?: string }) {
  const routed = useContext(RouteContext);
  const [forced] = useUiState('dev.forced', 'seed');

  useEffect(() => {
    if (routed === undefined || navigateTo === undefined) return undefined;
    const timer = window.setTimeout(() => routed.navigate(navigateTo), 1_500);
    return () => window.clearTimeout(timer);
  }, [navigateTo, routed]);

  return (
    <div
      data-scope-probe={label}
      style={{ background: '#FFFBE6', border: '1px solid #F0E6A8', fontSize: 12, padding: '4px 8px' }}
    >
      frame {label} · route <code data-scope-route>{routed?.path ?? '—'}</code> · ui.dev.forced=<code data-scope-forced>{forced}</code>
    </div>
  );
}

/** Throws from render, on a timer, so the frame's error boundary replaces it after boot. */
function ScopeDevBomb({ delayMs }: { delayMs: number }) {
  const [boom, setBoom] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => setBoom(true), delayMs);
    return () => window.clearTimeout(timer);
  }, [delayMs]);

  if (boom) {
    throw new Error('ScopeDevBomb: this frame threw on schedule; every other frame must be untouched.');
  }
  return null;
}

const SCOPE_DEV_COMPOSE = { composeId: 1, environmentId: 1, projectId: 1 } as const;

function ScopeDevHarness() {
  return (
    <div style={{ background: '#F0F0F0', display: 'flex', gap: 32, minHeight: '100vh', padding: 32 }}>
      <SceneScopeProvider
        clock="2026-09-26T09:00:00Z"
        height={900}
        id="dev.compose-a"
        route={paths.compose(SCOPE_DEV_COMPOSE.projectId, SCOPE_DEV_COMPOSE.environmentId, SCOPE_DEV_COMPOSE.composeId)}
        toast={{ content: 'Frame A sticky toast — visible only in this frame', key: 'dev-a', type: 'success' }}
        ui={new Map([['dev.forced', 'from-scope-A']])}
        width={780}
      >
        <ScopeDevProbe
          label="A"
          navigateTo={paths.compose(
            SCOPE_DEV_COMPOSE.projectId,
            SCOPE_DEV_COMPOSE.environmentId,
            SCOPE_DEV_COMPOSE.composeId,
            'monitor',
          )}
        />
        <ComposePage
          composeId={SCOPE_DEV_COMPOSE.composeId}
          environmentId={SCOPE_DEV_COMPOSE.environmentId}
          projectId={SCOPE_DEV_COMPOSE.projectId}
        />
      </SceneScopeProvider>

      <SceneScopeProvider
        clock="2026-09-26T09:00:00Z"
        height={1200}
        id="dev.compose-b"
        route={paths.compose(SCOPE_DEV_COMPOSE.projectId, SCOPE_DEV_COMPOSE.environmentId, SCOPE_DEV_COMPOSE.composeId)}
        width={780}
      >
        <ScopeDevProbe label="B" />
        <ScopeDevBomb delayMs={6_000} />
        <ComposePage
          composeId={SCOPE_DEV_COMPOSE.composeId}
          environmentId={SCOPE_DEV_COMPOSE.environmentId}
          projectId={SCOPE_DEV_COMPOSE.projectId}
        />
      </SceneScopeProvider>
    </div>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <Root />
    </StyleProvider>
  </StrictMode>,
);
