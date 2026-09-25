import { projectComposes } from '../data/types';
import type { Environment, Project } from '../data/types';

/**
 * View decisions for the Project screens, kept pure so the design's rules —
 * the tab a URL segment resolves to, which Environment the switcher shows, and
 * how the Projects list filters and sorts — are testable without a router.
 */

export type ProjectTab = 'composes' | 'settings' | 'variables';

/** The tab a path segment resolves to; an unknown segment falls back to Composes. */
export function activeTab(tab?: string): ProjectTab {
  return tab === 'variables' || tab === 'settings' ? tab : 'composes';
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
 * records only — files stay on disk and running containers are not stopped (the
 * ticket-10 decision). The path is read from the first Compose's frozen slugs
 * (ADR-0005: identity does not follow later renames), so it is absent when the
 * Environment holds no Composes.
 */
export type EnvironmentDeleteFacts = {
  composes: number;
  /** `dataDir/{projectSlug}/{envSlug}/` — the prefix the files stay under. */
  dataDir?: string;
};

export function environmentDeleteFacts(environment: Environment): EnvironmentDeleteFacts {
  const first = environment.composes[0];
  return {
    composes: environment.composes.length,
    dataDir: first ? `dataDir/${first.projectSlug}/${first.envSlug}/` : undefined,
  };
}

/** The same record-only facts for a Project delete, rolled up across its tree. */
export type ProjectDeleteFacts = {
  composes: number;
  /** `dataDir/{projectSlug}/` — absent when the Project holds no Composes. */
  dataDir?: string;
  environments: number;
};

export function projectDeleteFacts(project: Project): ProjectDeleteFacts {
  const composes = projectComposes(project);
  const first = composes[0];
  return {
    composes: composes.length,
    dataDir: first ? `dataDir/${first.projectSlug}/` : undefined,
    environments: project.environments.length,
  };
}
