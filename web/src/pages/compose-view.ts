import type { ComposeLocation } from '../data/types';

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
