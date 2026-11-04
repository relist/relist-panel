import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { api } from './api';
import type { AccessView, LoginResult, ProjectInput, SignedInUser } from './api';
import type { Compose, ComposeDomains, DeploymentRequest, ComposeVariable, ComposePreview, EffectiveVariable, ServiceBindings, VariableSet, ComposeService, ComposeVolumes, Domain, DomainInput, EnvironmentVariable, HostMetrics, MetricWindow, OrphanVolume, Project, ServiceMetrics, VolumeListing, VolumeAccess, ZeroDowntimeService } from './types';
import { findComposeLocation, findEnvironmentLocation, serviceFromFacts } from './types';

/**
 * The panel's data layer: the prototype's store shape (the retired design mock, tag `design-mock`)
 * with every method body replaced by a call to the real API. Components read the
 * same state and call the same methods as in the prototype; the store is the seam.
 *
 * Contract:
 *  - Methods resolve when the mutation succeeded and the tree is updated; they
 *    reject with an `Error` carrying the API's message, for the caller's Alert.
 *  - Loading is exposed through `isPending(key)` under the prototype's keys
 *    (`save-compose:3`, `deploy:3`, `status:3`, …), plus the data loads
 *    (`projects`, `auth-status`, `variables:<environmentId>`, `logs:<composeId>`,
 *    `metrics:<window>`, `services:<composeId>`, `volumes:<composeId>`,
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
 * key, and tree patches would wipe it), and a Compose's `lastDeployedAt` is
 * the API's — written on deploy success — so the tree patch a deploy returns
 * carries the fresh timestamp; nothing is stamped client-side.
 */

/** A variable to create or update: the flag is always the caller's answer — the modal reads the toggle, a bulk paste sends plain. */
export type NewVariable = { key: string; value: string; secret: boolean };

export type AuthState = {
  /** False until the first `/api/auth/status` read settles. */
  checked: boolean;
  authenticated: boolean;
  /** The signed-in User, once the status has said who it is. */
  user?: SignedInUser;
  /** A member's roles; null or absent for an admin. */
  access?: AccessView | null;
};

const signedOut: AuthState = { checked: true, authenticated: false };

type PanelValue = {
  auth: AuthState;
  addEnvironment: (projectId: number, name: string) => Promise<void>;
  checkedAt: Record<number, string>;
  createCompose: (environmentId: number, name: string, yamlContent: string) => Promise<Compose>;
  createProject: (input: ProjectInput) => Promise<Project>;
  deleteCompose: (composeId: number) => Promise<void>;
  deleteEnvironment: (projectId: number, environmentId: number) => Promise<void>;
  deleteProject: (projectId: number) => Promise<void>;
  /** Queues a Deployment and resolves to its number; progress is read from the Deployment, not this call. */
  deployCompose: (composeId: number, req: DeploymentRequest) => Promise<number>;
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
  /** The Compose's orphan Volumes: Docker volumes under its project that the file no longer declares. */
  loadOrphanVolumes: (composeId: number) => Promise<OrphanVolume[]>;
  deleteOrphanVolume: (composeId: number, name: string) => Promise<void>;
  /** The Compose's domains and the Services a domain can name. */
  loadDomains: (composeId: number) => Promise<ComposeDomains>;
  /** The Compose's Services with a Domain and their zero-downtime switches (ADR-0023). */
  loadZeroDowntime: (composeId: number) => Promise<ZeroDowntimeService[]>;
  setZeroDowntime: (composeId: number, service: string, enabled: boolean) => Promise<void>;
  /** The resolved file a deploy would write for `yamlContent`, which need not be saved. */
  previewCompose: (composeId: number, yamlContent: string) => Promise<ComposePreview>;
  /** Each domain mutation refreshes the Compose afterwards, so `domainsChangedAt` reaches the page. */
  createDomain: (composeId: number, input: DomainInput) => Promise<Domain>;
  updateDomain: (composeId: number, domainId: number, input: DomainInput) => Promise<Domain>;
  deleteDomain: (composeId: number, domainId: number) => Promise<void>;
  /** The Compose's own variables. Each write refreshes the Compose, so `variablesChangedAt` reaches the page. */
  loadComposeVariables: (composeId: number) => Promise<ComposeVariable[]>;
  saveComposeVariable: (composeId: number, variable: NewVariable, variableId?: number) => Promise<void>;
  deleteComposeVariable: (composeId: number, variableId: number) => Promise<void>;
  /** A pasted `.env` replaces the Compose's variables: keys the paste drops are deleted. */
  replaceComposeVariables: (composeId: number, rows: NewVariable[]) => Promise<void>;
  /** The Compose's Variable Sets with their entries and bound Services. Sets live on the API, not in the tree; each write refreshes the Compose. */
  loadVariableSets: (composeId: number) => Promise<VariableSet[]>;
  createVariableSet: (composeId: number, name: string) => Promise<void>;
  renameVariableSet: (composeId: number, setId: number, name: string) => Promise<void>;
  deleteVariableSet: (composeId: number, setId: number) => Promise<void>;
  saveSetEntry: (composeId: number, setId: number, entry: NewVariable, entryId?: number) => Promise<void>;
  deleteSetEntry: (composeId: number, setId: number, entryId: number) => Promise<void>;
  /** A pasted `.env` replaces a set's entries: keys the paste drops are deleted. */
  replaceSetEntries: (composeId: number, setId: number, rows: NewVariable[]) => Promise<void>;
  /** The Compose's Service bindings. Saving refreshes the Compose, so `variablesChangedAt` reaches the page. */
  loadBindings: (composeId: number) => Promise<ServiceBindings[]>;
  saveBindings: (composeId: number, service: string, setIds: number[]) => Promise<void>;
  loadEffectiveVariables: (composeId: number, service: string) => Promise<EffectiveVariable[]>;
  /** The Compose's resolved Volumes and pre-flight statuses, from the Compose CLI. */
  loadVolumes: (composeId: number) => Promise<ComposeVolumes>;
  /** One directory inside a resolved Volume, for the Explorer's current path. */
  listVolumeEntries: (composeId: number, source: string, path: string, access?: VolumeAccess, after?: string) => Promise<VolumeListing>;
  /** One file's bytes inside a resolved Volume, for the editor (the caller applies the 1 MB gate). */
  readVolumeFile: (composeId: number, source: string, path: string, access?: VolumeAccess) => Promise<Uint8Array>;
  /** Uploads or overwrites one file inside a resolved Volume. Rejects with the API's message. */
  writeVolumeFile: (composeId: number, source: string, path: string, file: Blob, access?: VolumeAccess) => Promise<void>;
  /** Deletes one entry, or a whole tree, inside a resolved Volume. Permanent. */
  deleteVolumeEntry: (composeId: number, source: string, path: string, access?: VolumeAccess) => Promise<void>;
  /** Creates one directory inside a resolved Volume, as the confirmed operation user. */
  createVolumeDirectory: (composeId: number, source: string, path: string, access?: VolumeAccess) => Promise<void>;
  /** Creates one empty file inside a resolved Volume, refusing a name already taken. */
  createVolumeFile: (composeId: number, source: string, path: string, access?: VolumeAccess) => Promise<void>;
  /**
   * Streams a tar.gz into one directory of a resolved Volume: upload and
   * extract are one operation, so there is no separate upload step and the
   * archive is never stored. Rejects with the API's message.
   */
  restoreVolume: (composeId: number, source: string, path: string, file: Blob, access?: VolumeAccess) => Promise<void>;
  loadVariables: (environmentId: number) => Promise<void>;
  projects: Project[];
  /** Per-Service rows per Compose, from the Services endpoint. */
  services: Record<number, ComposeService[]>;
  refreshStatus: (composeId: number) => Promise<void>;
  restartCompose: (composeId: number) => Promise<void>;
  saveCompose: (composeId: number, yamlContent: string) => Promise<void>;
  saveVariable: (environmentId: number, variable: NewVariable, variableId?: number) => Promise<void>;
  deleteVariable: (environmentId: number, variableId: number) => Promise<void>;
  replaceVariables: (environmentId: number, rows: NewVariable[]) => Promise<void>;
  signIn: (email: string, password: string, code?: string) => Promise<LoginResult>;
  signOut: () => Promise<void>;
  stopCompose: (composeId: number) => Promise<void>;
  updateProject: (projectId: number, input: ProjectInput) => Promise<void>;
};

const PanelContext = createContext<PanelValue | undefined>(undefined);

export function PanelProvider({ children }: { children: ReactNode }) {
  const [auth, setAuth] = useState<AuthState>({ checked: false, authenticated: false });
  const [projects, setProjects] = useState<Project[]>([]);
  const [checkedAt, setCheckedAt] = useState<Record<number, string>>({});
  const [services, setServices] = useState<Record<number, ComposeService[]>>({});
  const [pending, setPending] = useState<Record<string, true>>({});

  // Mutable mirrors of the tree, read inside the mutation methods. Without
  // them, stable (deps-free) callbacks would close over stale state.
  const projectsRef = useRef<Project[]>([]);
  const checkedAtRef = useRef<Record<number, string>>({});

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
      setAuth({ checked: true, authenticated: status.authenticated, user: status.user, access: status.access });
    });
  }, [track]);

  const signIn = useCallback(
    async (email: string, password: string, code?: string) => {
      return track('signin', async () => {
        const result = await api.login({ email, password, code });
        // Only a completed sign-in opens the app; a code prompt or the lock
        // leaves the auth state untouched so the page stays on its step.
        if (result.kind === 'signedIn') {
          // The status says who signed in, which the shell needs before it
          // renders: a non-admin sees only their Account. The session cookie
          // is already set, so a failed read still signs in, without the
          // User; the next status read fills it in.
          try {
            const status = await api.authStatus();
            setAuth({ checked: true, authenticated: status.authenticated, user: status.user, access: status.access });
          } catch {
            setAuth({ checked: true, authenticated: true });
          }
        }
        return result;
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
    (key: 'stop' | 'restart', composeId: number) =>
      track(`${key}:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const path = { projectId: location.project.id, environmentId: location.environment.id, composeId };
        const updated = key === 'stop' ? await api.stopCompose(path) : await api.restartCompose(path);
        patchCompose(composeId, () => updated);
        // The API returns no read timestamp; the rail's "checked N minutes ago"
        // is stamped client-side, as the prototype does (GUIDELINES §11). The
        // deploy time needs no stamp: `updated` carries the API's
        // `lastDeployedAt`, written on deploy success.
        stampChecked(composeId);
      }),
    [patchCompose, stampChecked, track],
  );

  const deployCompose = useCallback(
    async (composeId: number, req: DeploymentRequest) => {
      return track(`deploy:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const { number } = await api.enqueueDeployment(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          req,
        );
        return number;
      });
    },
    [track],
  );
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

  // The resolution carries no tree state either: it is a fact about the
  // Compose's files on disk, read fresh each time, and the page owns the result
  // so the Volumes tab and the page-level tab-hiding decision share one fetch
  // (the spec: one `docker compose config` per page load, ~30 ms, no caching).
  const loadVolumes = useCallback(
    (composeId: number) =>
      track(`volumes:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        return api.listVolumes({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
      }),
    [track],
  );

  const loadOrphanVolumes = useCallback(
    (composeId: number) =>
      track(`orphans:${composeId}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        const { orphans } = await api.listOrphanVolumes({
          projectId: location.project.id,
          environmentId: location.environment.id,
          composeId,
        });
        return orphans;
      }),
    [track],
  );

  const deleteOrphanVolume = useCallback(
    (composeId: number, name: string) =>
      track(`orphan-delete:${composeId}:${name}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.deleteOrphanVolume(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          name,
        );
      }),
    [track],
  );

  // Domains are stored on the API, not in the tree: the Domains tab reads them
  // fresh. A write refreshes the Compose, which carries the `domainsChangedAt`
  // stamp the redeploy banner reads.
  const domainLocation = useCallback((composeId: number) => {
    const location = findComposeLocation(projectsRef.current, composeId);
    if (!location) {
      throw new Error('Compose not found');
    }
    return { projectId: location.project.id, environmentId: location.environment.id, composeId };
  }, []);

  const loadDomains = useCallback(
    (composeId: number) =>
      track(`domains:${composeId}`, () => api.listDomains(domainLocation(composeId))),
    [domainLocation, track],
  );

  const loadZeroDowntime = useCallback(
    (composeId: number) =>
      track(`zero-downtime:${composeId}`, () => api.listZeroDowntime(domainLocation(composeId))),
    [domainLocation, track],
  );

  const setZeroDowntime = useCallback(
    (composeId: number, service: string, enabled: boolean) =>
      track(`zero-downtime-save:${composeId}:${service}`, () =>
        api.setZeroDowntime(domainLocation(composeId), service, enabled),
      ),
    [domainLocation, track],
  );

  const previewCompose = useCallback(
    (composeId: number, yamlContent: string) =>
      track(`preview:${composeId}`, () => api.previewCompose(domainLocation(composeId), yamlContent)),
    [domainLocation, track],
  );

  const createDomain = useCallback(
    (composeId: number, input: DomainInput) =>
      track(`domain-save:${composeId}`, async () => {
        const created = await api.createDomain(domainLocation(composeId), input);
        await refreshStatus(composeId);
        return created;
      }),
    [domainLocation, refreshStatus, track],
  );

  const updateDomain = useCallback(
    (composeId: number, domainId: number, input: DomainInput) =>
      track(`domain-save:${composeId}`, async () => {
        const updated = await api.updateDomain(domainLocation(composeId), domainId, input);
        await refreshStatus(composeId);
        return updated;
      }),
    [domainLocation, refreshStatus, track],
  );

  const deleteDomain = useCallback(
    (composeId: number, domainId: number) =>
      track(`domain-delete:${composeId}:${domainId}`, async () => {
        await api.deleteDomain(domainLocation(composeId), domainId);
        await refreshStatus(composeId);
      }),
    [domainLocation, refreshStatus, track],
  );

  const loadComposeVariables = useCallback(
    (composeId: number) =>
      track(`compose-variables:${composeId}`, () => api.listComposeVariables(domainLocation(composeId))),
    [domainLocation, track],
  );

  const saveComposeVariable = useCallback(
    (composeId: number, variable: NewVariable, variableId?: number) =>
      track(`save-compose-variable:${composeId}`, async () => {
        const location = domainLocation(composeId);
        if (variableId) {
          await api.updateComposeVariable(location, variableId, variable);
        } else {
          await api.createComposeVariable(location, variable);
        }
        await refreshStatus(composeId);
      }),
    [domainLocation, refreshStatus, track],
  );

  const deleteComposeVariable = useCallback(
    (composeId: number, variableId: number) =>
      track(`delete-compose-variable:${variableId}`, async () => {
        await api.deleteComposeVariable(domainLocation(composeId), variableId);
        await refreshStatus(composeId);
      }),
    [domainLocation, refreshStatus, track],
  );

  // No bulk endpoint, as for the Environment: a paste is composed from the
  // per-row calls against the list as the server has it now, then one refresh.
  const replaceComposeVariables = useCallback(
    (composeId: number, rows: NewVariable[]) =>
      track(`bulk-compose-variables:${composeId}`, async () => {
        const location = domainLocation(composeId);
        const current = await api.listComposeVariables(location);
        for (const existing of current) {
          if (!rows.some((row) => row.key === existing.key)) await api.deleteComposeVariable(location, existing.id);
        }
        for (const row of rows) {
          const existing = current.find((candidate) => candidate.key === row.key);
          if (existing) await api.updateComposeVariable(location, existing.id, row);
          else await api.createComposeVariable(location, row);
        }
        await refreshStatus(composeId);
      }),
    [domainLocation, refreshStatus, track],
  );

  // The Compose's sets. A write that can stamp `variablesChangedAt` (an entry
  // write, a delete) refreshes this Compose so the banner follows; create and
  // rename refresh too, which is harmless. The write already succeeded, so a
  // failed refresh must not report it as failed.
  const refreshQuietly = useCallback(
    async (composeId: number) => {
      try {
        await refreshStatus(composeId);
      } catch {
        // The banner appears on the next reload instead.
      }
    },
    [refreshStatus],
  );

  const loadVariableSets = useCallback(
    (composeId: number) => track(`variable-sets:${composeId}`, () => api.listVariableSets(domainLocation(composeId))),
    [domainLocation, track],
  );

  const createVariableSet = useCallback(
    (composeId: number, name: string) =>
      track(`create-variable-set:${composeId}`, async () => {
        await api.createVariableSet(domainLocation(composeId), name);
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const renameVariableSet = useCallback(
    (composeId: number, setId: number, name: string) =>
      track(`rename-variable-set:${setId}`, async () => {
        await api.renameVariableSet(domainLocation(composeId), setId, name);
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const deleteVariableSet = useCallback(
    (composeId: number, setId: number) =>
      track(`delete-variable-set:${setId}`, async () => {
        await api.deleteVariableSet(domainLocation(composeId), setId);
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const saveSetEntry = useCallback(
    (composeId: number, setId: number, entry: NewVariable, entryId?: number) =>
      track(`save-set-entry:${setId}`, async () => {
        const location = domainLocation(composeId);
        if (entryId) {
          await api.updateSetEntry(location, setId, entryId, entry);
        } else {
          await api.createSetEntry(location, setId, entry);
        }
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const deleteSetEntry = useCallback(
    (composeId: number, setId: number, entryId: number) =>
      track(`delete-set-entry:${entryId}`, async () => {
        await api.deleteSetEntry(domainLocation(composeId), setId, entryId);
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const replaceSetEntries = useCallback(
    (composeId: number, setId: number, rows: NewVariable[]) =>
      track(`bulk-set-entries:${setId}`, async () => {
        const location = domainLocation(composeId);
        const sets = await api.listVariableSets(location);
        const current = sets.find((set) => set.id === setId)?.entries ?? [];
        for (const existing of current) {
          if (!rows.some((row) => row.key === existing.key)) await api.deleteSetEntry(location, setId, existing.id);
        }
        for (const row of rows) {
          const existing = current.find((candidate) => candidate.key === row.key);
          if (existing) await api.updateSetEntry(location, setId, existing.id, row);
          else await api.createSetEntry(location, setId, row);
        }
        await refreshQuietly(composeId);
      }),
    [domainLocation, refreshQuietly, track],
  );

  const loadBindings = useCallback(
    (composeId: number) => track(`bindings:${composeId}`, () => api.listBindings(domainLocation(composeId))),
    [domainLocation, track],
  );

  const saveBindings = useCallback(
    (composeId: number, service: string, setIds: number[]) =>
      track(`save-bindings:${composeId}:${service}`, async () => {
        await api.saveBindings(domainLocation(composeId), service, setIds);
        await refreshStatus(composeId);
      }),
    [domainLocation, refreshStatus, track],
  );

  const loadEffectiveVariables = useCallback(
    (composeId: number, service: string) =>
      track(`effective:${composeId}:${service}`, () => api.effectiveVariables(domainLocation(composeId), service)),
    [domainLocation, track],
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

  // A directory listing carries no tree state either: it is a fact about the
  // Volume's files at one moment, and the Explorer owns it. The pending key
  // names the exact listing (`volume-list:<compose>:<source>:<path>`) so a
  // refresh spins only the directory it re-reads.
  const listVolumeEntries = useCallback(
    (composeId: number, source: string, path: string, access?: VolumeAccess, after?: string) =>
      track(`volume-list:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        return api.listVolumeEntries(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          access,
          after,
        );
      }),
    [track],
  );

  // One file's bytes for the editor carry no tree state either. The pending key
  // names the exact read (`volume-read:<compose>:<source>:<path>`), so the
  // row's edit control spins only for the file it opened.
  const readVolumeFile = useCallback(
    (composeId: number, source: string, path: string, access?: VolumeAccess) =>
      track(`volume-read:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        return api.readVolumeFile(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          access,
        );
      }),
    [track],
  );

  // A write carries no tree state either: the Volume's contents are the
  // server's, and the Explorer re-reads the directory it wrote into. The
  // pending key names the exact entry (`volume-write:<compose>:<source>:<path>`),
  // so a control spins only for the write it started.
  const writeVolumeFile = useCallback(
    (composeId: number, source: string, path: string, file: Blob, access?: VolumeAccess) =>
      track(`volume-write:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.writeVolumeFile(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          file,
          access,
        );
      }),
    [track],
  );

  const deleteVolumeEntry = useCallback(
    (composeId: number, source: string, path: string, access?: VolumeAccess) =>
      track(`volume-delete:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.deleteVolumeEntry(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          access,
        );
      }),
    [track],
  );

  // A create carries no tree state either: the Explorer re-reads the
  // directory it created into. The pending key names the exact entry
  // (`volume-mkdir:<compose>:<source>:<path>` and
  // `volume-create:<compose>:<source>:<path>`), so the dialog's action spins
  // only for its own request.
  const createVolumeDirectory = useCallback(
    (composeId: number, source: string, path: string, access?: VolumeAccess) =>
      track(`volume-mkdir:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.createVolumeDirectory(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          access,
        );
      }),
    [track],
  );

  const createVolumeFile = useCallback(
    (composeId: number, source: string, path: string, access?: VolumeAccess) =>
      track(`volume-create:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.createVolumeFile(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          access,
        );
      }),
    [track],
  );

  // A restore carries no tree state either: the extract writes files the
  // Explorer re-reads. The pending key names the target directory
  // (`volume-restore:<compose>:<source>:<path>`), so the toolbar's action
  // spins for its own request.
  const restoreVolume = useCallback(
    (composeId: number, source: string, path: string, file: Blob, access?: VolumeAccess) =>
      track(`volume-restore:${composeId}:${source}:${path}`, async () => {
        const location = findComposeLocation(projectsRef.current, composeId);
        if (!location) {
          throw new Error('Compose not found');
        }
        await api.restoreVolume(
          { projectId: location.project.id, environmentId: location.environment.id, composeId },
          source,
          path,
          file,
          access,
        );
      }),
    [track],
  );

  const isPending = useCallback((key: string) => Boolean(pending[key]), [pending]);

  const value = useMemo<PanelValue>(
    () => ({
      addEnvironment,
      auth,
      checkedAt,
      createCompose,
      createProject,
      createVolumeDirectory,
      createVolumeFile,
      deleteCompose,
      deleteEnvironment,
      deleteProject,
      deleteVariable,
      deleteOrphanVolume,
      deleteVolumeEntry,
      createDomain,
      deleteDomain,
      deleteComposeVariable,
      loadComposeVariables,
      replaceComposeVariables,
      replaceSetEntries,
      saveComposeVariable,
      loadVariableSets,
      createVariableSet,
      renameVariableSet,
      deleteVariableSet,
      saveSetEntry,
      deleteSetEntry,
      loadBindings,
      saveBindings,
      loadEffectiveVariables,
      loadDomains,
      loadZeroDowntime,
      setZeroDowntime,
      previewCompose,
      updateDomain,
      deployCompose,
      isPending,
      loadAuthStatus,
      loadLogs,
      loadMetrics,
      loadOrphanVolumes,
      loadProjects,
      loadServiceMetrics,
      loadServices,
      loadVariables,
      loadVolumes,
      listVolumeEntries,
      projects,
      readVolumeFile,
      refreshStatus,
      replaceVariables,
      restartCompose,
      restoreVolume,
      saveCompose,
      saveVariable,
      services,
      signIn,
      signOut,
      stopCompose,
      updateProject,
      writeVolumeFile,
    }),
    [
      addEnvironment,
      auth,
      checkedAt,
      createCompose,
      createProject,
      createVolumeDirectory,
      createVolumeFile,
      deleteCompose,
      deleteEnvironment,
      deleteProject,
      deleteVariable,
      deleteOrphanVolume,
      deleteVolumeEntry,
      createDomain,
      deleteDomain,
      deleteComposeVariable,
      loadComposeVariables,
      replaceComposeVariables,
      replaceSetEntries,
      saveComposeVariable,
      loadVariableSets,
      createVariableSet,
      renameVariableSet,
      deleteVariableSet,
      saveSetEntry,
      deleteSetEntry,
      loadBindings,
      saveBindings,
      loadEffectiveVariables,
      loadDomains,
      loadZeroDowntime,
      setZeroDowntime,
      previewCompose,
      updateDomain,
      deployCompose,
      isPending,
      loadAuthStatus,
      loadLogs,
      loadMetrics,
      loadOrphanVolumes,
      loadProjects,
      loadServiceMetrics,
      loadServices,
      loadVariables,
      loadVolumes,
      listVolumeEntries,
      pending,
      projects,
      readVolumeFile,
      refreshStatus,
      services,
      replaceVariables,
      restartCompose,
      restoreVolume,
      saveCompose,
      saveVariable,
      signIn,
      signOut,
      stopCompose,
      updateProject,
      writeVolumeFile,
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
