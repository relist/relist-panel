import type { AuthState } from './store';
import type { Role } from './api';
import { useAuthState } from './store';

/** A level a page can ask about; each includes the ones before it. */
export type Level = 'viewer' | 'operator' | 'editor';

const rank: Record<Role, number> = { none: 0, viewer: 1, operator: 2, editor: 3 };

/** The questions a page asks of the signed-in User's roles. */
export type Access = {
  isAdmin: boolean;
  can: (environmentId: number | undefined, level: Level) => boolean;
  isProjectEditor: (projectId: number | undefined) => boolean;
};

/**
 * Reads `/api/auth/status`'s user and access. A status without a User (an
 * older panel) reads as an admin. Convenience only: the API decides
 * (ADR-0031).
 */
export function accessOf(auth: AuthState): Access {
  const isAdmin = auth.user?.isAdmin ?? true;
  const projects = auth.access?.projects ?? [];
  return {
    isAdmin,
    can: (environmentId, level) => {
      if (isAdmin) return true;
      if (environmentId === undefined) return false;
      for (const project of projects) {
        const environment = project.environments.find((candidate) => candidate.id === environmentId);
        if (environment) return rank[environment.role] >= rank[level];
      }
      return false;
    },
    isProjectEditor: (projectId) => isAdmin || projects.find((project) => project.id === projectId)?.role === 'editor',
  };
}

/**
 * For a page that asks about several Environments or Projects at once.
 * Outside a PanelProvider it answers as for an admin; the API decides anyway.
 */
export function useAccess(): Access {
  return accessOf(useAuthState() ?? { checked: true, authenticated: true });
}

/** Whether the signed-in User is an admin. */
export function useIsAdmin(): boolean {
  return useAccess().isAdmin;
}

/** Whether the signed-in User holds at least `level` in the Environment. */
export function useCan(environmentId: number | undefined, level: Level): boolean {
  return useAccess().can(environmentId, level);
}

/** Whether the User may edit the Project and its Environments: an editor Project Grant. */
export function useIsProjectEditor(projectId: number | undefined): boolean {
  return useAccess().isProjectEditor(projectId);
}
