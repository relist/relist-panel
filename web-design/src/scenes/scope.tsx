import { Component, createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties, ReactNode } from 'react';
import { App as AntApp, ConfigProvider } from 'antd';

import { parseClockInstant } from '../app/scene';
import { getSceneAdapter } from '../app/sceneAdapters';
import { useFontsReady } from '../app/sceneReady';
import { RouteContext, currentScopeNavigate, parse, paths, setScopeNavigate } from '../app/router';
import type { RoutedValue } from '../app/router';
import { UiOverridesContext } from '../app/uiState';
import type { UiValue } from '../app/uiState';
import { SceneClockContext } from '../app/useNow';
import { PanelProvider, usePanel } from '../data/store';
import type { SceneOverrides } from '../data/store';

/**
 * One frame of the board, isolated from every other frame in the same document
 * (ticket 10). `SceneScopeProvider` takes everything ticket 01's harness reads
 * from the URL — a route, forced `ui` state, a frozen clock, store overrides,
 * a named `after:` transition, a scene id — and provides it through context to
 * exactly the subtree it wraps. Standalone the providers are absent and every
 * harness module falls back to its read-once module behaviour, unchanged.
 *
 * What a scope carries, and where it lands:
 *
 *  - **Route** — `RouteContext` feeds `useRoute`/`useActivePath`; the scope
 *    also *claims* the plain `navigate` (see `app/router.ts`) so the one
 *    mount-time redirect a screen makes is absorbed into the frame.
 *  - **Forced state** — `UiOverridesContext` is consulted by `useUiState`
 *    before the module `?ui=` map.
 *  - **Clock** — `SceneClockContext` freezes `useNow`, and the frame's own
 *    `PanelProvider` seeds its timestamps from the same instant.
 *  - **Feedback** — a nested `<AntApp>` makes every `App.useApp()` toast
 *    frame-local with zero call-site changes, and `ConfigProvider
 *    getPopupContainer` lands Dropdown/Select/Tooltip/Popconfirm portals in
 *    the frame box. Modal/Drawer follow `useSceneContainer()` per call site.
 *  - **Containment** — the frame box is an explicit-size, `overflow: hidden`,
 *    `contain: paint` element, so the fixed-position overlays above anchor to
 *    and clip within the frame instead of the board; `--frame-vh` makes the
 *    handful of `vh`-based declarations resolve against the frame's height;
 *    the outer wrapper is `pointer-events: none` — a frame is static, and the
 *    board re-enables events for its own chrome only.
 *  - **Monaco** — `useSceneModelPrefix()` namespaces model paths per frame.
 *  - **Readiness** — `data-scene-ready`/`data-scene-id` land on the frame's
 *    own element, so the board can assert per frame.
 *  - **Failure** — a frame that throws is a red card naming its scene id,
 *    never a blank board.
 */

const IS_DEV = import.meta.env.DEV;

/** Per-frame identity, the frame box, and the Monaco namespace derived from it. */
type SceneScopeValue = {
  /** The frame box element — portals, popups and `--frame-vh` resolve against it. */
  container: HTMLElement | null;
  id: string;
  /** Monaco model-path prefix: every model this frame creates is namespaced by it. */
  modelPrefix: string;
};

const SceneScopeContext = createContext<SceneScopeValue | undefined>(undefined);

/**
 * The element Modal/Drawer portals should land in: the frame box inside a
 * scope, `undefined` outside — which is antd's default (the document body),
 * so standalone call sites behave exactly as before.
 */
export function useSceneContainer(): HTMLElement | undefined {
  const scope = useContext(SceneScopeContext);
  return scope?.container ?? undefined;
}

/** Prefix for Monaco model paths: `scene:<id>:` inside a scope, `''` outside. */
export function useSceneModelPrefix(): string {
  const scope = useContext(SceneScopeContext);
  return scope?.modelPrefix ?? '';
}

/** A sticky announcement a frame carries from mount: `message.open({ duration: 0 })`. */
export type SceneToast = {
  content: ReactNode;
  key?: string;
  type?: 'error' | 'info' | 'loading' | 'success' | 'warning';
};

export function SceneScopeProvider({
  after,
  children,
  clock,
  height = 1000,
  id,
  overrides,
  route = paths.overview,
  toast,
  ui,
  width = 1600,
}: {
  /** A named `after:` transition (registered in `app/sceneAdapters.ts`), run once on mount. */
  after?: string;
  children: ReactNode;
  /** ISO instant, same grammar as `?clock=`. Freezes `useNow` and the store's seeds. */
  clock?: string;
  /** Frame height in px; sets `--frame-vh` (`height / 100`). Defaults to the designed 1000. */
  height?: number;
  /** The scene id — readiness echoes it back, the error card names it, Monaco models take it. */
  id: string;
  /** Storage-level seed for the frame's own `PanelProvider`. */
  overrides?: SceneOverrides;
  /** Initial route, hash-shaped (`#/…`). Read once, like the standalone `?ui=` map. */
  route?: string;
  /** A sticky toast fired through the frame's own `AntApp` context once mounted. */
  toast?: SceneToast;
  /** Forced screen state, consulted by `useUiState` before the module `?ui=` map. */
  ui?: ReadonlyMap<string, UiValue>;
  /** Frame width in px. Defaults to the designed 1600. */
  width?: number;
}) {
  // The frame box, known to React a render after mount — which is the earliest
  // anything can portal into it anyway. The ref (unlike the state) is current
  // inside the first commit's effects, so a portal that opens that early — a
  // sticky toast does — still lands in the box instead of the document body.
  const boxRef = useRef<HTMLElement | null>(null);
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const attachContainer = useCallback((element: HTMLElement | null) => {
    boxRef.current = element;
    setContainer(element);
  }, []);

  // The frame's route is plain state: the read happens once at mount, exactly
  // like the standalone harness reads the URL once.
  const [path, setPath] = useState(route);
  const navigateScoped = useCallback((to: string) => setPath(to), []);

  // Claim plain `navigate` for this frame. A layout effect, deliberately: it
  // runs before *any* of the commit's passive effects, so a screen mounted with
  // a stale route absorbs its own redirect (ComposePage's is the one) instead
  // of moving the page. Unmounting releases the claim only if this frame still
  // holds it.
  useLayoutEffect(() => {
    setScopeNavigate(navigateScoped);
    return () => {
      if (currentScopeNavigate() === navigateScoped) {
        setScopeNavigate(null);
      }
    };
  }, [navigateScoped]);

  const routeValue = useMemo<RoutedValue>(() => ({ navigate: navigateScoped, path, route: parse(path) }), [navigateScoped, path]);

  const instant = useMemo(() => {
    if (clock === undefined) return undefined;
    const parsed = parseClockInstant(clock);
    if (parsed === null && IS_DEV) {
      console.warn(`[scene] clock="${clock}" is not a parseable instant; frame "${id}" keeps the wall clock.`);
    }
    return parsed ?? undefined;
  }, [clock, id]);

  const uiMap = useMemo(() => (ui === undefined ? undefined : new Map(ui)), [ui]);

  const scopeValue = useMemo<SceneScopeValue>(() => ({ container, id, modelPrefix: `scene:${id}:` }), [container, id]);

  return (
    <div data-scene-scope={id} style={{ pointerEvents: 'none' }}>
      <div
        data-scene-id={id}
        ref={attachContainer}
        style={
          {
            '--frame-vh': `${height / 100}px`,
            contain: 'paint',
            height,
            overflow: 'hidden',
            position: 'relative',
            width,
          } as CSSProperties
        }
      >
        <SceneErrorBoundary sceneId={id}>
          {/* `message.getContainer` is what actually hosts a frame's toasts (the
              nested App's holder otherwise portals to `document.body`, outside
              every frame); `getPopupContainer` covers Dropdown/Select/Tooltip/
              Popconfirm. Both read the ref, not the state: a toast opens in the
              mount commit, before the container state has caught up. No `theme`
              here: a frame wears whatever the outer ConfigProvider wears, so the
              board's theme switch flips every frame at once. */}
          <AntApp message={{ getContainer: () => boxRef.current ?? document.body }}>
            <ConfigProvider getPopupContainer={() => boxRef.current ?? document.body}>
              <PanelProvider clock={instant} overrides={overrides}>
                <SceneScopeContext.Provider value={scopeValue}>
                  <UiOverridesContext.Provider value={uiMap}>
                    <SceneClockContext.Provider value={instant}>
                      <RouteContext.Provider value={routeValue}>
                        <ScopedReadiness container={container} id={id} />
                        <AfterRunner after={after} navigate={navigateScoped} />
                        <ToastRunner toast={toast} />
                        {children}
                      </RouteContext.Provider>
                    </SceneClockContext.Provider>
                  </UiOverridesContext.Provider>
                </SceneScopeContext.Provider>
              </PanelProvider>
            </ConfigProvider>
          </AntApp>
        </SceneErrorBoundary>
      </div>
    </div>
  );
}

/**
 * Publishes the frame's readiness on the frame's own element: `data-scene-ready="true"`
 * plus `data-scene-id` once the store's `pending` is empty and the fonts are in.
 * The attribute is *withdrawn* while anything is pending, exactly like the
 * standalone publisher (`app/sceneReady.ts`) — that logic is shared, and
 * standalone keeps writing `<html>`.
 *
 * `data-scene-id` is also on the element statically: the board must be able to
 * *find* a frame before it is ready (to deep-link to it), so readiness never
 * takes the id away.
 */
function ScopedReadiness({ container, id }: { container: HTMLElement | null; id: string }) {
  const { pending } = usePanel();
  const fontsReady = useFontsReady();
  const ready = fontsReady && Object.keys(pending).length === 0;

  useEffect(() => {
    if (container === null) return undefined;
    if (ready) {
      container.setAttribute('data-scene-ready', 'true');
      container.setAttribute('data-scene-id', id);
    } else {
      container.removeAttribute('data-scene-ready');
    }
    return undefined;
  }, [container, id, ready]);

  return null;
}

/**
 * Runs the frame's `after:` adapter once per mount. StrictMode's double
 * effects would run a transition twice; the guard is here so no adapter has to
 * be written defensively. The frame's `navigate` is claimed for the duration
 * of the (synchronous) call, so an adapter that navigates lands in *this*
 * frame no matter how many siblings rendered last.
 */
function AfterRunner({ after, navigate }: { after?: string; navigate: (to: string) => void }) {
  const ran = useRef(false);

  useEffect(() => {
    if (after === undefined || ran.current) return;
    ran.current = true;
    const adapter = getSceneAdapter(after);
    if (adapter === undefined) return;
    const previous = setScopeNavigate(navigate);
    try {
      adapter();
    } finally {
      setScopeNavigate(previous);
    }
  }, [after, navigate]);

  return null;
}

/**
 * The frame's sticky toast, fired through the *nested* `AntApp`'s context —
 * so the message holder is a child of this frame's box, and a toast fired in
 * one frame is invisible everywhere else. `duration: 0` holds it; unmounting
 * the frame (culling on the board) takes it away.
 */
function ToastRunner({ toast }: { toast?: SceneToast }) {
  const { message } = AntApp.useApp();

  useEffect(() => {
    if (toast === undefined) return undefined;
    message.open({ content: toast.content, duration: 0, key: toast.key ?? 'scene-toast', type: toast.type });
    return undefined;
  }, [message, toast]);

  return null;
}

/**
 * A frame that throws is a red card naming its scene, bounded by the frame
 * box: the canvas around it keeps every other frame. Plain HTML on purpose —
 * the boundary must survive whatever broke, antd included.
 */
class SceneErrorBoundary extends Component<{ children: ReactNode; sceneId: string }, { error: Error | null }> {
  override state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error): { error: Error } {
    return { error };
  }

  override componentDidCatch(error: Error): void {
    if (IS_DEV) {
      console.error(`[scene] frame "${this.props.sceneId}" crashed:`, error);
    }
  }

  override render() {
    const { error } = this.state;
    if (error === null) {
      return this.props.children;
    }

    const card: CSSProperties = {
      alignItems: 'center',
      border: '2px solid #CF1322',
      boxSizing: 'border-box',
      color: '#CF1322',
      display: 'flex',
      flexDirection: 'column',
      gap: 8,
      height: '100%',
      justifyContent: 'center',
      padding: 24,
    };

    return (
      <div style={card}>
        <div style={{ fontSize: 16, fontWeight: 600 }}>Scene “{this.props.sceneId}” crashed</div>
        <div style={{ fontSize: 13 }}>{error.message}</div>
      </div>
    );
  }
}
