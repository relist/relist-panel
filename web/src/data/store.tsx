import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { api } from './api';
import type { Credentials, ProjectInput } from './api';
import type { Compose, ComposeService, EnvironmentVariable, HostMetrics, MetricWindow, Project, ServiceMetrics } from './types';
import { findComposeLocation, findEnvironmentLocation, serviceFromFacts } from './types';

/**
 * The panel's data layer: the prototype's store shape (`web-design/src/data/store.tsx`)
 * with every method body replaced by a call to the real API. Components read the
 * same state and call the same methods as in the prototype; the store is the seam.
 *
 * Contract:
 *  - Methods resolve when the mutation succeeded and the tree is updated; they
 *    reject with an `Error` carrying the API's message, for the caller's Alert.
 *  - Loading is exposed through `isPending(key)` under the prototype's keys
 *    (`save-compose:3`, `deploy:3`, `status:3`, …), plus the data loads
 *    (`projects`, `auth-status`, `variables:<environmentId>`, `logs:<composeId>`,
 *    `metrics:<window>`, `services:<composeId>`,
 *    `service-metrics:<composeId>:<service>:<window>`).
 *  - Every method is a stable `useCallback`, so an effect that depends on one
 *    never refires when pending or data state changes — the prototype hit an
 *    infinite-update loop with per-render callbacks; do not reintroduce it.
 *
 * Prototype chrome that is deliberately absent: the latency switch and the
 * `track`/`wait` simulation (real latency is the real API's), the activity
 * feed and `host` info (no endpoint — GUIDELINES §11). The metrics loaders
 * return the endpoints' envelope (window, `partial`, `sources`, samples), not
 * the prototype's bare arrays — the screens read `partial` and `sources` off
 * it instead of guessing. Per-Service rows live in a `services` map keyed by
 * Compose rather than on the Compose object (the API's Compose has no services
 * key, and tree patches would wipe it), and `deployedAt` is stamped client-side
 * on a successful deploy — the API records no deploy timestamp (GUIDELINES
 * §11), and the Monitor tab's deploy marker is only as honest as that stamp.
 */

export type NewVariable = { key: string; value: string };

export type AuthState = {
  /** False until the first `/api/auth/status` read settles. */
  checked: boolean;
  setupRequired: boolean;
  authenticated: boolean;
};

const signedOut: AuthState = { checked: true, setupRequired: false, authenticated: false };

type PanelValue = {
  auth: AuthState;
  addEnvironment: (projectId: number, name: string) => Promise<void>;
  checkedAt: Record<number, string>;
  createCompose: (environmentId: number, name: string, yamlContent: string) => Promise<Compose>;
  createProject: (input: ProjectInput) => Promise<Project>;
  deleteCompose: (composeId: number) => Promise<void>;
  deleteEnvironment: (projectId: number, environmentId: number) => Promise<void>;
  deleteProject: (projectId: number) => Promise<void>;
  deployCompose: (composeId: number) => Promise<void>;
  isPending: (key: string) => boolean;
  loadAuthStatus: () => Promise<void>;
  loadLogs: (composeId: number) => Promise<string>;
  /** The host's metrics for one window — the `/api/host/metrics` envelope. */
  loadMetrics: (metricWindow: MetricWindow) => Promise<HostMetrics>;
  loadProjects: () => Promise<void>;
  /** One Service's metrics envelope from the Compose's Services endpoint. */
  loadServiceMetrics: (composeId: number, service: string, metricWindow: MetricWindow) => Promise<ServiceMetrics>;
  /** The Compose's per-Service rows, from the container collector's cache. */
  loadServices: (composeId: number) => Promise<void>;
  loadVariables: (environmentId: number) => Promise<void>;
  projects: Project[];
  /** Client-side deploy stamps (§11: the API records no deploy time). */
  deployedAt: Record<number, string>;
  /** Per-Service rows per Compose, from the Services endpoint. */
  services: Record<number, ComposeService[]>;
  refreshStatus: (composeId: number) => Promise<void>;
  restartCompose: (composeId: number) => Promise<void>;
  saveCompose: (composeId: number, yamlContent: string) => Promise<void>;
  saveVariable: (environmentId: number, variable: NewVariable, variableId?: number) => Promise<void>;
  deleteVariable: (environmentId: number, variableId: number) => Promise<void>;
  replaceVariables: (environmentId: number, rows: NewVariable[]) => Promise<void>;
  signIn: (email: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  signUp: (email: string, password: string) => Promise<void>;
  stopCompose: (composeId: number) => Promise<void>;
  updateProject: (projectId: number, input: ProjectInput) => Promise<void>;
};

const PanelContext = createContext<PanelValue | undefined>(undefined);

export function PanelProvider({ children }: { children: ReactNode }) {
  const [auth, setAuth] = useState<AuthState>({ checked: false, setupRequired: false, authenticated: false });
  const [projects, setProjects] = useState<Project[]>([]);
  const [checkedAt, setCheckedAt] = useState<Record<number, string>>({});
  const [deployedAt, setDeployedAt] = useState<Record<number, string>>({});
  const [services, setServices] = useState<Record<number, ComposeService[]>>({});
  const [pending, setPending] = useState<Record<string, true>>({});

  // Mutable mirrors of the tree, read inside the mutation methods. Without
  // them, stable (deps-free) callbacks would close over stale state.
  const projectsRef = useRef<Project[]>([]);
  const checkedAtRef = useRef<Record<number, string>>({});
  const deployedAtRef = useRef<Record<number, string>>({});

  const updateProjects = useCallback((next: Project[]) => {
    projectsRef.current = next;
    setProjects(next);
  }, []);

  const patchProjects = useCallback(
    (patch: (current: Project[]) => Project[]) => {
      updateProjects(patch(projectsRef.current));
    },
    [updateProjects],
  );

  const stampChecked = useCallback((composeId: number) => {
    const next = { ...checkedAtRef.current, [composeId]: new Date().toISOString() };
    checkedAtRef.current = next;
    setCheckedAt(next);
  }, []);

  // The API records no deploy timestamp (GUIDELINES §11), so a successful
  // deploy stamps one client-side — the same treatment §11 prescribes for the
  // status read time. The stamp lives for the session; the deploy marker and
  // the facts strip are only as honest as it.
  const stampDeployed = useCallback((composeId: number) => {
    const next = { ...deployedAtRef.current, [composeId]: new Date().toISOString() };
    deployedAtRef.current = next;
    setDeployedAt(next);
  }, []);

  const track = useCallback(async <T,>(key: string, work: () => Promise<T>): Promise<T> => {
    setPending((current) => ({ ...current, [key]: true }));
    try {
      return await work();
    } finally {
      setPending((current) => {
        if (!(key in current)) {
          return current;
        }
        const next = { ...current };
        delete next[key];
        return next;
      });
    }
  }, []);

  const loadVariablesInto = useCallback(
    async (projectId: number, environmentId: number) => {
      const variables = await api.listVariables(projectId, environmentId);
      patchProjects((current) =>
        current.map((project) =>
          project.id !== projectId
            ? project
            : {
                ...project,
                environments: project.environments.map((environment) =>
                  environment.id === environmentId ? { ...environment, variables } : environment,
                ),
              },
        ),
      );
    },
    [patchProjects],
  );

  // ---- Auth -----------------------------------------------------------------

  const loadAuthStatus = useCallback(async () => {
    await track('auth-status', async () => {
      const status = await api.authStatus();
      setAuth({ checked: true, setupRequired: status.setupRequired, authenticated: status.authenticated });
    });
  }, [track]);

  const signIn = useCallback(
    async (email: string, password: string) => {
      await track('signin', async () => {
        await api.login({ email, password } satisfies Credentials);
        setAuth({ checked: true, setupRequired: false, authenticated: true });
      });
    },
    [track],
  );

  const signUp = useCallback(
    async (email: string, password: string) => {
      await track('signup', async () => {
        await api.setup({ email, password } satisfies Credentials);
        // Setup creates the admin account but does not start a session; the
        // entry flow moves on to sign-in (as the monolith does today).
        setAuth({ checked: true, setupRequired: false, authenticated: false });
      });
    },
    [track],
  );

  const signOut = useCallback(async () => {
    await track('signout', async () => {
      await api.logout();
      setAuth(signedOut);
      // Drop the signed-in tree so the next session reloads it from scratch.
      updateProjects([]);
      setCheckedAt({});
      checkedAtRef.current = {};
    });
  }, [track, updateProjects]);

  // Auth drives the app entry (unauthenticated → setup/sign-in, authenticated
  // → the shell), so the provider settles it once on mount.
  useEffect(() => {
    void loadAuthStatus();
  }, [loadAuthStatus]);

  // ---- Projects and Environments ---------------------------------------------

  const loadProjects = useCallback(async () => {
    await track('projects', async () => {
      const list = await api.listProjects();
      // The API nests composes under an environment and environments under a
      // project path; the store resolves the tree exactly as the monolith
      // does today, one collection per path segment.
      const hydrated = await Promise.all(
        list.map(async (project) => ({
          ...project,
          environments: await Promise.all(
            (await api.listEnvironments(project.id)).map(async (environment) => ({
              ...environment,
              variables: [] as EnvironmentVariable[],
              composes: await api.listComposes(project.id, environment.id),
            })),
          ),
        })),
      );
      updateProjects(hydrated);
    });
  }, [track, updateProjects]);

  const createProject = useCallback(
    async (input: ProjectInput) => {
      return track('create-project', async () => {
        const created = await api.createProject(input);
        // The API provisions a default `production` Environment inside the
        // create transaction, so the tree is hydrated for the new Project
        // right away instead of carrying a placeholder empty list.
        const environments = await Promise.all(
          (await api.listEnvironments(created.id)).map(async (environment) => ({
            ...environment,
            variables: [] as EnvironmentVariable[],
            composes: await api.listComposes(created.id, environment.id),
          })),
        );
        const hydrated: Project = { ...created, environments };
        patchProjects((current) => [hydrated, ...current]);
        return hydrated;
      });
    },
    [patchProjects, track],
  );

  const updateProject = useCallback(
    async (projectId: number, input: ProjectInput) => {
      await track(`save-project:${projectId}`, async () => {
        const updated = await api.updateProject(projectId, input);
        patchProjects((current) =>
          current.map((project) => (project.id === projectId ? { ...project, ...updated } : project)),
        );
      });
    },
    [patchProjects, track],
  );

  const deleteProject = useCallback(
    async (projectId: number) => {
      await track(`delete-project:${projectId}`, async () => {
        await api.deleteProject(projectId);
        patchProjects((current) => current.filter((project) => project.id !== projectId));
      });
    },
    [patchProjects, track],
  );

  const addEnvironment = useCallback(
    async (projectId: number, name: string) => {
      await track(`create-environment:${projectId}`, async () => {
        const created = await api.createEnvironment(projectId, { name });
        patchProjects((current) =>
          current.map((project) =>
            project.id === projectId
              ? {
                  ...project,
                  environmentCount: project.environmentCount + 1,
                  environments: [
                    ...project.environments,
                    { ...created, variables: [], composes: [] as Compose[] },
                  ],
                }
              : project,
          ),
        );
      });
    },
    [patchProjects, track],
  );

  const deleteEnvironment = useCallback(
    async (projectId: number, environmentId: number) => {
      await track(`delete-environment:${environmentId}`, async () => {
        await api.deleteEnvironment(projectId, environmentId);
        patchProjects((current) =>
          current.map((project) =>
            project.id === projectId
              ? {
                  ...project,
                  environmentCount: Math.max(0, project.environmentCount - 1),
                  environments: project.environments.filter((environment) => environment.id !== environmentId),
                }
              : project,
          ),
        );
      });
    },
    [patchProjects, track],
  );

  // ---- Environment Variables ---------------------------------------------------

  const saveVariable = useCallback(
    async (environmentId: number, variable: NewVariable, variableId?: number) => {
      await track(`save-variable:${environmentId}`, async () => {
        const location = findEnvironmentLocation(projectsRef.current, environmentId);
        if (!location) {
          throw new Error('Environment not found');
        }
        if (variableId) {
          await api.updateVariable(location.project.id, environmentId, variableId, variable);
        } else {
          await api.createVariable(location.project.id, environmentId, variable);
        }
        await loadVariablesInto(location.project.id, environmentId);
      });
    },
    [loadVariablesInto, track],
  );

  const deleteVariable = useCallback(
    async (environmentId: number, variableId: number) => {
      await track(`delete-variable:${variableId}`, async () => {
        const location = findEnvironmentLocation(projectsRef.current, environmentId);
        if (!location) {
          throw new Error('Environment not found');
        }
        await api.deleteVariable(location.project.id, environmentId, variableId);
        await loadVariablesInto(location.project.id, environmentId);
      });
    },
    [loadVariablesInto, track],
  );

  // No bulk endpoint exists, so a `.env` replace is composed from the existing
  // per-variable calls: remove what the paste drops, upsert what it brings,
  // then read the list back.
  const replaceVariables = useCallback(
    async (environmentId: number, rows: NewVariable[]) => {
      await track(`bulk-variables:${environmentId}`, async () => {
        const location = findEnvironmentLocation(projectsRef.current, environmentId);
        if (!location) {
          throw new Error('Environment not found');
        }
        const { project } = location;
        const current = location.environment.variables;
        for (const existing of current) {
          if (!rows.some((row) => row.key === existing.key)) {
            await api.deleteVariable(project.id, environmentId, existing.id);
          }
        }
        for (const row of rows) {
          const existing = current.find((candidate) => candidate.key === row.key);
          if (existing) {
            await api.updateVariable(project.id, environmentId, existing.id, row);
          } else {
            await api.createVariable(project.id, environmentId, row);
          }
        }
        await loadVariablesInto(project.id, environmentId);
      });
    },
    [loadVariablesInto, track],
  );

  // ---- Composes -----------------------------------------------------------------

  const patchCompose = useCallback(
    (composeId: number, patch: (compose: Compose) => Compose) => {
      patchProjects((current) =>
        current.map((project) => ({
          ...project,
          environments: project.environments.map((environment) => ({
            ...environment,
            composes: environment.composes.map((compose) => (compose.id === composeId ? patch(compose) : compose)),
          })),
        })),
      );
    },
    [patchProjects],
  );

  const createCompose = useCallback(
    async (environmentId: number, name: string, yamlContent: string) => {
      return track(`create-compose:${environmentId}`, async () => {
        const location = findEnvironmentLocation(projectsRef.current, environmentId);
        if (!location) {
          throw new Error('Environment not found');
        }
        const created = await api.createCompose(location.project.id, environmentId, { name, yamlContent });
        patchProjects((current) =>
          current.map((project) =>
            project.id !== location.project.id
              ? project
              : {
                  ...project,
                  environments: project.environments.map((environment) =>
                    environment.id === environmentId
                      ? { ...environment, composes: [...environment.composes, created] }
                      : environment,
                  ),
                },
          ),
        );
        return created;
      });
    },
    [patchProjects, track],
  );

  const saveCompose = useCallback(
    async (composeId: number, yamlContent: string) => {
      await track(`save-compose:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const updated = await api.updateCompose(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          { yamlContent },
        );
        patchCompose(composeId, () => updated);
      });
    },
    [patchCompose, track],
  );

  const deleteCompose = useCallback(
    async (composeId: number) => {
      await track(`delete-compose:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.deleteCompose({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
        patchProjects((current) =>
          current.map((project) => ({
            ...project,
            environments: project.environments.map((environment) => ({
              ...environment,
              composes: environment.composes.filter((compose) => compose.id !== composeId),
            })),
          })),
        );
      });
    },
    [patchProjects, track],
  );

  const runComposeAction = useCallback(
    (key: 'deploy' | 'stop' | 'restart', composeId: number) =>
      track(`${key}:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const path = { projectId: location.project.id, environmentId: location.environment.id, composeId };
        const updated =
          key === 'deploy'
            ? await api.deployCompose(path)
            : key === 'stop'
              ? await api.stopCompose(path)
              : await api.restartCompose(path);
        patchCompose(composeId, () => updated);
        // The API returns no read timestamp; the rail's "checked N minutes ago"
        // is stamped client-side, as the prototype does (GUIDELINES §11).
        stampChecked(composeId);
        if (key === 'deploy') {
          stampDeployed(composeId);
        }
      }),
    [patchCompose, stampChecked, stampDeployed, track],
  );

  const deployCompose = useCallback((composeId: number) => runComposeAction('deploy', composeId), [runComposeAction]);
  const stopCompose = useCallback((composeId: number) => runComposeAction('stop', composeId), [runComposeAction]);
  const restartCompose = useCallback((composeId: number) => runComposeAction('restart', composeId), [runComposeAction]);

  const refreshStatus = useCallback(
    async (composeId: number) => {
      await track(`status:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const fresh = await api.getCompose({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
        patchCompose(composeId, () => fresh);
        stampChecked(composeId);
      });
    },
    [patchCompose, stampChecked, track],
  );

  const loadLogs = useCallback(
    async (composeId: number) => {
      return track(`logs:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const payload = await api.logs({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
        return payload.logs;
      });
    },
    [track],
  );

  // The metrics reads carry no tree state — the envelope is screen-local data,
  // refetched per window — so the method only contributes its pending key.
  const loadMetrics = useCallback(
    (metricWindow: MetricWindow) =>
      track(`metrics:${metricWindow}`, () => api.hostMetrics(metricWindow)),
    [track],
  );

  const loadServiceMetrics = useCallback(
    (composeId: number, service: string, metricWindow: MetricWindow) =>
      track(`service-metrics:${composeId}:${service}:${metricWindow}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        return api.serviceMetrics(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          service,
          metricWindow,
        );
      }),
    [track],
  );

  const loadServices = useCallback(
    async (composeId: number) => {
      await track(`services:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const facts = await api.listServices({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
        const rows = facts.map(serviceFromFacts);
        setServices((current) => ({ ...current, [composeId]: rows }));
      });
    },
    [track],
  );

  const loadVariables = useCallback(
    async (environmentId: number) => {
      await track(`variables:${environmentId}`, async () => {
        const location = findEnvironmentLocation(projectsRef.current, environmentId);
        if (!location) {
          throw new Error('Environment not found');
        }
        await loadVariablesInto(location.project.id, environmentId);
      });
    },
    [loadVariablesInto, track],
  );

  const isPending = useCallback((key: string) => Boolean(pending[key]), [pending]);

  const value = useMemo<PanelValue>(
    () => ({
      addEnvironment,
      auth,
      checkedAt,
      createCompose,
      createProject,
      deleteCompose,
      deleteEnvironment,
      deleteProject,
      deleteVariable,
      deployCompose,
      deployedAt,
      isPending,
      loadAuthStatus,
      loadLogs,
      loadMetrics,
      loadProjects,
      loadServiceMetrics,
      loadServices,
      loadVariables,
      projects,
      refreshStatus,
      replaceVariables,
      restartCompose,
      saveCompose,
      saveVariable,
      services,
      signIn,
      signOut,
      signUp,
      stopCompose,
      updateProject,
    }),
    [
      addEnvironment,
      auth,
      checkedAt,
      createCompose,
      createProject,
      deleteCompose,
      deleteEnvironment,
      deleteProject,
      deleteVariable,
      deployCompose,
      deployedAt,
      isPending,
      loadAuthStatus,
      loadLogs,
      loadMetrics,
      loadProjects,
      loadServiceMetrics,
      loadServices,
      loadVariables,
      pending,
      projects,
      refreshStatus,
      services,
      replaceVariables,
      restartCompose,
      saveCompose,
      saveVariable,
      signIn,
      signOut,
      signUp,
      stopCompose,
      updateProject,
    ],
  );

  return <PanelContext.Provider value={value}>{children}</PanelContext.Provider>;
}

export function usePanel(): PanelValue {
  const value = useContext(PanelContext);
  if (!value) {
    throw new Error('usePanel must be used inside <PanelProvider>');
  }
  return value;
}
