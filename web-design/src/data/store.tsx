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
} from './types';
import { findComposeLocation } from './types';

/**
 * In-memory stand-in for the panel's HTTP API. Every mutation is local and
 * resolves after `latency`, so loading, pending and success states in the
 * redesign behave exactly as they would against the real backend.
 */

export type Speed = 'instant' | 'normal' | 'slow';

const LATENCY: Record<Speed, number> = { instant: 80, normal: 650, slow: 1_800 };

export type NewVariable = { key: string; value: string };

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
  host: HostInfo;
  isPending: (key: string) => boolean;
  logs: Record<number, string>;
  loadLogs: (composeId: number) => Promise<string>;
  loadMetrics: (window: MetricWindow) => Promise<MetricSample[]>;
  loadServiceMetrics: (composeId: number, serviceName: string, window: MetricWindow) => Promise<ServiceSample[]>;
  projects: Project[];
  refreshStatus: (composeId: number) => Promise<void>;
  restartCompose: (composeId: number) => Promise<void>;
  saveCompose: (composeId: number, yamlContent: string) => Promise<void>;
  saveVariable: (environmentId: number, variable: NewVariable, variableId?: number) => Promise<void>;
  deleteVariable: (environmentId: number, variableId: number) => Promise<void>;
  replaceVariables: (environmentId: number, rows: NewVariable[]) => Promise<void>;
  setSpeed: (speed: Speed) => void;
  speed: Speed;
  stopCompose: (composeId: number) => Promise<void>;
  updateProject: (projectId: number, input: { name: string; description: string }) => Promise<void>;
};

const PanelContext = createContext<PanelValue | undefined>(undefined);

let nextId = 1_000;
const mintId = () => ++nextId;
const nowISO = () => new Date().toISOString();

export function PanelProvider({ children }: { children: ReactNode }) {
  const [projects, setProjects] = useState<Project[]>(mockProjects);
  const [activity, setActivity] = useState<ActivityEntry[]>(mockActivity);
  const [logs, setLogs] = useState<Record<number, string>>(mockLogs);
  const [checkedAt, setCheckedAt] = useState<Record<number, string>>({});
  const [pending, setPending] = useState<Record<string, true>>({});
  const [speed, setSpeed] = useState<Speed>('normal');

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

  const value = useMemo<PanelValue>(
    () => ({
      activity,
      checkedAt,
      host: mockHost,
      logs,
      projects,
      speed,
      setSpeed,
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
    [activity, checkedAt, lifecycle, loadMetrics, loadServiceMetrics, logs, patchCompose, pending, projects, record, speed, track, wait, withCompose],
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
