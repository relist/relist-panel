import { createContext, useContext, useEffect, useState } from 'react';

/**
 * Minimal hash router. Deliberately hand-rolled: routing library choice is not
 * part of this design pass, but every screen must be directly linkable so the
 * whole set can be reviewed and shared page by page.
 *
 * Ticket 10 makes it context-first: a `SceneScopeProvider` provides
 * `RouteContext`, and the hooks read that route when present and the window
 * hash exactly as before when not — standalone there is no provider, so the
 * behaviour below the context check is today's, untouched.
 */

export type Route =
  | { kind: 'overview' }
  | { kind: 'projects' }
  | { kind: 'monitoring' }
  | { kind: 'project'; environmentId?: number; tab?: string; projectId: number }
  | { kind: 'compose'; composeId: number; environmentId: number; projectId: number; tab?: string }
  /**
   * The Volume Explorer. Opened in its own browser tab from the Compose page's
   * Volumes tab, which is why it is a route rather than a Drawer — but it stays
   * inside the panel's own chrome, because it is a screen of the panel and not a
   * separate application.
   */
  | { kind: 'volumeExplorer'; composeId: number; environmentId: number; path: string; projectId: number; volumeId: string }
  | { kind: 'guidelines' }
  | { kind: 'states' }
  /**
   * Every screen on one page, each in its own live frame. A review surface, not
   * a screen of the panel: it renders outside `AppShell`, because the shell is
   * part of what each frame is showing.
   */
  /**
   * The still board — every registry scene on one pan/zoom canvas. A review
   * surface like the screens gallery, and bare for the same reason: the shell
   * is part of what each of its frames is showing.
   */
  | { kind: 'board' }
  /**
   * The task flows — the registry's declared links read as the journeys a
   * reviewer walks, one section per task (ticket 11). A document, not a canvas:
   * it renders inside the shell, like the guidelines it sits beside.
   */
  | { kind: 'flow' }
  /**
   * The scene map — the whole registry as cards, with the reachability panel
   * (ticket 13). Bare like the board: it is a canvas, and the shell would only
   * compete with it.
   */
  | { kind: 'map' }
  | { kind: 'signin' }
  | { kind: 'setup' }
  | { kind: 'not-found'; path: string };

/** A Volume id contains a colon and slashes, so it cannot sit in a path raw. */
const encodeSegment = encodeURIComponent;

function decodeSegment(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export const paths = {
  overview: '#/',
  projects: '#/projects',
  monitoring: '#/monitoring',
  project: (projectId: number) => `#/projects/${projectId}`,
  environment: (projectId: number, environmentId: number, tab = 'composes') =>
    `#/projects/${projectId}/env/${environmentId}/${tab}`,
  compose: (projectId: number, environmentId: number, composeId: number, tab?: string) =>
    `#/projects/${projectId}/env/${environmentId}/compose/${composeId}${tab ? `/${tab}` : ''}`,
  /**
   * `path` is relative to the Volume root and is `''` at the root, matching the
   * helper protocol. Each segment is encoded separately so a filename holding a
   * slash or a newline survives the round trip.
   */
  volumeExplorer: (projectId: number, environmentId: number, composeId: number, volumeId: string, path = '') => {
    const tail = path
      .split('/')
      .filter(Boolean)
      .map(encodeSegment)
      .join('/');
    return `#/projects/${projectId}/env/${environmentId}/compose/${composeId}/volumes/${encodeSegment(volumeId)}${tail ? `/${tail}` : ''}`;
  },
  guidelines: '#/design/guidelines',
  states: '#/design/states',
  board: '#/design/board',
  flow: '#/design/flow',
  map: '#/design/map',
  signIn: '#/signin',
  setup: '#/setup',
} as const;

/** A hash-shaped path (`#/…`) to a `Route`. Exported for the scene scope, which parses its own initial route. */
export function parse(hash: string): Route {
  const path = hash.replace(/^#/, '').replace(/\/+$/, '');
  const parts = path.split('/').filter(Boolean);

  if (parts.length === 0) return { kind: 'overview' };

  if (parts[0] === 'monitoring') return { kind: 'monitoring' };

  if (parts[0] === 'projects') {
    if (parts.length === 1) return { kind: 'projects' };
    const projectId = Number(parts[1]);
    if (!Number.isFinite(projectId)) return { kind: 'not-found', path };

    if (parts[2] === 'env' && parts[3]) {
      const environmentId = Number(parts[3]);
      if (!Number.isFinite(environmentId)) return { kind: 'not-found', path };

      if (parts[4] === 'compose' && parts[5]) {
        const composeId = Number(parts[5]);
        if (!Number.isFinite(composeId)) return { kind: 'not-found', path };
        if (parts[6] === 'volumes' && parts[7]) {
          return {
            composeId,
            environmentId,
            kind: 'volumeExplorer',
            path: parts.slice(8).map(decodeSegment).join('/'),
            projectId,
            volumeId: decodeSegment(parts[7]),
          };
        }
        return { kind: 'compose', projectId, environmentId, composeId, tab: parts[6] };
      }
      return { kind: 'project', projectId, environmentId, tab: parts[4] };
    }
    return { kind: 'project', projectId };
  }

  if (parts[0] === 'design') {
    if (parts[1] === 'guidelines') return { kind: 'guidelines' };
    if (parts[1] === 'states') return { kind: 'states' };
    if (parts[1] === 'board') return { kind: 'board' };
    if (parts[1] === 'flow') return { kind: 'flow' };
    if (parts[1] === 'map') return { kind: 'map' };
  }

  if (parts[0] === 'signin') return { kind: 'signin' };
  if (parts[0] === 'setup') return { kind: 'setup' };

  return { kind: 'not-found', path };
}

/**
 * A frame's route, as the scope owns it: the current hash-shaped path, its
 * parsed form, and the setter that plays `navigate` inside the frame.
 */
export type RoutedValue = {
  navigate: (to: string) => void;
  path: string;
  route: Route;
};

export const RouteContext = createContext<RoutedValue | undefined>(undefined);

/**
 * The one plain, non-hook `navigate` a frame can end up calling is
 * `ComposePage`'s stale-link redirect (a mount effect), and ticket 02's
 * `after:` adapters are plain functions too. A module function cannot read
 * React context, so a scope *claims* this seam: while a scope holds it, plain
 * `navigate` writes that scope's own route state and the page hash is
 * untouched — absorbed, invisible.
 *
 * The claim is the most recently rendered scope, and it is exactly right in
 * every case that happens: the redirect fires in a commit's passive effects,
 * *after* every provider has claimed the seam in layout effects, so a just-mounted
 * frame absorbs its own redirect. With a whole board mounted at once a stray
 * plain `navigate` would land in *a* frame rather than the page — still
 * absorbed, never a board-wide navigation; scenes declare valid routes, so the
 * redirect does not fire there at all. Board chrome that must move the page
 * calls `setScopeNavigate(null)` first. Standalone no scope ever claims the
 * seam, and `navigate` is exactly today's hash write.
 */
let scopeNavigate: ((to: string) => void) | null = null;

/** Claim (or, with `null`, release) plain `navigate`. Returns the previous claim. */
export function setScopeNavigate(dispatch: ((to: string) => void) | null): ((to: string) => void) | null {
  const previous = scopeNavigate;
  scopeNavigate = dispatch;
  return previous;
}

/** The current claim, if a scope holds it. */
export function currentScopeNavigate(): ((to: string) => void) | null {
  return scopeNavigate;
}

export function navigate(to: string) {
  const scoped = scopeNavigate;
  if (scoped) {
    scoped(to);
    return;
  }
  if (window.location.hash === to) return;
  window.location.hash = to;
}

export function useRoute(): Route {
  const routed = useContext(RouteContext);
  const [hashRoute, setHashRoute] = useState<Route>(() => parse(window.location.hash));
  useEffect(() => {
    // Scoped: the frame's route is context state, and a page-level hash change
    // (the board deep-linking, another frame redirecting) must not move it.
    if (routed !== undefined) return undefined;
    const onChange = () => setHashRoute(parse(window.location.hash));
    window.addEventListener('hashchange', onChange);
    onChange();
    return () => window.removeEventListener('hashchange', onChange);
  }, [routed]);
  return routed !== undefined ? routed.route : hashRoute;
}

export function useActivePath(): string {
  const routed = useContext(RouteContext);
  const [hash, setHash] = useState(() => window.location.hash);
  useEffect(() => {
    if (routed !== undefined) return undefined;
    const onChange = () => setHash(window.location.hash);
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, [routed]);
  return routed !== undefined ? routed.path : hash;
}
