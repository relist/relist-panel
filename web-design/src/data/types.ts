/**
 * Domain types for the redesign mock.
 *
 * These intentionally mirror the shapes the Go API already returns
 * (`internal/projects`, `internal/environments`, `internal/environmentvariables`,
 * `internal/composes`) so the redesign maps onto real endpoints later. Anything
 * marked `@design-only` is a proposal: it has no API backing yet and is called
 * out in GUIDELINES.md under "API gaps".
 */

export type EnvironmentVariable = {
  id: number;
  environmentId: number;
  key: string;
  /** @design-only: the API returns the real value; the mock marks a few as secret. */
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
  /** Loaded per-environment by the mock store; the API exposes this as its own collection. */
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
  /** @design-only: drives the activity feed and "last deployed" copy. */
  lastDeployedAt?: string;
  /** @design-only */
  lastAction?: 'deployed' | 'stopped' | 'restarted' | 'saved';
  /** @design-only */
  services: ComposeService[];
};

export type ActivityEntry = {
  id: number;
  at: string;
  actor: string;
  action: 'compose.deploy' | 'compose.stop' | 'compose.restart' | 'compose.save' | 'compose.delete' | 'variable.update' | 'project.create';
  detail: string;
  target: string;
  ok: boolean;
};

export type HostInfo = {
  name: string;
  dockerVersion: string;
  traefikVersion: string;
  dataDir: string;
  network: string;
};

/** Windows the Monitoring screen offers. */
export type MetricWindow = '15m' | '1h' | '6h' | '24h';

/**
 * Resolution is part of the design contract, not an implementation detail: 10s
 * inside the hour is what makes a 30-second spike visible, and a 24-hour window
 * at that resolution is 8,640 points of noise. Beyond the hour the panel rolls
 * the samples up to one minute.
 */
export const METRIC_WINDOWS = {
  '15m': { intervalSeconds: 10, windowSeconds: 900 },
  '1h': { intervalSeconds: 10, windowSeconds: 3_600 },
  '6h': { intervalSeconds: 60, windowSeconds: 21_600 },
  '24h': { intervalSeconds: 60, windowSeconds: 86_400 },
} as const satisfies Record<MetricWindow, { intervalSeconds: number; windowSeconds: number }>;

/**
 * Per-container sampling is deliberately slower than the host's 10s: a host
 * reads four /proc files, a container is a `docker stats` call per service, and
 * an operator reads a service's shape in minutes, not seconds. Inside the hour
 * 30s still shows a restart; beyond it the 1-minute rollups are enough.
 */
export const SERVICE_METRIC_WINDOWS = {
  '15m': { intervalSeconds: 30, windowSeconds: 900 },
  '1h': { intervalSeconds: 30, windowSeconds: 3_600 },
  '6h': { intervalSeconds: 60, windowSeconds: 21_600 },
  '24h': { intervalSeconds: 60, windowSeconds: 86_400 },
} as const satisfies Record<MetricWindow, { intervalSeconds: number; windowSeconds: number }>;

/**
 * One host sample. `at` is epoch ms, the `*Used` / `*Total` fields are bytes,
 * and the I/O fields are already rates (bytes/s) — no counter deltas to keep.
 * @design-only: no endpoint returns this yet — see GUIDELINES.md, "API gaps".
 */
export type MetricSample = {
  at: number;
  cpu: number;
  memUsed: number;
  memTotal: number;
  diskUsed: number;
  diskTotal: number;
  dockerUsed: number;
  dockerTotal: number;
  blkRead: number;
  blkWrite: number;
  netRx: number;
  netTx: number;
};

/**
 * One container sample, keyed by (Compose, service) rather than by container:
 * a deploy mints a new container, the service name does not change, and history
 * that reset on every deploy could never show what a deploy did.
 * @design-only: see GUIDELINES.md, "API gaps".
 */
export type ServiceSample = {
  at: number;
  cpu: number; // percent of one core — can exceed 100
  memUsed: number; // bytes, page cache already subtracted
  memLimit: number;
  netRx: number; // bytes/s
  netTx: number;
  blkRead: number; // bytes/s
  blkWrite: number;
  pids: number;
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
