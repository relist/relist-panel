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
import { StrictMode, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';
import './monaco';

import { AppShell } from './app/AppShell';
import { useRoute } from './app/router';
import { Screens } from './app/Screens';
import { sceneAfter, sceneOverrides } from './app/scene';
import { getSceneAdapter, SceneAdapterBridge } from './app/sceneAdapters';
import { SceneReadiness } from './app/sceneReady';
import { buildTheme, initialThemeMode, ThemeModeContext, ThemeModeControlContext } from './app/theme';
import type { ThemeMode } from './app/theme';
import { PanelProvider, useAuthMock } from './data/store';
import { BoardPage } from './pages/BoardPage';

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
   * The review surfaces render without the shell: the shell is part of what each
   * of their frames is showing, so wrapping them would nest one panel inside
   * another. The board is handled separately — it is not `Screens` at all, but
   * the board page's own canvas of scoped frames.
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
              {route.kind === 'board' ? (
                <BoardPage />
              ) : bare ? (
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

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <Root />
    </StyleProvider>
  </StrictMode>,
);
