/**
 * The panel's HTTP API client. One thin wrapper per endpoint, lifted from the
 * fetch call sites in the monolith (`web/src/main.tsx`) — same paths, same
 * methods, same JSON shapes. `request` turns non-2xx responses into rejected
 * promises carrying the API's `error` message, so store callers can render it
 * directly.
 */

import type {
  DockerReport,
  DockerPruneKind,
  DockerPrunePlan,
  DockerPruneResult,
  Compose,
  ComposeDomains,
  ComposeVariable,
  ComposePreview,
  ComposeSource,
  ComposeVolumes,
  Deployment,
  DeploymentHistoryQuery,
  GlobalDeploymentPage,
  GlobalQueuedDeployment,
  DeploymentLog,
  DeploymentRequest,
  DetectedDockerfile,
  QueuedDeployment,
  SourceInput,
  SourceTestResult,
  SshKey,
  ScannerTag,
  VulnCvePage,
  VulnCveQuery,
  VulnFinding,
  VulnImage,
  VulnSettings,
  VulnSettingsResponse,
  VulnStatus,
  VulnSummary,
  Domain,
  DomainInput,
  Environment,
  EnvironmentVariable,
  HostImage,
  HostMetrics,
  OrphanVolume,
  Project,
  RemovalPlanEntry,
  ServiceBindings,
  SetEntry,
  VariableSet,
  EffectiveVariable,
  ServiceFacts,
  ServiceMetrics,
  TerminalServices,
  VolumeAccess,
  VolumeListing,
} from './types';
import type { MetricWindow } from './types';
import type { MigrationStatus } from './migration';

export type AuthStatus = {
  authenticated: boolean;
};

/** The signed-in admin account: its email and whether two-factor is on. */
export type Account = {
  email: string;
  twoFactorEnabled: boolean;
  /** The password is still the one RELIST_ADMIN_PASSWORD seeded, on installs from before create-admin. */
  passwordFromEnv: boolean;
};

/** The setup answer: the base32 secret for manual entry and the URI the QR code encodes. */
export type TwoFactorSetup = {
  secret: string;
  otpauthUri: string;
};

export type Credentials = {
  email: string;
  password: string;
};

/** A sign-in attempt: `code` is present only on the second, two-factor step. */
export type LoginInput = Credentials & { code?: string };

/**
 * The three answers a sign-in attempt can settle on. Two of them are not
 * failures: `codeRequired` is the API asking for the second factor, and
 * `rateLimited` is the lock, carrying how long to wait. A wrong password or
 * code stays a thrown `ApiError` — there is nothing to do but show it.
 */
export type LoginResult =
  | { kind: 'signedIn' }
  | { kind: 'codeRequired' }
  | { kind: 'rateLimited'; retryAfterSeconds: number };

export type ProjectInput = {
  name: string;
  description: string;
};

export type EnvironmentInput = {
  name: string;
};

export type EnvironmentVariableInput = {
  key: string;
  value: string;
  /** Absent on the wire means false; the client always sends its answer. */
  secret: boolean;
};

export type ComposeCreateInput = {
  name: string;
  yamlContent: string;
};

export type ComposeUpdateInput = {
  yamlContent: string;
};

export type LogsPayload = {
  logs: string;
};

type ErrorResponse = {
  error?: string;
};

/**
 * A non-2xx API response: the API's own message, plus the HTTP status. The
 * write family's view helpers map the status to the limit that was hit (413's
 * 1 GiB cap, 507's free space); every other caller keeps reading `.message`
 * exactly as it did before the status existed.
 */
export class ApiError extends Error {
  readonly status: number;
  /** The request field the API refused, when its 400 names one (e.g. `host`). */
  readonly field?: string;

  constructor(status: number, message: string, field?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.field = field;
  }
}

/** The API's `error` message from a response body, or a status-based one when the body carries none. */
function errorMessage(status: number, bodyText: string): string {
  return errorBody(status, bodyText).message;
}

/** The message and, when the body names one, the refused field. */
function errorBody(status: number, bodyText: string): { message: string; field?: string } {
  try {
    const payload = JSON.parse(bodyText) as (ErrorResponse & { field?: string }) | null;
    if (payload?.error) {
      return {
        message: payload.error,
        field: payload.field || undefined,
      };
    }
  } catch {
    // Fall through to a status-based message when the response is not JSON.
  }
  return { message: `Request failed (${status})` };
}

async function responseError(response: Response): Promise<string> {
  return errorMessage(response.status, await response.text().catch(() => ''));
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...init });
  if (!response.ok) {
    const { message, field } = errorBody(response.status, await response.text().catch(() => ''));
    throw new ApiError(response.status, message, field);
  }
  // Deletes, logout and an accepted cancel (202) answer with no body to parse.
  if (response.status === 204) {
    return undefined as T;
  }
  const text = await response.text();
  if (text === '') {
    return undefined as T;
  }
  return JSON.parse(text) as T;
}

/**
 * Sign-in's `401` body, parsed once: `codeRequired` is the API asking for the
 * second factor rather than reporting a bad password, so the flag has to be
 * read before the error message is turned into an `ApiError`.
 */
type LoginErrorResponse = ErrorResponse & { codeRequired?: boolean };

async function loginErrorResponse(response: Response): Promise<LoginErrorResponse> {
  try {
    const payload = (await response.json()) as unknown;
    if (payload && typeof payload === 'object') {
      return payload as LoginErrorResponse;
    }
  } catch {
    // A non-JSON body carries no `codeRequired`; the caller falls back to a status message.
  }
  return {};
}

/** The lock's wait, in whole seconds; the server always sends at least one. */
function retryAfterSeconds(header: string | null): number {
  const seconds = Number(header);
  return Number.isFinite(seconds) && seconds >= 1 ? Math.ceil(seconds) : 1;
}

function jsonInit(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  };
}

export const api = {
  // A server Migration's lock on the old host (ADR-0018).
  migrationStatus: () => request<MigrationStatus>('/api/migration/status'),
  resumeDocker: (confirm: string) => request<{ resumed: boolean }>('/api/docker/recover', jsonInit('POST', { confirm })),
  dockerReport: (refresh = false) => request<DockerReport>(`/api/docker${refresh ? '?refresh=1' : ''}`),
  dockerPrunePlan: (kind: DockerPruneKind) => request<DockerPrunePlan>(`/api/docker/prune-plan?kind=${encodeURIComponent(kind)}`),
  pruneDocker: (kind: DockerPruneKind, confirm: string, includePanel = false) =>
    request<DockerPruneResult>('/api/docker/prune', jsonInit('POST', includePanel ? { kind, confirm, includePanel } : { kind, confirm })),
  authStatus: () => request<AuthStatus>('/api/auth/status'),
  /**
   * Sign-in, with its own `fetch` rather than `request`: a `401` asking for the
   * two-factor code and a `429` announcing the lock are results, not failures.
   * A wrong password or code still throws an `ApiError`, as does any other
   * non-2xx response.
   */
  login: async (input: LoginInput): Promise<LoginResult> => {
    const response = await fetch('/api/auth/login', { credentials: 'same-origin', ...jsonInit('POST', input) });
    if (response.status === 200) {
      return { kind: 'signedIn' };
    }
    if (response.status === 401) {
      const payload = await loginErrorResponse(response);
      if (payload.codeRequired) {
        return { kind: 'codeRequired' };
      }
      throw new ApiError(401, payload.error ?? 'Request failed (401)');
    }
    if (response.status === 429) {
      return { kind: 'rateLimited', retryAfterSeconds: retryAfterSeconds(response.headers.get('Retry-After')) };
    }
    throw new ApiError(response.status, await responseError(response));
  },
  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),

  /** The Account page's read: the admin's email and two-factor state. */
  account: () => request<Account>('/api/auth/account', { method: 'GET' }),
  /**
   * Starts turning two-factor on: checks the password and issues a pending
   * secret, replacing any earlier one. 409 when two-factor is already on.
   */
  twoFactorSetup: (password: string) => request<TwoFactorSetup>('/api/auth/2fa/setup', jsonInit('POST', { password })),
  /** Confirms the pending secret with a code; success is 204. */
  twoFactorEnable: (code: string) => request<void>('/api/auth/2fa/enable', jsonInit('POST', { code })),
  /**
   * Replaces the password after checking the current one, and a code when
   * two-factor is on. Every other session ends; success is 204.
   */
  changePassword: (currentPassword: string, newPassword: string, code?: string) =>
    request<void>('/api/auth/password', jsonInit('POST', { currentPassword, newPassword, code })),
  /** Turns two-factor off, checking both the password and a current code; success is 204. */
  twoFactorDisable: (password: string, code: string) =>
    request<void>('/api/auth/2fa/disable', jsonInit('POST', { password, code })),

  listProjects: () => request<Project[]>('/api/projects'),
  createProject: (input: ProjectInput) => request<Project>('/api/projects', jsonInit('POST', input)),
  updateProject: (projectId: number, input: ProjectInput) =>
    request<Project>(`/api/projects/${projectId}`, jsonInit('PUT', input)),
  deleteProject: (projectId: number) => request<void>(`/api/projects/${projectId}`, { method: 'DELETE' }),

  listEnvironments: (projectId: number) => request<Environment[]>(`/api/projects/${projectId}/environments`),
  createEnvironment: (projectId: number, input: EnvironmentInput) =>
    request<Environment>(`/api/projects/${projectId}/environments`, jsonInit('POST', input)),
  deleteEnvironment: (projectId: number, environmentId: number) =>
    request<void>(`/api/projects/${projectId}/environments/${environmentId}`, { method: 'DELETE' }),

  listVariables: (projectId: number, environmentId: number) =>
    request<EnvironmentVariable[]>(variablesPath(projectId, environmentId)),
  createVariable: (projectId: number, environmentId: number, input: EnvironmentVariableInput) =>
    request<EnvironmentVariable>(variablesPath(projectId, environmentId), jsonInit('POST', input)),
  updateVariable: (projectId: number, environmentId: number, variableId: number, input: EnvironmentVariableInput) =>
    request<EnvironmentVariable>(variablePath(projectId, environmentId, variableId), jsonInit('PUT', input)),
  deleteVariable: (projectId: number, environmentId: number, variableId: number) =>
    request<void>(variablePath(projectId, environmentId, variableId), { method: 'DELETE' }),

  listComposes: (projectId: number, environmentId: number) =>
    request<Compose[]>(composesPath(projectId, environmentId)),
  createCompose: (projectId: number, environmentId: number, input: ComposeCreateInput) =>
    request<Compose>(composesPath(projectId, environmentId), jsonInit('POST', input)),
  getCompose: (location: ComposePath) => request<Compose>(composePath(location), { method: 'GET' }),
  updateCompose: (location: ComposePath, input: ComposeUpdateInput) =>
    request<Compose>(composePath(location), jsonInit('PUT', input)),
  deleteCompose: (location: ComposePath) => request<void>(composePath(location), { method: 'DELETE' }),
  /** The plan behind a Delete: one of the `removalPlanPath` URLs. Reads only; deletes nothing. */
  removalPlan: (path: string) => request<{ composes: RemovalPlanEntry[] }>(path),
  /**
   * Queues a Deployment: 202 for a new one, 200 when an identical request is already
   * queued, 409 when one with other options is (the message names it). No client
   * timeout: the POST waits behind a same-Project deploy step.
   */
  enqueueDeployment: (location: ComposePath, req: DeploymentRequest) =>
    request<{ number: number }>(`${composePath(location)}/deployments`, jsonInit('POST', req)),
  /** The Compose's Deployments, newest first. */
  listDeployments: (location: ComposePath) => request<Deployment[]>(`${composePath(location)}/deployments`),
  listGlobalDeployments: (query: DeploymentHistoryQuery = {}) => {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined) params.set(key, String(value));
    }
    const search = params.toString();
    return request<GlobalDeploymentPage>(`/api/deployments${search ? `?${search}` : ''}`);
  },
  globalDeploymentQueue: () => request<GlobalQueuedDeployment[]>('/api/deployments/queue'),
  getDeployment: (location: ComposePath, number: number) =>
    request<Deployment>(`${composePath(location)}/deployments/${number}`),
  deploymentLog: (location: ComposePath, number: number, offset: number) =>
    request<DeploymentLog>(`${composePath(location)}/deployments/${number}/log?offset=${offset}`),
  /** 202 when the cancel is accepted; 409 once the Deployment is no longer cancelable. */
  cancelDeployment: (location: ComposePath, number: number) =>
    request<void>(`${composePath(location)}/deployments/${number}/cancel`, { method: 'POST' }),
  /** The host-wide queue: the running Deployment and those waiting behind it. */
  deploymentQueue: () => request<QueuedDeployment[]>('/api/deployments/queue'),

  /** The Compose's Source, or `null` when it has none (the API answers 404). */
  getSource: async (location: ComposePath): Promise<ComposeSource | null> => {
    try {
      return await request<ComposeSource>(`${composePath(location)}/source`);
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.status === 404) {
        return null;
      }
      throw reason;
    }
  },
  saveSource: (location: ComposePath, input: SourceInput) =>
    request<ComposeSource>(`${composePath(location)}/source`, jsonInit('PUT', input)),
  deleteSource: (location: ComposePath) => request<void>(`${composePath(location)}/source`, { method: 'DELETE' }),
  /** The host-wide SSH keys library, ordered by name. */
  listSshKeys: () => request<SshKey[]>('/api/ssh-keys'),
  createSshKey: (name: string) => request<SshKey>('/api/ssh-keys', jsonInit('POST', { name })),
  renameSshKey: (id: number, name: string) => request<SshKey>(`/api/ssh-keys/${id}`, jsonInit('PATCH', { name })),
  deleteSshKey: (id: number) => request<void>(`/api/ssh-keys/${id}`, { method: 'DELETE' }),
  // Vulnerability scanning (ADR-0021).
  vulnSettings: () => request<VulnSettingsResponse>(`${VULN_URL}/settings`),
  saveVulnSettings: (settings: VulnSettings) => request<VulnSettingsResponse>(`${VULN_URL}/settings`, jsonInit('PUT', settings)),
  vulnStatus: () => request<VulnStatus>(`${VULN_URL}/status`),
  vulnScannerTags: () => request<{ tags: ScannerTag[] }>(`${VULN_URL}/scanner-tags`),
  vulnScan: (imageId?: string) => request<{ queued: number }>(`${VULN_URL}/scans`, jsonInit('POST', imageId ? { imageId } : {})),
  vulnDeleteCache: () => request<void>(`${VULN_URL}/cache`, { method: 'DELETE' }),
  vulnImages: () => request<VulnImage[]>(`${VULN_URL}/images`),
  vulnFindings: (imageId: string) => request<VulnFinding[]>(vulnFindingsUrl(imageId)),
  vulnCves: (query: VulnCveQuery) => request<VulnCvePage>(vulnCvesUrl(query)),
  vulnSummary: (scope: VulnScope) => request<VulnSummary>(vulnSummaryUrl(scope)),
  /** Tries the saved repository with the Source's SSH key and lists its branches. */
  testSource: (location: ComposePath) =>
    request<SourceTestResult>(`${composePath(location)}/source/test`, { method: 'POST' }),
  resetKnownHosts: (location: ComposePath) =>
    request<void>(`${composePath(location)}/source/known-hosts/reset`, { method: 'POST' }),
  uploadSource: (location: ComposePath, file: File) => {
    const body = new FormData();
    body.append('file', file);
    return request<ComposeSource>(`${composePath(location)}/source/upload`, { method: 'POST', body });
  },
  /** Dockerfiles found in the Source; the API sends no file name, so it is the path's last segment. */
  detectDockerfiles: async (location: ComposePath): Promise<DetectedDockerfile[]> => {
    const found = await request<Omit<DetectedDockerfile, 'dockerfile'>[]>(`${composePath(location)}/source/dockerfiles`);
    return found.map((item) => ({ ...item, dockerfile: item.path.split('/').pop() ?? item.path }));
  },
  /** Resolves a draft exactly as a deploy would; saves nothing and never calls Docker. */
  previewCompose: (location: ComposePath, yamlContent: string) =>
    request<ComposePreview>(`${composePath(location)}/preview`, jsonInit('POST', { yamlContent })),
  stopCompose: (location: ComposePath) => request<Compose>(`${composePath(location)}/stop`, { method: 'POST' }),
  restartCompose: (location: ComposePath) => request<Compose>(`${composePath(location)}/restart`, { method: 'POST' }),
  logs: (location: ComposePath) => request<LogsPayload>(`${composePath(location)}/logs`),

  /** The Compose's domains, and the saved YAML's Services a domain can point at. */
  listDomains: (location: ComposePath) => request<ComposeDomains>(domainsPath(location)),
  createDomain: (location: ComposePath, input: DomainInput) =>
    request<Domain>(domainsPath(location), jsonInit('POST', input)),
  updateDomain: (location: ComposePath, domainId: number, input: DomainInput) =>
    request<Domain>(`${domainsPath(location)}/${domainId}`, jsonInit('PUT', input)),
  /** The Compose's own Variable Sets, with entries and the Services that bind each. */
  listVariableSets: (location: ComposePath) => request<VariableSet[]>(setsPath(location)),
  createVariableSet: (location: ComposePath, name: string) =>
    request<VariableSet>(setsPath(location), jsonInit('POST', { name })),
  renameVariableSet: (location: ComposePath, setId: number, name: string) =>
    request<VariableSet>(`${setsPath(location)}/${setId}`, jsonInit('PUT', { name })),
  /** Success is 204; the set's bindings go with it. */
  deleteVariableSet: (location: ComposePath, setId: number) =>
    request<void>(`${setsPath(location)}/${setId}`, { method: 'DELETE' }),
  createSetEntry: (location: ComposePath, setId: number, input: EnvironmentVariableInput) =>
    request<SetEntry>(`${setsPath(location)}/${setId}/entries`, jsonInit('POST', input)),
  updateSetEntry: (location: ComposePath, setId: number, entryId: number, input: EnvironmentVariableInput) =>
    request<SetEntry>(`${setsPath(location)}/${setId}/entries/${entryId}`, jsonInit('PUT', input)),
  deleteSetEntry: (location: ComposePath, setId: number, entryId: number) =>
    request<void>(`${setsPath(location)}/${setId}/entries/${entryId}`, { method: 'DELETE' }),
  listComposeVariables: (location: ComposePath) => request<ComposeVariable[]>(composeVariablesPath(location)),
  createComposeVariable: (location: ComposePath, input: EnvironmentVariableInput) =>
    request<ComposeVariable>(composeVariablesPath(location), jsonInit('POST', input)),
  updateComposeVariable: (location: ComposePath, variableId: number, input: EnvironmentVariableInput) =>
    request<ComposeVariable>(`${composeVariablesPath(location)}/${variableId}`, jsonInit('PUT', input)),
  /** Success is 204. */
  deleteComposeVariable: (location: ComposePath, variableId: number) =>
    request<void>(`${composeVariablesPath(location)}/${variableId}`, { method: 'DELETE' }),
  listBindings: (location: ComposePath) => request<ServiceBindings[]>(`${composePath(location)}/bindings`),
  /** Replaces one Service's bindings; the order of `setIds` is the binding order, `[]` removes them. */
  saveBindings: (location: ComposePath, service: string, setIds: number[]) =>
    request<ServiceBindings>(
      `${composePath(location)}/bindings/${encodeURIComponent(service)}`,
      jsonInit('PUT', { setIds }),
    ),
  effectiveVariables: (location: ComposePath, service: string) =>
    request<EffectiveVariable[]>(`${composePath(location)}/services/${encodeURIComponent(service)}/effective-variables`),
  /** Success is 204. */
  deleteDomain: (location: ComposePath, domainId: number) =>
    request<void>(`${domainsPath(location)}/${domainId}`, { method: 'DELETE' }),

  /**
   * The Terminal tab's Service/Container listing, with a bash/sh probe per
   * running Container. `available: false` carries the reason the feature
   * cannot run (no Engine API); a Service with no running Container still
   * appears, with an empty `containers` list.
   */
  terminalServices: (location: ComposePath) =>
    request<TerminalServices>(`${composePath(location)}/terminal/services`),

  hostMetrics: (window: MetricWindow) => request<HostMetrics>(`/api/host/metrics?window=${window}`),
  /** The Compose's Services, from the container collector's cache. */
  listServices: (location: ComposePath) => request<ServiceFacts[]>(`${composePath(location)}/services`),
  /**
   * The Compose's resolved Volumes, each one's pre-flight status, and every
   * Service's operation identity. `available: false` carries the reason the
   * feature cannot run; before the first deploy the API answers 502 with the
   * CLI's own `couldn't find env file`.
   */
  listVolumes: (location: ComposePath) => request<ComposeVolumes>(`${composePath(location)}/volumes`),
  listOrphanVolumes: (location: ComposePath) =>
    request<{ orphans: OrphanVolume[] }>(`${composePath(location)}/volumes/orphans`),
  deleteOrphanVolume: (location: ComposePath, name: string) =>
    request<void>(`${composePath(location)}/volumes/orphans/${encodeURIComponent(name)}`, { method: 'DELETE' }),
  /**
   * One directory's first entries (a capped, name-sorted listing that says
   * when it was cut short), from the helper's `list`. `source` must be one
   * of the Compose's resolved sources and `path` is relative to the Volume
   * root (`''` is the root); a refusal — a symlink that escapes, a directory
   * the operation uid cannot read — arrives as the API's own message.
   */
  listVolumeEntries: (location: ComposePath, source: string, path: string, access?: VolumeAccess, after?: string) =>
    request<VolumeListing>(
      `${composePath(location)}/volumes/list?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}${volumeAccessQuery(access)}${after ? `&after=${encodeURIComponent(after)}` : ''}`,
    ),
  /**
   * One file's bytes, for the editor: `read` without the `download` marker,
   * held whole because the caller's 1 MB editor gate has already refused
   * anything larger. A failure carries the API's own message like every read.
   */
  readVolumeFile: async (location: ComposePath, source: string, path: string, access?: VolumeAccess): Promise<Uint8Array> => {
    const response = await fetch(volumeReadApiUrl(location, source, path, access), { credentials: 'same-origin' });
    if (!response.ok) {
      throw new ApiError(response.status, await responseError(response));
    }
    return new Uint8Array(await response.arrayBuffer());
  },
  /**
   * Uploads (or overwrites) one file: the `File` is the request body, so the
   * browser declares its length for it — which the API requires, because the
   * length becomes the helper's `--expect`. `path` must name the file, never
   * the Volume root (400); a size above the 1 GiB cap is refused with 413, and
   * a write the helper refuses for want of space with 507 carrying its own
   * need-vs-available message. Success is 204.
   */
  writeVolumeFile: (location: ComposePath, source: string, path: string, file: Blob, access?: VolumeAccess) =>
    request<void>(volumeContentUrl(location, source, path, access), { body: file, method: 'PUT' }),
  /**
   * Deletes one entry, or a whole directory tree: permanent, no trash, and
   * the API's own refusal when the path is the Volume root. Success is 204.
   */
  deleteVolumeEntry: (location: ComposePath, source: string, path: string, access?: VolumeAccess) =>
    request<void>(volumeContentUrl(location, source, path, access), { method: 'DELETE' }),
  /**
   * Creates one directory (the Explorer's New folder) as the confirmed
   * operation user. One helper run, no body; an existing name is refused with
   * 409 rather than merged. Success is 204.
   */
  createVolumeDirectory: (location: ComposePath, source: string, path: string, access?: VolumeAccess) =>
    request<void>(volumeDirectoryUrl(location, source, path, access), { method: 'PUT' }),
  /**
   * Creates one empty file (the Explorer's New file). Deliberately not the
   * write endpoint: `create` fails with 409 on a name that already exists
   * instead of truncating it, so New file can never wipe the file it
   * collided with. Success is 204.
   */
  createVolumeFile: (location: ComposePath, source: string, path: string, access?: VolumeAccess) =>
    request<void>(volumeFileUrl(location, source, path, access), { method: 'PUT' }),
  /**
   * Restores a tar.gz into one resolved Volume directory: the archive is the
   * request body, streamed straight into the helper's `extract`, so upload and
   * extract are one operation and nothing is staged on the panel's disk. The
   * browser's `File` declares its length, which the API requires (a body
   * without one is a 411). `path` names the target directory, which must
   * already exist (the Volume root, `.`, is legal here unlike the write
   * family). A compressed archive above 1 GiB is refused with 413, an unsafe,
   * cap-breaking or unusable archive with 422 carrying the helper's own
   * message, out of space with 507, and a missing source with 409. Success is
   * 204.
   */
  restoreVolume: (location: ComposePath, source: string, path: string, file: Blob, access?: VolumeAccess) =>
    request<void>(volumeRestoreUrl(location, source, path, access), { body: file, method: 'PUT' }),
  serviceMetrics: (location: ComposePath, service: string, window: MetricWindow) =>
    request<ServiceMetrics>(`${composePath(location)}/services/${encodeURIComponent(service)}/metrics?window=${window}`),
};

export type ComposePath = {
  projectId: number;
  environmentId: number;
  composeId: number;
};

function variablesPath(projectId: number, environmentId: number): string {
  return `/api/projects/${projectId}/environments/${environmentId}/variables`;
}

function variablePath(projectId: number, environmentId: number, variableId: number): string {
  return `${variablesPath(projectId, environmentId)}/${variableId}`;
}

function composesPath(projectId: number, environmentId: number): string {
  return `/api/projects/${projectId}/environments/${environmentId}/composes`;
}

function composePath(location: ComposePath): string {
  return `${composesPath(location.projectId, location.environmentId)}/${location.composeId}`;
}

/** The URLs of the removal plan a Delete of a Project, an Environment or a Compose shows first. */
export const removalPlanPath = {
  compose: (location: ComposePath) => `${composePath(location)}/removal-plan`,
  environment: (projectId: number, environmentId: number) =>
    `/api/projects/${projectId}/environments/${environmentId}/removal-plan`,
  project: (projectId: number) => `/api/projects/${projectId}/removal-plan`,
};

function setsPath(location: ComposePath): string {
  return `${composePath(location)}/variable-sets`;
}

function composeVariablesPath(location: ComposePath): string {
  return `${composePath(location)}/variables`;
}

function domainsPath(location: ComposePath): string {
  return `${composePath(location)}/domains`;
}

/**
 * The direct download URL for one directory's tar.gz archive: `path` defaults
 * to the Volume root (`.`, the whole-tree backup the Volumes tab offers) and
 * the Explorer passes the directory it is showing. Both are handed to the
 * browser as an `<a href>` — the archive streams from the helper straight to
 * disk, potentially for gigabytes, so it must never pass through a `fetch`
 * and a blob in the tab's memory.
 */
export function volumeArchiveUrl(location: ComposePath, source: string, path = '.', access?: VolumeAccess): string {
  return `${composePath(location)}/volumes/archive?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}${volumeAccessQuery(access)}`;
}

/**
 * The read URL with no download marker: the bytes come back inline, which is
 * what the editor's `fetch` wants. `volumeReadUrl` adds `download=1` for the
 * toolbar's `<a href>`.
 */
export function volumeReadApiUrl(location: ComposePath, source: string, path: string, access?: VolumeAccess): string {
  return `${composePath(location)}/volumes/read?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}${volumeAccessQuery(access)}`;
}

/**
 * The direct download URL for one file — `read` with `download=1`, so the
 * response is an attachment and the anchor never navigates the tab. Same rule
 * as the archive: an `<a href>`, never a fetch/blob.
 */
export function volumeReadUrl(location: ComposePath, source: string, path: string, access?: VolumeAccess): string {
  return `${volumeReadApiUrl(location, source, path, access)}&download=1`;
}

/**
 * The write endpoint's URL for one entry — both PUT (upload or edit) and
 * DELETE share it, with the same `source` + `path` grammar as the read URLs.
 * The body of a PUT is the file's bytes, so the URL never carries content.
 */
export function volumeContentUrl(location: ComposePath, source: string, path: string, access?: VolumeAccess): string {
  return `${composePath(location)}/volumes/content?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}${volumeAccessQuery(access)}`;
}

/**
 * The create endpoints' URLs: one entry per request, never the Volume root.
 * `directory` answers PUT with the helper's `mkdir` and `file` with
 * `create` — an empty file that refuses an existing name, unlike the
 * truncating `write` behind `volumeContentUrl`. Neither carries a body, so
 * nothing about the name rides anywhere but the query.
 */
export function volumeDirectoryUrl(location: ComposePath, source: string, path: string, access?: VolumeAccess): string {
  return volumeCreateUrl(location, 'directory', source, path, access);
}

export function volumeFileUrl(location: ComposePath, source: string, path: string, access?: VolumeAccess): string {
  return volumeCreateUrl(location, 'file', source, path, access);
}

function volumeCreateUrl(location: ComposePath, endpoint: string, source: string, path: string, access?: VolumeAccess): string {
  return `${composePath(location)}/volumes/${endpoint}?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path)}${volumeAccessQuery(access)}`;
}

/**
 * The restore endpoint's URL for one target directory — the current directory
 * in the Explorer, and `.` at the Volume root, where `extract` merges rather
 * than refusing the way the write family does. The body is the archive, so the
 * URL never carries content.
 */
export function volumeRestoreUrl(location: ComposePath, source: string, path = '.', access?: VolumeAccess): string {
  return `${composePath(location)}/volumes/restore?source=${encodeURIComponent(source)}&path=${encodeURIComponent(path || '.')}${volumeAccessQuery(access)}`;
}

export function volumeAccessQuery(access?: VolumeAccess): string {
  const query = new URLSearchParams();
  if (access?.user) {
    query.set('user', access.user);
  } else if (access?.service) {
    query.set('service', access.service);
  }
  if (access?.rootConfirmed) {
    query.set('rootConfirmed', '1');
  }
  if (access?.bindConfirmed) {
    query.set('bindConfirmed', '1');
  }
  const encoded = query.toString();
  return encoded ? `&${encoded}` : '';
}

export const VULN_URL = '/api/vulnerabilities';

export interface VulnScope {
  projectId?: number;
  composeId?: number;
}

export function vulnFindingsUrl(imageId: string): string {
  return `${VULN_URL}/images/${encodeURIComponent(imageId)}/findings`;
}

export function vulnCvesUrl(query: VulnCveQuery): string {
  const params = new URLSearchParams();
  if (query.projectId !== undefined) params.set('projectId', String(query.projectId));
  if (query.composeId !== undefined) params.set('composeId', String(query.composeId));
  if (query.severity.length > 0) params.set('severity', query.severity.join(','));
  if (query.fixable) params.set('fixable', '1');
  if (query.new) params.set('new', '1');
  if (query.q) params.set('q', query.q);
  params.set('page', String(query.page));
  params.set('pageSize', String(query.pageSize));
  return `${VULN_URL}/cves?${params}`;
}

export function vulnSummaryUrl(scope: VulnScope): string {
  if (scope.composeId !== undefined) return `${VULN_URL}/summary?composeId=${scope.composeId}`;
  if (scope.projectId !== undefined) return `${VULN_URL}/summary?projectId=${scope.projectId}`;
  return `${VULN_URL}/summary`;
}

export const IMAGES_URL = '/api/images';

/** The host's images, unwrapped from `{ images }`. */
export async function listImages(): Promise<HostImage[]> {
  return (await request<{ images: HostImage[] }>(IMAGES_URL)).images;
}

/**
 * Loads an image archive onto the host: the file is the PUT body, sent over
 * `XMLHttpRequest` because `fetch` cannot report upload progress. Resolves with
 * the names the engine loaded; failures reject as `ApiError` (status 0 for a
 * dropped connection or a cancel).
 */
export function uploadImage(
  file: Blob,
  onProgress: (sent: number, total: number) => void,
  signal?: AbortSignal,
): Promise<string[]> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new ApiError(0, 'Upload cancelled'));
      return;
    }
    const xhr = new XMLHttpRequest();
    const onAbort = () => xhr.abort();
    const done = () => signal?.removeEventListener('abort', onAbort);
    xhr.open('PUT', IMAGES_URL);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        onProgress(event.loaded, event.total);
      }
    };
    xhr.onload = () => {
      done();
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve((JSON.parse(xhr.responseText) as { images: string[] }).images);
        } catch {
          reject(new ApiError(xhr.status, `Request failed (${xhr.status})`));
        }
        return;
      }
      reject(new ApiError(xhr.status, errorMessage(xhr.status, xhr.responseText)));
    };
    xhr.onerror = () => {
      done();
      reject(new ApiError(0, 'Upload failed — connection lost'));
    };
    xhr.onabort = () => {
      done();
      reject(new ApiError(0, 'Upload cancelled'));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
    xhr.send(file);
  });
}
