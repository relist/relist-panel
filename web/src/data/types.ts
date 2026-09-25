/**
 * Domain types for the redesign, mirroring the shapes the Go API returns
 * (`internal/projects`, `internal/environments`, `internal/environmentvariables`,
 * `internal/composes`) so the port maps onto real endpoints. Anything marked
 * `@design-only` is a proposal from the prototype: it has no API backing yet
 * and is called out in GUIDELINES.md under "API gaps".
 *
 * Deliberately not ported from the prototype's `types.ts`: the activity feed
 * (the API records no events — the Activity cards stay omitted). The monitoring
 * pieces arrive with `.scratch/host-monitoring`, below.
 */

export type EnvironmentVariable = {
  id: number;
  environmentId: number;
  key: string;
  /**
   * The user's display tier, always present on the wire: a marked value is
   * masked on screen, and the flag does nothing else — the value is stored
   * and served exactly as a plain one (not a security boundary).
   */
  secret: boolean;
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

/** One Service of a Compose, as the rail and the Monitor tab render it. */
export type ComposeService = {
  name: string;
  image: string;
  state: ContainerState;
  health: ServiceHealth;
  ports: string[];
  /**
   * The container this service currently maps to (the Services endpoint's
   * collector cache). One container per deploy, so this is identity for a
   * single deploy and never a history key — the service name is.
   */
  containerId?: string;
};

/**
 * The Services endpoint's per-Service facts (`…/composes/{c}/services`,
 * straight from the container collector's cache). Field names are the wire's;
 * `serviceFromFacts` adapts them onto `ComposeService` for the screens.
 */
export type ServiceFacts = {
  service: string;
  containerId: string;
  containerName: string;
  image: string;
  state: string;
  health: string;
  ports: { hostIp: string; hostPort: number; containerPort: number; protocol: string }[];
};

const CONTAINER_STATES: readonly ContainerState[] = ['created', 'exited', 'restarting', 'running'];
const SERVICE_HEALTHS: readonly ServiceHealth[] = ['healthy', 'none', 'starting', 'unhealthy'];

function pick<V extends string>(value: string, known: readonly V[], fallback: V): V {
  return (known as readonly string[]).includes(value) ? (value as V) : fallback;
}

/** Docker's published port form: `0.0.0.0:8080->80/tcp`, wildcard IP dropped. */
export function formatPort(port: ServiceFacts['ports'][number]): string {
  const exposed = `${port.containerPort}/${port.protocol}`;
  if (!port.hostPort) {
    return exposed;
  }
  // Only IPv6 literals need the brackets; docker prints IPv4 bare.
  const wildcard = port.hostIp === '' || port.hostIp === '0.0.0.0' || port.hostIp === '::';
  const host = wildcard ? '' : port.hostIp.includes(':') ? `[${port.hostIp}]` : port.hostIp;
  return `${host ? `${host}:` : ''}${port.hostPort}->${exposed}`;
}

/** Docker's short container ID — the form the facts strip shows (and copies). */
export function shortContainerId(containerId: string): string {
  return containerId.slice(0, 12);
}

/** Wire facts → screen row. States the vocabulary doesn't know render as `created`. */
export function serviceFromFacts(facts: ServiceFacts): ComposeService {
  return {
    containerId: facts.containerId || undefined,
    health: pick(facts.health, SERVICE_HEALTHS, 'none'),
    image: facts.image,
    name: facts.service,
    ports: facts.ports.map(formatPort),
    state: pick(facts.state, CONTAINER_STATES, 'created'),
  };
}

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

// ---- Metrics (.scratch/host-monitoring) --------------------------------------
//
// The endpoints serve an envelope naming the window, which store answered it
// (`ring` or `rollups`), the samples' resolution, whether the data covers the
// window (`partial` — still collecting after a restart), the `sources`
// availability block, and the samples at the window's natural resolution;
// downsampling is the chart's job.

/** Windows the Monitoring screen offers — exactly what the endpoint validates. */
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
 * A field reads `null` when the sampler had no honest reading for it (an
 * unreadable source, a rateless first tick): the API encodes NaN as JSON null,
 * and an absent reading must never be plotted as a zero.
 */
export type MetricSample = {
  at: number;
  cpu: number | null;
  memUsed: number | null;
  memTotal: number | null;
  diskUsed: number | null;
  diskTotal: number | null;
  dockerUsed: number | null;
  dockerTotal: number | null;
  blkRead: number | null;
  blkWrite: number | null;
  netRx: number | null;
  netTx: number | null;
};

/**
 * One container sample, keyed by (Compose, service) rather than by container:
 * a deploy mints a new container, the service name does not change, and history
 * that reset on every deploy could never show what a deploy did. `null` means
 * no honest reading, as on the host sample. `cpu` is percent of one core and
 * can exceed 100; `memUsed` is usage minus page cache.
 */
export type ServiceSample = {
  at: number;
  cpu: number | null;
  memUsed: number | null;
  memLimit: number | null;
  netRx: number | null;
  netTx: number | null;
  blkRead: number | null;
  blkWrite: number | null;
  pids: number | null;
};

/** Whether a metrics source was readable at the last tick, and why not when not. */
export type SourceStatus = {
  available: boolean;
  reason?: string;
};

export type MetricsEnvelope<Sample> = {
  window: MetricWindow;
  /** Which store served the window: the in-memory ring or the SQLite rollups. */
  from: 'ring' | 'rollups';
  intervalSeconds: number;
  /** True when the samples cover less than the window — the restart seam. */
  partial: boolean;
  sources: Record<string, SourceStatus>;
  samples: Sample[];
};

export type HostMetrics = MetricsEnvelope<MetricSample>;

/** The Service envelope's one source is the Docker stats API sweep. */
export type ServiceMetrics = MetricsEnvelope<ServiceSample>;
