/**
 * The panel's HTTP API client. One thin wrapper per endpoint, lifted from the
 * fetch call sites in the monolith (`web/src/main.tsx`) — same paths, same
 * methods, same JSON shapes. `request` turns non-2xx responses into rejected
 * promises carrying the API's `error` message, so store callers can render it
 * directly.
 */

import type {
  Compose,
  ComposeVolumes,
  Environment,
  EnvironmentVariable,
  HostMetrics,
  Project,
  ServiceFacts,
  ServiceMetrics,
} from './types';
import type { MetricWindow } from './types';

export type AuthStatus = {
  setupRequired: boolean;
  authenticated: boolean;
};

export type Credentials = {
  email: string;
  password: string;
};

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

async function responseError(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as ErrorResponse;
    if (payload.error) {
      return payload.error;
    }
  } catch {
    // Fall through to a status-based message when the response is not JSON.
  }
  return `Request failed (${response.status})`;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: 'same-origin', ...init });
  if (!response.ok) {
    throw new Error(await responseError(response));
  }
  // Deletes (and logout) answer 204 with no body to parse.
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

function jsonInit(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  };
}

export const api = {
  authStatus: () => request<AuthStatus>('/api/auth/status'),
  setup: (credentials: Credentials) => request<unknown>('/api/auth/setup', jsonInit('POST', credentials)),
  login: (credentials: Credentials) => request<unknown>('/api/auth/login', jsonInit('POST', credentials)),
  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),

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
  deployCompose: (location: ComposePath) => request<Compose>(`${composePath(location)}/deploy`, { method: 'POST' }),
  stopCompose: (location: ComposePath) => request<Compose>(`${composePath(location)}/stop`, { method: 'POST' }),
  restartCompose: (location: ComposePath) => request<Compose>(`${composePath(location)}/restart`, { method: 'POST' }),
  logs: (location: ComposePath) => request<LogsPayload>(`${composePath(location)}/logs`),

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

/**
 * The direct download URL for one Volume's whole-tree tar.gz backup
 * (`path: "."`). It is handed to the browser as an `<a href>` — the archive
 * streams from the helper straight to disk, potentially for gigabytes, so it
 * must never pass through a `fetch` and a blob in the tab's memory.
 */
export function volumeArchiveUrl(location: ComposePath, source: string): string {
  return `${composePath(location)}/volumes/archive?source=${encodeURIComponent(source)}&path=.`;
}
