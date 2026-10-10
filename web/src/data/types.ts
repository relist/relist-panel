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

/** A key-value pair scoped to one Compose; it overrides an Environment Variable of the same key. */
export type ComposeVariable = {
  id: number;
  composeId: number;
  key: string;
  secret: boolean;
  value: string;
};

/** One key-value pair of a Variable Set. */
export type SetEntry = {
  id: number;
  setId: number;
  key: string;
  secret: boolean;
  value: string;
};

/** A Service that binds a Variable Set. */
export type SetUsage = {
  composeId: number;
  composeName: string;
  service: string;
};

/** A named list of key-value pairs belonging to an Environment, delivered only to the Services bound to it. */
export type VariableSet = {
  id: number;
  environmentId: number;
  name: string;
  createdAt: string;
  updatedAt: string;
  entries: SetEntry[];
  usedBy: SetUsage[];
};

/** One Service's bindings: set ids in binding order (position = precedence). */
export type ServiceBindings = {
  service: string;
  setIds: number[];
};

/** Where an effective variable comes from; the server's answer, never derived client-side. */
export type VariableSource = 'environment' | 'compose' | 'yaml' | { set: { id: number; name: string } };

/** One row of a Service's effective environment; `overrides` are the shadowed layers, lowest first; a trailing `yaml` means the YAML's `environment:` also sets the key and wins. */
export type EffectiveVariable = {
  key: string;
  value: string;
  secret: boolean;
  source: VariableSource;
  overrides: VariableSource[];
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
  /** Actual Docker name and configured directory, resolved by the backend for either identity version. */
  deployIdentity?: { projectName: string; directory: string };
  yamlContent: string;
  createdAt: string;
  updatedAt: string;
  status: ComposeStatus;
  /**
   * When the API last deployed this Compose, written only on deploy success —
   * `null` (or absent) means never deployed. Composes deployed before the
   * panel recorded stamps also read `null`.
   */
  lastDeployedAt?: string | null;
  /** When a domain of this Compose last changed, or `null` when none ever has. Set by the API on every domain write. */
  domainsChangedAt?: string | null;
  /** When a Compose Variable of this Compose last changed, or `null` when none ever has. Set by the API on every write. */
  variablesChangedAt?: string | null;
  /** Deploy-time notes from the API (overwritten Traefik labels, router hints); present on deploy responses only. */
  warnings?: string[];
};

export type CertProvider = 'none' | 'letsencrypt' | 'custom';

/** A Domain's writable fields, as the domains API takes them. */
export type DomainInput = {
  host: string;
  path: string;
  internalPath: string;
  stripPath: boolean;
  service: string;
  port: number;
  entrypoint: string;
  https: boolean;
  certProvider: CertProvider;
  certResolver: string;
  middlewares: string[];
};

export type Domain = DomainInput & {
  id: number;
  createdAt: string;
  updatedAt: string;
};

/** `GET …/domains`: the Compose's domains and the Services a domain can point at. */
export type ComposeDomains = {
  domains: Domain[];
  services: string[];
};

/** The file a deploy would write for a draft, and the warnings it would return. */
export type ComposePreview = {
  yaml: string;
  warnings: string[];
};

/** Derived lifecycle state of a Compose. Single source of truth for status colour and copy. */
export type ComposeState = 'not-deployed' | 'partial' | 'running' | 'stopped';

export function composeState(status: ComposeStatus, lastDeployedAt?: string | null): ComposeState {
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

// ---- Volume files (.scratch/volume-files) ------------------------------------
//
// One Compose's resolved Volumes, from `GET .../composes/{c}/volumes`. Every
// path here comes from `docker compose config --format json` — the panel never
// parses YAML for a source and never accepts a host path from a form — and the
// pre-flight is the API's check of whether the source may be mounted at all.

/** How a Volume is realised on the host, matching the Compose CLI's `type`. */
export type VolumeKind = 'volume' | 'bind';

/** One Volume's pre-flight outcome: checked and present, checked and absent, or not checkable. */
export type VolumePreflightStatus = 'ok' | 'missing' | 'unverifiable';

/** One Service's declaration of a Volume: the container-side path and whether that mount is `:ro`. */
export type VolumeUse = {
  service: string;
  target: string;
  readOnly: boolean;
};

/**
 * One Volume's pre-flight result. `warning` is the API's own explanation —
 * empty for `ok`, and naming the consequence otherwise (an unverifiable bind
 * becomes `root:root 0755` if a mount creates it).
 */
export type VolumePreflight = {
  status: VolumePreflightStatus;
  warning: string;
};

/**
 * One source a Compose mounts, grouped across its Services. For a named
 * volume, `logicalName` is its key in the top-level `volumes:` map and
 * `source` is its real Docker name; for a bind, `logicalName` is empty and
 * `source` is the resolved absolute host path.
 */
export type ComposeVolume = {
  kind: VolumeKind;
  logicalName: string;
  source: string;
  uses: VolumeUse[];
  preflight: VolumePreflight;
};

/** Where a Service's operation identity came from. */
export type VolumeUserSource = 'compose' | 'fallback';

/** One Service's uid/gid resolution; `warning` is non-empty only for the `0:0` fallback. */
export type VolumeServiceUser = {
  service: string;
  user: string;
  source: VolumeUserSource;
  warning: string;
};

/**
 * One Compose's resolved Volumes and Service identities. `available: false`
 * means the feature cannot run at all — a bare binary or no Docker socket —
 * and `reason` says why; the Volumes tab is hidden in that case.
 */
export type ComposeVolumes = {
  available: boolean;
  reason?: string;
  volumes: ComposeVolume[];
  services: VolumeServiceUser[];
};

/** A named Volume Docker holds under this Compose's project label that its file no longer declares. */
export type OrphanVolume = { name: string; createdAt: string };

/** What one entry of a directory listing is, per the helper's `list`/`stat` JSON. */
export type VolumeEntryKind = 'file' | 'directory' | 'symlink' | 'other';

/**
 * One entry of a directory listing (`GET …/volumes/list`), exactly as
 * `relist-volhelper list` reports it. `mode` is the raw `fs.FileMode` value —
 * the low 9 bits are the permission bits, the high bits carry the type — and
 * `mtime` is RFC 3339. `link` is a symlink's target as stored: shown, never
 * silently resolved, and empty (absent on the wire) for everything else.
 */
export type VolumeEntry = {
  name: string;
  kind: VolumeEntryKind;
  size: number;
  mode: number;
  mtime: string;
  uid: number;
  gid: number;
  link?: string;
};

/** Identity and acknowledged mount risks for every Volume operation. */
export type VolumeAccess = {
  service?: string;
  user?: string;
  rootConfirmed?: boolean;
  bindConfirmed?: boolean;
};

// ---- Compose terminal (.scratch/compose-terminal) ----------------------------
//
// The Terminal tab's Service listing, from `GET …/terminal/services`. The API
// probes each running Container for bash and sh; `available: false` means the
// Engine API is unreachable at all and `reason` says why.

/** One Compose's Services and their running Containers, with the shells each carries. */
export type TerminalServices = {
  available: boolean;
  reason: string;
  services: {
    name: string;
    containers: {
      id: string;
      name: string;
      number: number;
      shells: { bash: boolean; sh: boolean };
    }[];
  }[];
};

/** One image on the host's Docker engine, from `GET /api/images`. */
export type HostImage = { id: string; tags: string[]; size: number; created: string; inUse: boolean };
