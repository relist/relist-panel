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
  setup: '/setup',
  signIn: '/signin',
  /**
   * The Volume Explorer's URL: the Compose's route plus a `volumes/explorer`
   * leaf, with the Volume's resolved source and the relative path in the
   * query. A source is an absolute host path or a Docker name, and a path is
   * a sequence of file names — either way, a segment may hold slashes,
   * spaces or newlines, which is why the query carries them instead of the
   * path: each parameter is percent-encoded whole, so it round-trips
   * untouched through a reload. `path` is `''` at the Volume root, matching
   * the helper protocol.
   */
  volumeExplorer: (
    projectId: number | string,
    environmentId: number | string,
    composeId: number | string,
    source: string,
    path = '',
  ) =>
    `/projects/${projectId}/env/${environmentId}/compose/${composeId}/volumes/explorer?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}`,
} as const;

/**
 * The route grammar of the shell's screens, as react-router matcher patterns
 * (no elements). The auth screens (`/signin`, `/setup`) sit outside the shell —
 * the signed-out visitor must not see the sider — so they are routed in
 * `app/routes.tsx` without being part of this grammar.
 */
export const routePaths = [
  { path: paths.overview },
  { path: 'monitoring' },
  { path: 'projects' },
  { path: 'projects/:projectId' },
  { path: 'projects/:projectId/env/:environmentId/:tab' },
  // One Compose route with an optional tab, so Editor ↔ tab is a params change
  // and `ComposePage` stays mounted — a remount would drop editor history and,
  // later, the Terminal session.
  { path: 'projects/:projectId/env/:environmentId/compose/:composeId/:tab?' },
  // The Explorer is a route of its own so the new tab survives a reload and
  // the back button works. `volumes/explorer` is two static segments, so it
  // out-ranks the Compose `:tab` route and `.../volumes` stays the tab.
  { path: 'projects/:projectId/env/:environmentId/compose/:composeId/volumes/explorer' },
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
