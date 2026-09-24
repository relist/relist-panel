import type { Compose, ComposeService, Project } from '../data/types';
import { composeState } from '../data/types';

/**
 * View decisions for the Overview screen (GUIDELINES §4.1), kept pure so the
 * design's rules — the flat all-Composes loop, the stat definitions, what
 * raises a Compose onto the attention list, and the table's sort order — are
 * testable without a router or a store.
 */

/** One row of the flat all-Composes view: a Compose plus the trail to it. */
export type FlatCompose = {
  compose: Compose;
  environmentId: number;
  environmentName: string;
  projectId: number;
  projectName: string;
};

/**
 * Every Compose on the host, flattened from the Project tree — the same
 * client-side loop the mock runs; no flat endpoint exists or is needed.
 */
export function flattenComposes(projects: Project[]): FlatCompose[] {
  return projects.flatMap((project) =>
    project.environments.flatMap((environment) =>
      environment.composes.map((compose) => ({
        compose,
        environmentId: environment.id,
        environmentName: environment.name,
        projectId: project.id,
        projectName: project.name,
      })),
    ),
  );
}

export type OverviewStats = {
  composes: number;
  /** Containers up, of `containers` known to the host. */
  containers: number;
  environments: number;
  neverDeployed: number;
  projects: number;
  running: number;
};

/** The four stat cards' definitions, counted from the tree. */
export function overviewStats(projects: Project[]): OverviewStats {
  const stats = projects.reduce<OverviewStats>(
    (acc, project) => {
      acc.projects += 1;
      acc.environments += project.environments.length;
      for (const compose of project.environments.flatMap((environment) => environment.composes)) {
        acc.composes += 1;
        acc.containers += compose.status.total;
        acc.running += compose.status.running;
        if (compose.status.total === 0) {
          acc.neverDeployed += 1;
        }
      }
      return acc;
    },
    { composes: 0, containers: 0, environments: 0, neverDeployed: 0, projects: 0, running: 0 },
  );
  return stats;
}

/**
 * What raises a Compose onto the needs-attention list (GUIDELINES §4.1): partly
 * running, stopped after a successful deploy, or an unhealthy Service. A
 * never-deployed Compose is not attention — nothing has dropped out. The
 * prototype's filter additionally required `total > 0` for a stop; that would
 * exclude a deploy later torn down (total 0, deploy timestamp present), which
 * the ticket's own definition counts, so the state alone decides here.
 *
 * Per-Service health comes from the Services endpoint via the store's
 * `services` map — the API's Compose itself carries no services array (§11).
 * Where the rows have not been loaded, the counts decide on their own.
 */
export function needsAttention(compose: Compose, services: ComposeService[] = []): boolean {
  const state = composeState(compose.status, compose.lastDeployedAt);
  return state === 'partial' || state === 'stopped' || services.some((service) => service.health === 'unhealthy');
}

/** The attention rows, in tree order (one row per degraded Compose). */
export function attentionRows(
  flat: FlatCompose[],
  servicesByCompose?: Record<number, ComposeService[]>,
): FlatCompose[] {
  return flat.filter(({ compose }) => needsAttention(compose, servicesByCompose?.[compose.id] ?? []));
}

/**
 * The row's notes beside its status tag: which Services dropped out, and the
 * stopped-after-success story. The Services come from the same store map as
 * `needsAttention`; when they have not been loaded, the state's own note is
 * all there is.
 */
export function degradationNotes(compose: Compose, services: ComposeService[] = []): string[] {
  const notes: string[] = [];
  const down = services.filter((service) => service.state === 'exited');
  if (down.length > 0) {
    notes.push(`${down.map((service) => service.name).join(', ')} exited`);
  }
  const unhealthy = services.filter((service) => service.health === 'unhealthy');
  if (unhealthy.length > 0) {
    notes.push(`${unhealthy.map((service) => service.name).join(', ')} unhealthy`);
  }
  const state = composeState(compose.status, compose.lastDeployedAt);
  if (state === 'stopped') {
    notes.push('Stopped after a successful deploy');
  }
  return notes;
}

export type ComposeSortKey = 'compose' | 'containers' | 'deployed' | 'updated' | 'where';

export type FlatComparator = (a: FlatCompose, b: FlatCompose) => number;

const byName = (a: FlatCompose, b: FlatCompose) => a.compose.name.localeCompare(b.compose.name);

/**
 * The table's sort orders, as natural **ascending** comparators — antd applies
 * or reverses them per the column's direction toggle, so none of these bake a
 * direction in (a baked-in "newest first" would display oldest-first under the
 * descend indicator). Recency columns declare `sortDirections:
 * ['descend', 'ascend']` in the table so the first click reads newest first;
 * the Last-edited column's `defaultSortOrder: 'descend'` opens the table that
 * way. Ties always fall back to the Compose name.
 */
export const composeComparators: Record<ComposeSortKey, FlatComparator> = {
  compose: byName,
  containers: (a, b) =>
    a.compose.status.running - b.compose.status.running ||
    a.compose.status.total - b.compose.status.total ||
    byName(a, b),
  // A Compose the panel cannot date sorts before dated ones, which puts it
  // last in the descend view the column opens in.
  deployed: (a, b) => {
    const aTime = a.compose.lastDeployedAt ? Date.parse(a.compose.lastDeployedAt) : 0;
    const bTime = b.compose.lastDeployedAt ? Date.parse(b.compose.lastDeployedAt) : 0;
    return aTime - bTime || byName(a, b);
  },
  updated: (a, b) => Date.parse(a.compose.updatedAt) - Date.parse(b.compose.updatedAt) || byName(a, b),
  where: (a, b) =>
    a.projectName.localeCompare(b.projectName) ||
    a.environmentName.localeCompare(b.environmentName) ||
    byName(a, b),
};
