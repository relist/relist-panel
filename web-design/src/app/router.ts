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
  | { kind: 'guidelines' }
  | { kind: 'states' }
  | { kind: 'signin' }
  | { kind: 'setup' }
  | { kind: 'not-found'; path: string };

export const paths = {
  overview: '#/',
  projects: '#/projects',
  monitoring: '#/monitoring',
  project: (projectId: number) => `#/projects/${projectId}`,
  environment: (projectId: number, environmentId: number, tab = 'composes') =>
    `#/projects/${projectId}/env/${environmentId}/${tab}`,
  compose: (projectId: number, environmentId: number, composeId: number, tab?: string) =>
    `#/projects/${projectId}/env/${environmentId}/compose/${composeId}${tab ? `/${tab}` : ''}`,
  guidelines: '#/design/guidelines',
  states: '#/design/states',
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
        return { kind: 'compose', projectId, environmentId, composeId, tab: parts[6] };
      }
      return { kind: 'project', projectId, environmentId, tab: parts[4] };
    }
    return { kind: 'project', projectId };
  }

  if (parts[0] === 'design') {
    if (parts[1] === 'guidelines') return { kind: 'guidelines' };
    if (parts[1] === 'states') return { kind: 'states' };
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
