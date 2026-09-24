import { matchRoutes } from 'react-router';

/**
 * The design's URLs (GUIDELINES §3) as history paths. The prototype's hash
 * prefix is review scaffolding — §12 says not to port it — so these are plain
 * paths handed to react-router. Everything that needs a URL builds it here, so
 * the route grammar has one source; `routePaths` below is the same grammar as
 * matcher patterns, shared by the router (`app/routes.tsx`) and by the shell
 * (which reads ids out of the location to colour the sider and the breadcrumb).
 */

export const paths = {
  compose: (projectId: number | string, environmentId: number | string, composeId: number | string, tab?: string) =>
    `/projects/${projectId}/env/${environmentId}/compose/${composeId}${tab ? `/${tab}` : ''}`,
  environment: (projectId: number | string, environmentId: number | string, tab = 'composes') =>
    `/projects/${projectId}/env/${environmentId}/${tab}`,
  monitoring: '/monitoring',
  overview: '/',
  project: (projectId: number | string) => `/projects/${projectId}`,
  projects: '/projects',
} as const;

/** The route grammar, as react-router matcher patterns (no elements). */
export const routePaths = [
  { path: paths.overview },
  { path: 'monitoring' },
  { path: 'projects' },
  { path: 'projects/:projectId' },
  { path: 'projects/:projectId/env/:environmentId/:tab' },
  { path: 'projects/:projectId/env/:environmentId/compose/:composeId' },
  { path: 'projects/:projectId/env/:environmentId/compose/:composeId/:tab' },
] as const;

export type ResolvedPath = {
  params: Record<string, string>;
  /** The matched route pattern, e.g. `projects/:projectId/env/:environmentId/:tab`. */
  pattern: string;
};

/**
 * Match a browser pathname against the route grammar. Unknown paths return
 * `undefined`; the router renders the not-found page for them.
 */
export function resolvePath(pathname: string): ResolvedPath | undefined {
  const match = matchRoutes(routePaths as unknown as Parameters<typeof matchRoutes>[0], pathname);
  if (!match) {
    return undefined;
  }
  const leaf = match[match.length - 1];
  return { params: leaf.params as Record<string, string>, pattern: String(leaf.route.path) };
}
