import { createContext, useCallback, useContext, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { paths } from '../app/router';
import {
  composeLogs as mockLogs,
  activity as mockActivity,
  containerIdFor,
  host as mockHost,
  metricSamples,
  projects as mockProjects,
  serviceMetricSamples,
} from './mocks';
import type {
  ActivityEntry,
  Compose,
  ComposeLocation,
  EnvironmentVariable,
  HostInfo,
  MetricSample,
  MetricWindow,
  Project,
  ServiceSample,
  UidSource,
  Volume,
  VolumeEntry,
  VolumeHandle,
  VolumeHandleMode,
} from './types';
import { findComposeLocation } from './types';
import { fixtureFileContents, IDLE_TIMEOUT_MINUTES, listVolumeDir, resolveVolumes, unreadableReason } from './volumeMocks';

/**
 * In-memory stand-in for the panel's HTTP API. Every mutation is local and
 * resolves after `latency`, so loading, pending and success states in the
 * redesign behave exactly as they would against the real backend.
 */

export type Speed = 'instant' | 'normal' | 'slow';

const LATENCY: Record<Speed, number> = { instant: 80, normal: 650, slow: 1_800 };

export type NewVariable = { key: string; value: string };

/**
 * Storage-level state a *scene* forces, for the mount's lifetime — the
 * `?ui=store.*` keys the harness turns into this prop (spec: *Forcing state
 * without a click*). A prop rather than a hook because this is the store's own
 * seed state: no override means the seed, and "back to normal" is the seed.
 *
 * It carries declarations, not objects: a scene says *which* volume ids are open
 * and the store decides what a handle looks like, so the shape of a handle stays
 * in this file with the rest of the fixtures.
 */
export type SceneOverrides = {
  /** The activity log, replaced wholesale — `[]` is the empty feed. */
  activity?: ActivityEntry[];
  /** Volume ids whose Handle is open, in the seed's own shape; `[]` is none open. */
  handles?: string[];
  /** The mock's latency, for a scene that shows a pending state. */
  speed?: Speed;
};

/** What the start dialog collects. Everything else about a handle is derived. */
export type StartHandleInput = {
  mode: VolumeHandleMode;
  uid: number;
  gid: number;
  uidSource: UidSource;
};

type PanelValue = {
  activity: ActivityEntry[];
  addEnvironment: (projectId: number, name: string) => Promise<void>;
  checkedAt: Record<number, string>;
  createCompose: (environmentId: number, name: string, yamlContent: string) => Promise<Compose>;
  createProject: (input: { name: string; description: string }) => Promise<Project>;
  deleteCompose: (composeId: number) => Promise<void>;
  deleteEnvironment: (projectId: number, environmentId: number) => Promise<void>;
  deleteProject: (projectId: number) => Promise<void>;
  deployCompose: (composeId: number) => Promise<void>;
  /** Handles, keyed by `Volume.id`. */
  handles: Record<string, VolumeHandle>;
  host: HostInfo;
  isPending: (key: string) => boolean;
  /** One directory of a Volume. Rejects when the handle's uid cannot read it. */
  listDir: (volumeId: string, path: string) => Promise<VolumeEntry[]>;
  logs: Record<number, string>;
  /**
   * Every request in flight, keyed the way `isPending` is. Exposed because the
   * scene harness publishes readiness off it: a capture may not start while the
   * store is still loading (`app/sceneReady.ts`).
   */
  pending: Record<string, true>;
  loadLogs: (composeId: number) => Promise<string>;
  loadMetrics: (window: MetricWindow) => Promise<MetricSample[]>;
  loadServiceMetrics: (composeId: number, serviceName: string, window: MetricWindow) => Promise<ServiceSample[]>;
  /** A text file's contents, for the editor. */
  readVolumeFile: (volumeId: string, path: string) => Promise<string>;
  projects: Project[];
  refreshStatus: (composeId: number) => Promise<void>;
  restartCompose: (composeId: number) => Promise<void>;
  /** A Compose's Volumes, or the CLI's error when they cannot be resolved. */
  resolveComposeVolumes: (composeId: number) => Promise<{ ok: true; volumes: Volume[] } | { ok: false; error: string }>;
  saveCompose: (composeId: number, yamlContent: string) => Promise<void>;
  saveVariable: (environmentId: number, variable: NewVariable, variableId?: number) => Promise<void>;
  deleteVariable: (environmentId: number, variableId: number) => Promise<void>;
  replaceVariables: (environmentId: number, rows: NewVariable[]) => Promise<void>;
  setSpeed: (speed: Speed) => void;
  speed: Speed;
  startHandle: (volumeId: string, input: StartHandleInput) => Promise<void>;
  stopCompose: (composeId: number) => Promise<void>;
  stopHandle: (volumeId: string) => Promise<void>;
  updateProject: (projectId: number, input: { name: string; description: string }) => Promise<void>;
  writeVolumeFile: (volumeId: string, path: string, contents: string) => Promise<void>;
};

/** Text a Compose's Volumes tab can never resolve, until it is deployed once. */
const ENV_FILE_MISSING = "couldn't find env file";

export { ENV_FILE_MISSING, IDLE_TIMEOUT_MINUTES };

const PanelContext = createContext<PanelValue | undefined>(undefined);

let nextId = 1_000;
const mintId = () => ++nextId;
const nowISO = () => new Date().toISOString();

/**
 * The Volume Handle every fresh load starts with, on Compose 1's uploads bind,
 * and the container id that deploy minted.
 *
 * The Overview banner and the "already open" behaviour of the Volumes tab only
 * exist when a handle outlives the visit that created it, so a fresh load has to
 * show one to be reviewable at all — and putting it on the Volume with the demo
 * tree means the Explorer opens on its real content rather than on the empty
 * state a reviewer would then have to click past. The empty state is in the
 * states gallery, where it belongs. The container id is a literal rather than
 * `containerIdFor` so the seed reads as the deploy it stands for; a handle a
 * *scene* asks for is minted from its volume id instead.
 */
const SEEDED_HANDLE = { containerId: 'a41c9e77b3d2', volumeId: '1:/app/uploads' } as const;

/** How long ago the seed says the handle was opened and last used. */
const SEEDED_HANDLE_OPENED_MINUTES_AGO = 7;
const SEEDED_HANDLE_STARTED_MINUTES_AGO = 22;

/**
 * A handle in the shape the seed has: the app's uid, read and write, opened 7
 * minutes ago and started 22 minutes ago. Relative to *now*, so it follows the
 * frozen clock rather than fighting it.
 */
function seedHandle(volumeId: string, containerId: string): VolumeHandle {
  return {
    composeId: Number(volumeId.split(':')[0]),
    containerId,
    gid: 999,
    mode: 'rw',
    startedAt: new Date(Date.now() - SEEDED_HANDLE_STARTED_MINUTES_AGO * 60_000).toISOString(),
    uid: 999,
    uidSource: 'container-inspect',
    lastUsedAt: new Date(Date.now() - SEEDED_HANDLE_OPENED_MINUTES_AGO * 60_000).toISOString(),
    volumeId,
  };
}

/** The open handles a load starts with: the seed, or the volume ids a scene named. */
function seedHandles(volumeIds: string[]): Record<string, VolumeHandle> {
  return Object.fromEntries(
    volumeIds.map((volumeId) => [
      volumeId,
      seedHandle(volumeId, volumeId === SEEDED_HANDLE.volumeId ? SEEDED_HANDLE.containerId : containerIdFor(volumeId)),
    ]),
  );
}

export function PanelProvider({ children, overrides }: { children: ReactNode; overrides?: SceneOverrides }) {
  const [projects, setProjects] = useState<Project[]>(mockProjects);
  const [activity, setActivity] = useState<ActivityEntry[]>(overrides?.activity ?? mockActivity);
  const [logs, setLogs] = useState<Record<number, string>>(mockLogs);
  const [checkedAt, setCheckedAt] = useState<Record<number, string>>({});
  const [pending, setPending] = useState<Record<string, true>>({});
  const [speed, setSpeed] = useState<Speed>(overrides?.speed ?? 'normal');
  const [handles, setHandles] = useState<Record<string, VolumeHandle>>(() =>
    seedHandles(overrides?.handles ?? [SEEDED_HANDLE.volumeId]),
  );

  const wait = useCallback(
    (ms?: number) => new Promise<void>((resolve) => setTimeout(resolve, ms ?? LATENCY[speed])),
    [speed],
  );

  const track = useCallback(async <T,>(key: string, work: () => Promise<T> | T): Promise<T> => {
    setPending((current) => ({ ...current, [key]: true }));
    try {
      return await work();
    } finally {
      setPending((current) => {
        const next = { ...current };
        delete next[key];
        return next;
      });
    }
  }, []);

  const record = useCallback((entry: Omit<ActivityEntry, 'id' | 'at' | 'actor'>) => {
    setActivity((current) => [{ ...entry, id: mintId(), at: nowISO(), actor: 'admin@relist.local' }, ...current].slice(0, 40));
  }, []);

  const patchCompose = useCallback((composeId: number, patch: (compose: Compose) => Compose) => {
    setProjects((current) =>
      current.map((project) => ({
        ...project,
        environments: project.environments.map((environment) => ({
          ...environment,
          composes: environment.composes.map((compose) => (compose.id === composeId ? patch(compose) : compose)),
        })),
      })),
    );
  }, []);

  const withCompose = useCallback(
    (composeId: number, work: (location: ComposeLocation) => void) => {
      const location = findComposeLocation(projects, composeId);
      if (location) {
        work(location);
      }
    },
    [projects],
  );

  const lifecycle = useCallback(
    (composeId: number, action: 'deploy' | 'restart' | 'stop') =>
      track(`${action}:${composeId}`, async () => {
        withCompose(composeId, ({ compose, environment, project }) => {
          record({
            action: action === 'deploy' ? 'compose.deploy' : action === 'stop' ? 'compose.stop' : 'compose.restart',
            detail: `${action === 'deploy' ? 'Deployed' : action === 'stop' ? 'Stopped' : 'Restarted'} ${project.name} / ${environment.name} / ${compose.name}`,
            target: paths.compose(project.id, environment.id, compose.id),
            ok: true,
          });
        });
        await wait();
        patchCompose(composeId, (compose) => {
          const total = compose.services.length || compose.status.total || 1;
          if (action === 'stop') {
            return {
              ...compose,
              lastAction: 'stopped',
              services: compose.services.map((service) => ({ ...service, state: 'exited', health: 'none' })),
              status: { running: 0, total },
              updatedAt: nowISO(),
            };
          }
          const deployedAt = nowISO();
          return {
            ...compose,
            lastAction: action === 'deploy' ? 'deployed' : 'restarted',
            lastDeployedAt: deployedAt,
            services: compose.services.map((service) => ({
              ...service,
              // A deploy replaces the containers: new IDs, same service names —
              // which is why per-service history is keyed by name.
              containerId: action === 'deploy' ? containerIdFor(`${compose.id}-${service.name}-${deployedAt}`) : service.containerId,
              state: 'running',
              health: service.health === 'unhealthy' ? 'healthy' : service.health,
            })),
            status: { running: total, total },
            updatedAt: action === 'deploy' ? deployedAt : compose.updatedAt,
          };
        });
        setCheckedAt((current) => ({ ...current, [composeId]: nowISO() }));
      }),
    [patchCompose, record, track, wait, withCompose],
  );

  // The Monitoring screen loads metrics from an effect, so this method's
  // identity has to be stable: a fresh function on every pending-state update
  // would re-run that effect, and the refetch would feed itself.
  const loadMetrics = useCallback(
    (window: MetricWindow) =>
      track(`metrics:${window}`, async () => {
        await wait();
        return metricSamples(window);
      }),
    [track, wait],
  );

  // Same contract for the Compose screen's Monitor tab: it loads from an effect
  // too, and history is keyed by (Compose, service) rather than by container.
  const loadServiceMetrics = useCallback(
    (composeId: number, serviceName: string, window: MetricWindow) =>
      track(`service-metrics:${composeId}:${serviceName}:${window}`, async () => {
        await wait();
        return serviceMetricSamples(composeId, serviceName, window);
      }),
    [track, wait],
  );

  // A handle's idle clock runs from the last request it served, so any read or
  // write pushes the auto-stop back. Nothing touches it on a timer: a handle
  // nobody is using is exactly the one that should expire.
  const touchHandle = useCallback((volumeId: string) => {
    setHandles((current) => {
      const handle = current[volumeId];
      if (!handle) return current;
      return { ...current, [volumeId]: { ...handle, lastUsedAt: nowISO() } };
    });
  }, []);

  /**
   * Volume methods live outside the `value` memo, on the same contract as
   * `loadMetrics` above: the Compose tab and the Volume Explorer both call one
   * from an effect, and an identity that changed on every pending-state update
   * would re-run that effect — the refetch would feed itself. Measured the hard
   * way: React error #185, maximum update depth exceeded.
   */
  const resolveComposeVolumes = useCallback(
    (composeId: number) =>
      track(`volumes:${composeId}`, async () => {
        await wait(400);
        return resolveVolumes(composeId);
      }),
    [track, wait],
  );

  const startHandle = useCallback(
    (volumeId: string, input: StartHandleInput) =>
      track(`handle-start:${volumeId}`, async () => {
        await wait();
        const openedAt = nowISO();
        setHandles((current) => ({
          ...current,
          [volumeId]: {
            composeId: Number(volumeId.split(':')[0]),
            containerId: containerIdFor(`handle-${volumeId}-${openedAt}`),
            gid: input.gid,
            lastUsedAt: openedAt,
            mode: input.mode,
            startedAt: openedAt,
            uid: input.uid,
            uidSource: input.uidSource,
            volumeId,
          },
        }));
      }),
    [track, wait],
  );

  const stopHandle = useCallback(
    (volumeId: string) =>
      track(`handle-stop:${volumeId}`, async () => {
        await wait();
        setHandles((current) => {
          const next = { ...current };
          delete next[volumeId];
          return next;
        });
      }),
    [track, wait],
  );

  const listDir = useCallback(
    (volumeId: string, path: string) =>
      track(`browse:${volumeId}:${path}`, async () => {
        await wait(500);
        touchHandle(volumeId);
        const reason = unreadableReason(volumeId, path);
        if (reason) {
          throw new Error(reason);
        }
        return listVolumeDir(volumeId, path);
      }),
    [touchHandle, track, wait],
  );

  const readVolumeFile = useCallback(
    (volumeId: string, path: string) =>
      track(`read:${volumeId}:${path}`, async () => {
        await wait(400);
        touchHandle(volumeId);
        return fixtureFileContents(path);
      }),
    [touchHandle, track, wait],
  );

  const writeVolumeFile = useCallback(
    (volumeId: string, path: string) =>
      track(`write:${volumeId}:${path}`, async () => {
        await wait();
        touchHandle(volumeId);
      }),
    [touchHandle, track, wait],
  );

  const value = useMemo<PanelValue>(
    () => ({
      activity,
      checkedAt,
      handles,
      listDir,
      host: mockHost,
      logs,
      pending,
      projects,
      readVolumeFile,
      resolveComposeVolumes,
      speed,
      setSpeed,
      startHandle,
      stopHandle,
      writeVolumeFile,
      isPending: (key) => Boolean(pending[key]),

      createProject: (input) =>
        track('create-project', async () => {
          const project: Project = {
            id: mintId(),
            name: input.name,
            description: input.description,
            environmentCount: 0,
            createdAt: nowISO(),
            lastUpdated: nowISO(),
            environments: [],
          };
          await wait();
          setProjects((current) => [project, ...current]);
          record({ action: 'project.create', detail: `Created Project ${project.name}`, target: `#/projects/${project.id}`, ok: true });
          return project;
        }),

      updateProject: (projectId, input) =>
        track(`save-project:${projectId}`, async () => {
          await wait();
          setProjects((current) =>
            current.map((project) =>
              project.id === projectId
                ? { ...project, name: input.name, description: input.description, lastUpdated: nowISO() }
                : project,
            ),
          );
        }),

      deleteProject: (projectId) =>
        track(`delete-project:${projectId}`, async () => {
          await wait();
          setProjects((current) => current.filter((project) => project.id !== projectId));
        }),

      addEnvironment: (projectId, name) =>
        track(`create-environment:${projectId}`, async () => {
          await wait();
          setProjects((current) =>
            current.map((project) =>
              project.id === projectId
                ? {
                    ...project,
                    environmentCount: project.environmentCount + 1,
                    lastUpdated: nowISO(),
                    environments: [
                      ...project.environments,
                      { id: mintId(), projectId, name, createdAt: nowISO(), variables: [], composes: [] },
                    ],
                  }
                : project,
            ),
          );
        }),

      deleteEnvironment: (projectId, environmentId) =>
        track(`delete-environment:${environmentId}`, async () => {
          await wait();
          setProjects((current) =>
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
        }),

      saveVariable: (environmentId, variable, variableId) =>
        track(`save-variable:${environmentId}`, async () => {
          await wait();
          setProjects((current) =>
            current.map((project) => ({
              ...project,
              environments: project.environments.map((environment) => {
                if (environment.id !== environmentId) return environment;
                const variables: EnvironmentVariable[] = variableId
                  ? environment.variables.map((existing) =>
                      existing.id === variableId ? { ...existing, ...variable, updatedAt: nowISO() } : existing,
                    )
                  : [
                      ...environment.variables,
                      { id: mintId(), environmentId, updatedAt: nowISO(), ...variable },
                    ];
                return { ...environment, variables };
              }),
            })),
          );
          record({
            action: 'variable.update',
            detail: `${variableId ? 'Updated' : 'Added'} variable ${variable.key}`,
            target: '#/projects',
            ok: true,
          });
        }),

      deleteVariable: (environmentId, variableId) =>
        track(`delete-variable:${variableId}`, async () => {
          await wait();
          setProjects((current) =>
            current.map((project) => ({
              ...project,
              environments: project.environments.map((environment) =>
                environment.id === environmentId
                  ? { ...environment, variables: environment.variables.filter((v) => v.id !== variableId) }
                  : environment,
              ),
            })),
          );
        }),

      replaceVariables: (environmentId, rows) =>
        track(`bulk-variables:${environmentId}`, async () => {
          await wait(900);
          setProjects((current) =>
            current.map((project) => ({
              ...project,
              environments: project.environments.map((environment) =>
                environment.id === environmentId
                  ? {
                      ...environment,
                      variables: rows.map((row) => ({ id: mintId(), environmentId, updatedAt: nowISO(), ...row })),
                    }
                  : environment,
              ),
            })),
          );
        }),

      createCompose: (environmentId, name, yamlContent) =>
        track(`create-compose:${environmentId}`, async () => {
          await wait();
          let created: Compose | undefined;
          setProjects((current) =>
            current.map((project) => ({
              ...project,
              environments: project.environments.map((environment) => {
                if (environment.id !== environmentId) return environment;
                const compose: Compose = {
                  id: mintId(),
                  environmentId,
                  name,
                  projectSlug: 'new-compose',
                  envSlug: environment.name,
                  yamlContent,
                  createdAt: nowISO(),
                  updatedAt: nowISO(),
                  status: { running: 0, total: 0 },
                  services: [],
                };
                created = compose;
                return { ...environment, composes: [...environment.composes, compose] };
              }),
            })),
          );
          if (!created) {
            throw new Error('Environment not found');
          }
          return created;
        }),

      saveCompose: (composeId, yamlContent) =>
        track(`save-compose:${composeId}`, async () => {
          await wait();
          withCompose(composeId, ({ compose, environment, project }) => {
            record({
              action: 'compose.save',
              detail: `Saved docker-compose.yml for ${project.name} / ${environment.name} / ${compose.name}`,
              target: paths.compose(project.id, environment.id, compose.id),
              ok: true,
            });
          });
          patchCompose(composeId, (compose) => ({ ...compose, yamlContent, updatedAt: nowISO(), lastAction: 'saved' }));
        }),

      deployCompose: (composeId) => lifecycle(composeId, 'deploy'),
      stopCompose: (composeId) => lifecycle(composeId, 'stop'),
      restartCompose: (composeId) => lifecycle(composeId, 'restart'),

      deleteCompose: (composeId) =>
        track(`delete-compose:${composeId}`, async () => {
          await wait();
          setProjects((current) =>
            current.map((project) => ({
              ...project,
              environments: project.environments.map((environment) => ({
                ...environment,
                composes: environment.composes.filter((compose) => compose.id !== composeId),
              })),
            })),
          );
        }),

      refreshStatus: (composeId) =>
        track(`status:${composeId}`, async () => {
          await wait(500);
          setCheckedAt((current) => ({ ...current, [composeId]: nowISO() }));
        }),

      loadLogs: (composeId) =>
        track(`logs:${composeId}`, async () => {
          await wait(700);
          const text = logs[composeId] ?? 'No logs available yet.';
          if (!logs[composeId]) {
            setLogs((current) => ({ ...current, [composeId]: text }));
          }
          return text;
        }),

      loadMetrics,
      loadServiceMetrics,
    }),
    [
      activity,
      checkedAt,
      handles,
      lifecycle,
      listDir,
      loadMetrics,
      loadServiceMetrics,
      logs,
      patchCompose,
      pending,
      projects,
      readVolumeFile,
      record,
      resolveComposeVolumes,
      speed,
      startHandle,
      stopHandle,
      track,
      withCompose,
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

/** Auth is mocked: the design only needs the three screens, not a session. */
export function useAuthMock() {
  // The prototype starts signed in so every screen is one click away; opening
  // #/signin or #/setup, or using the user menu's Sign out, shows the auth flow.
  const [signedIn, setSignedIn] = useState(true);
  return { signedIn, signIn: () => setSignedIn(true) };
}
