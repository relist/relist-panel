import { isNotDeployed } from '../components/volumes-view';
import type { Compose, ComposeLocation, ComposeVolumes } from '../data/types';

/** Use the backend's actual deploy paths; only older API payloads need the legacy fallback. */
export function composeDeployIdentity(
  compose: Pick<Compose, 'projectSlug' | 'envSlug' | 'name' | 'deployIdentity'>,
): { projectName: string; directory: string } {
  if (compose.deployIdentity) {
    return compose.deployIdentity;
  }
  const name = compose.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'x';
  return {
    projectName: `${compose.projectSlug}-${compose.envSlug}-${name}`,
    directory: `dataDir/${compose.projectSlug}/${compose.envSlug}/${name}`,
  };
}

/**
 * View decisions for the Compose screen, kept pure so the design's rules — the
 * tab a URL segment resolves to, when the file counts as unsaved, what a Deploy
 * does first, how the logs drawer narrows its lines, and when a link is stale —
 * are testable without a router or a store.
 */

export type ComposeTab = 'editor' | 'monitor' | 'volumes';

/** The tab a path segment resolves to; an unknown segment falls back to Editor (GUIDELINES §4.6). */
export function activeComposeTab(tab?: string): ComposeTab {
  if (tab === 'monitor' || tab === 'volumes') {
    return tab;
  }
  return 'editor';
}

/** The unsaved-changes rule behind the header tag, the `Modified` marker and the banner. */
export function hasUnsavedChanges(draft: string, savedYaml: string): boolean {
  return draft !== savedYaml;
}

/**
 * A Deploy's steps, in order: a dirty file is saved before the deploy reaches
 * the host (GUIDELINES §8 — deploy saves first and says so), so the host never
 * builds from older YAML than the one on screen.
 */
export function deploySteps(dirty: boolean): readonly ('deploy' | 'save')[] {
  return dirty ? ['save', 'deploy'] : ['deploy'];
}

/**
 * The logs drawer's view of the whole-stack `docker compose logs` text: keep
 * the lines that begin with one of the selected Services' names, then keep only
 * the last `limit` of those. No selection filters nothing.
 */
export function filterLogLines(
  logsText: string | undefined,
  services: readonly string[],
  limit: number,
): string[] {
  const lines = logsText ? logsText.split('\n') : [];
  const byService =
    services.length === 0
      ? lines
      : lines.filter((line) => services.some((service) => line.trimStart().startsWith(service)));
  return byService.slice(-limit);
}

/**
 * A Compose URL carries the ids of the parents the Compose actually lives in;
 * anything else (a hand-edited path, a link left stale) redirects to the
 * canonical one, tab included. An unknown Compose is not stale — it renders the
 * not-found page instead of a redirect into nothing.
 */
export function composeLinkIsStale(
  location: ComposeLocation | undefined,
  projectId: number,
  environmentId: number,
): boolean {
  if (!location) {
    return false;
  }
  return projectId !== location.project.id || environmentId !== location.environment.id;
}

/**
 * The fresh-volumes dialog's rules from the page's Volumes resolution: which
 * named volumes to list, and why the action is refused when it is. The list is
 * the deployed file resolved without profiles, so it can miss volumes only a
 * profile-gated Service mounts and includes `external: true` ones — the dialog
 * copy says both; this function never claims the list is complete.
 */
export function freshDeployView(
  payload: ComposeVolumes | undefined,
  error: string | undefined,
): { volumes: string[]; refusal: string | undefined } {
  if (error !== undefined) {
    return {
      volumes: [],
      refusal: isNotDeployed(error)
        ? 'This Compose has not been deployed yet, so it has no volumes to replace.'
        : `This Compose’s volumes could not be resolved: ${error}`,
    };
  }
  if (payload === undefined) {
    return { volumes: [], refusal: 'Resolving this Compose’s volumes…' };
  }
  if (!payload.available) {
    return { volumes: [], refusal: 'Volume management is unavailable on this host.' };
  }
  return { volumes: payload.volumes.filter((v) => v.kind === 'volume').map((v) => v.source), refusal: undefined };
}
