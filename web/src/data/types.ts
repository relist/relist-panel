/**
 * Domain types for the redesign, mirroring the shapes the Go API returns
 * (`internal/projects`, `internal/environments`, `internal/environmentvariables`,
 * `internal/composes`) so the port maps onto real endpoints. Anything marked
 * `@design-only` is a proposal from the prototype: it has no API backing yet
 * and is called out in GUIDELINES.md under "API gaps".
 *
 * Deliberately not ported from the prototype's `types.ts`: the monitoring
 * pieces (`MetricSample`, `ServiceSample`, `MetricWindow`, the window tables)
 * and the activity feed — they belong to `.scratch/host-monitoring` and to the
 * omitted Activity cards, and no endpoint returns them yet.
 */

export type EnvironmentVariable = {
  id: number;
  environmentId: number;
  key: string;
  /** @design-only: the API returns the real value, unmarked. */
  secret?: boolean;
  updatedAt: string;
  value: string;
};

export type Environment = {
  id: number;
  projectId: number;
  name: string;
  createdAt: string;
  variables: EnvironmentVariable[];
  /** Resolved from its own collection by the store; the API nests it under the environment path. */
  composes: Compose[];
};

export type Project = {
  id: number;
  name: string;
  description: string;
  environmentCount: number;
  createdAt: string;
  lastUpdated: string;
  environments: Environment[];
};

export type ContainerState = 'created' | 'exited' | 'restarting' | 'running';

export type ServiceHealth = 'healthy' | 'none' | 'starting' | 'unhealthy';

/** @design-only: per-service detail. Today the API only returns running/total. */
export type ComposeService = {
  name: string;
  image: string;
  state: ContainerState;
  health: ServiceHealth;
  ports: string[];
  /**
   * The container this service currently maps to (`docker compose ps --format
   * json`). One container per deploy, so this is identity for a single deploy
   * and never a history key — the service name is.
   */
  containerId?: string;
};

export type ComposeStatus = {
  running: number;
  total: number;
};

export type Compose = {
  id: number;
  environmentId: number;
  name: string;
  /** Immutable deploy identity, per ADR-0005. */
  projectSlug: string;
  envSlug: string;
  yamlContent: string;
  createdAt: string;
  updatedAt: string;
  status: ComposeStatus;
  /** @design-only: the API returns no deploy timestamp; the UI treats it as unknown. */
  lastDeployedAt?: string;
  /** @design-only */
  services: ComposeService[];
};

/** Derived lifecycle state of a Compose. Single source of truth for status colour and copy. */
export type ComposeState = 'not-deployed' | 'partial' | 'running' | 'stopped';

export function composeState(status: ComposeStatus, lastDeployedAt?: string): ComposeState {
  if (status.total === 0) {
    return lastDeployedAt ? 'stopped' : 'not-deployed';
  }
  if (status.running === 0) {
    return 'stopped';
  }
  return status.running === status.total ? 'running' : 'partial';
}

export type ComposeSummary = {
  running: number;
  total: number;
  composes: number;
};

export function summarize(composes: Compose[]): ComposeSummary {
  return composes.reduce<ComposeSummary>(
    (acc, compose) => ({
      running: acc.running + compose.status.running,
      total: acc.total + compose.status.total,
      composes: acc.composes + 1,
    }),
    { running: 0, total: 0, composes: 0 },
  );
}

export function projectComposes(project: Project): Compose[] {
  return project.environments.flatMap((environment) => environment.composes);
}

export function findCompose(projects: Project[], composeId: number): Compose | undefined {
  return findComposeLocation(projects, composeId)?.compose;
}

export type ComposeLocation = {
  project: Project;
  environment: Environment;
  compose: Compose;
};

export function findComposeLocation(projects: Project[], composeId: number): ComposeLocation | undefined {
  for (const project of projects) {
    for (const environment of project.environments) {
      const compose = environment.composes.find((candidate) => candidate.id === composeId);
      if (compose) {
        return { project, environment, compose };
      }
    }
  }
  return undefined;
}

export type EnvironmentLocation = {
  project: Project;
  environment: Environment;
};

export function findEnvironmentLocation(
  projects: Project[],
  environmentId: number,
): EnvironmentLocation | undefined {
  for (const project of projects) {
    const environment = project.environments.find((candidate) => candidate.id === environmentId);
    if (environment) {
      return { project, environment };
    }
  }
  return undefined;
}
