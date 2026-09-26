import { useEffect, useState } from 'react';

/**
 * Minimal hash router. Deliberately hand-rolled: routing library choice is not
 * part of this design pass, but every screen must be directly linkable so the
 * whole set can be reviewed and shared page by page.
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
  | { kind: 'screensGallery' }
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
  screens: '#/design/screens',
  signIn: '#/signin',
  setup: '#/setup',
} as const;

function parse(hash: string): Route {
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
    if (parts[1] === 'screens') return { kind: 'screensGallery' };
  }

  if (parts[0] === 'signin') return { kind: 'signin' };
  if (parts[0] === 'setup') return { kind: 'setup' };

  return { kind: 'not-found', path };
}

export function navigate(to: string) {
  if (window.location.hash === to) return;
  window.location.hash = to;
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parse(window.location.hash));
  useEffect(() => {
    const onChange = () => setRoute(parse(window.location.hash));
    window.addEventListener('hashchange', onChange);
    onChange();
    return () => window.removeEventListener('hashchange', onChange);
  }, []);
  return route;
}

export function useActivePath(): string {
  const [hash, setHash] = useState(() => window.location.hash);
  useEffect(() => {
    const onChange = () => setHash(window.location.hash);
    window.addEventListener('hashchange', onChange);
    return () => window.removeEventListener('hashchange', onChange);
  }, []);
  return hash;
}
