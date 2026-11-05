import { projectComposes } from '../data/types';
import type { Environment, Project } from '../data/types';
import { composeDeployIdentity } from './compose-view';

/**
 * View decisions for the Project screens, kept pure so the design's rules —
 * the tab a URL segment resolves to, which Environment the switcher shows, and
 * how the Projects list filters and sorts — are testable without a router.
 */

export type ProjectTab = 'activity' | 'composes' | 'members' | 'settings' | 'variables';

/** The tab a path segment resolves to; an unknown segment falls back to Composes. */
export function activeTab(tab?: string): ProjectTab {
  return tab === 'variables' || tab === 'settings' || tab === 'members' || tab === 'activity' ? tab : 'composes';
}

/** The Environment the switcher shows: the URL's when it exists, else the first. */
export function selectEnvironment(project: Project, environmentId?: number): Environment | undefined {
  if (environmentId !== undefined) {
    const found = project.environments.find((candidate) => candidate.id === environmentId);
    if (found) {
      return found;
    }
  }
  return project.environments[0];
}

export type ProjectSortKey = 'name' | 'size' | 'updated';

/**
 * The Projects list: name/description filter, then the operator's sort —
 * recently updated (default), name, or most Environments.
 */
export function visibleProjects(projects: Project[], query: string, sort: ProjectSortKey): Project[] {
  const needle = query.trim().toLowerCase();
  const filtered = needle
    ? projects.filter(
        (project) =>
          project.name.toLowerCase().includes(needle) || project.description.toLowerCase().includes(needle),
      )
    : projects;
  return [...filtered].sort((a, b) => {
    if (sort === 'name') return a.name.localeCompare(b.name);
    if (sort === 'size') return b.environments.length - a.environments.length;
    return new Date(b.lastUpdated).getTime() - new Date(a.lastUpdated).getTime();
  });
}

// ---- Delete copy facts ---------------------------------------------------------

/**
 * What an Environment delete actually tears down, for the `DangerModal`'s copy:
 * child infrastructure and files first, then metadata. Each Compose may use
 * its own configured directory (including a mixture of legacy and ID identities).
 */
export type EnvironmentDeleteFacts = {
  composes: number;
  directories: string[];
};

export function environmentDeleteFacts(environment: Environment): EnvironmentDeleteFacts {
  return {
    composes: environment.composes.length,
    directories: environment.composes.map((compose) => composeDeployIdentity(compose).directory),
  };
}

/** The same teardown facts for a Project delete, rolled up across its tree. */
export type ProjectDeleteFacts = {
  composes: number;
  directories: string[];
  environments: number;
};

export function projectDeleteFacts(project: Project): ProjectDeleteFacts {
  const composes = projectComposes(project);
  return {
    composes: composes.length,
    directories: composes.map((compose) => composeDeployIdentity(compose).directory),
    environments: project.environments.length,
  };
}

/**
 * The New Compose example. It carries no Traefik labels: a domain comes from
 * the Compose's Domains tab, and hand-written routing labels need an approver
 * (ADR-0032), so an example with them would wait for approval at once.
 */
export const STARTER_YAML = `services:
  app:
    image: nginx:alpine
    restart: unless-stopped
`;
