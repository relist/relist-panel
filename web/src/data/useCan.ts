import type { Role } from './api';
import { usePanel } from './store';

/** A level a page can ask about; each includes the ones before it. */
export type Level = 'viewer' | 'operator' | 'editor';

const rank: Record<Role, number> = { none: 0, viewer: 1, operator: 2, editor: 3 };

/**
 * Whether the signed-in User is an admin. A status without a User (an older
 * panel) reads as an admin; the API decides either way.
 */
export function useIsAdmin(): boolean {
  const { auth } = usePanel();
  return auth.user?.isAdmin ?? true;
}

/**
 * Whether the signed-in User holds at least `level` in the Environment, from
 * `/api/auth/status`. It only hides or disables actions; the server decides
 * (ADR-0031).
 */
export function useCan(environmentId: number | undefined, level: Level): boolean {
  const { auth } = usePanel();
  if (auth.user?.isAdmin ?? true) return true;
  if (environmentId === undefined) return false;
  for (const project of auth.access?.projects ?? []) {
    const environment = project.environments.find((candidate) => candidate.id === environmentId);
    if (environment) return rank[environment.role] >= rank[level];
  }
  return false;
}

/** Whether the User may edit the Project and its Environments: an editor Project Grant. */
export function useIsProjectEditor(projectId: number | undefined): boolean {
  const { auth } = usePanel();
  if (auth.user?.isAdmin ?? true) return true;
  return auth.access?.projects.find((project) => project.id === projectId)?.role === 'editor';
}
