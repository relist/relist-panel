import { isMap, parseDocument } from 'yaml';

import type { ComposeSource, Deployment } from './types';

export type SourceCardState = {
  kind: 'none' | 'git' | 'zip';
  title: string;
  builtLine: string;
  newCommits: boolean;
  notBuilt: boolean;
  /** A git Source whose SSH key is unset (never chosen, or its key was deleted): it cannot fetch. */
  noKey: boolean;
};

/** What the Source card shows, given the Source and the Compose's last successful build (if any). */
export function sourceCardState(src: ComposeSource | null, lastBuilt: Deployment | undefined): SourceCardState {
  if (!src) {
    return { kind: 'none', title: 'No Source', builtLine: '', newCommits: false, notBuilt: false, noKey: false };
  }
  const built = lastBuilt?.status === 'succeeded' ? lastBuilt : undefined;
  const builtLine = built
    ? `Built ${built.sourceRef.slice(0, 7)}${built.sourceSubject ? ` — ${built.sourceSubject}` : ''}`.trim()
    : 'Not built yet';

  if (src.kind === 'zip') {
    const uploadedAt = src.zip ? Date.parse(src.zip.uploadedAt) : NaN;
    // Compared to when the build started: an upload during its Deploy step is
    // not what that build used, and the upload is not blocked then.
    const startedAt = built ? Date.parse(built.startedAt ?? built.queuedAt) : NaN;
    const notBuilt = !built || (Number.isFinite(uploadedAt) && uploadedAt > startedAt);
    return { kind: 'zip', title: src.zip?.name ?? 'Uploaded zip', builtLine, newCommits: false, notBuilt, noKey: false };
  }

  const newCommits = src.headSha !== '' && built !== undefined && src.headSha !== built.sourceRef;
  return {
    kind: 'git',
    title: `${repoName(src.repoUrl)} · ${src.branch}`,
    builtLine,
    newCommits,
    notBuilt: !built,
    noKey: src.sshKey === null,
  };
}

/** Names of the Services in a Compose YAML, optionally only those with `build:`; empty when the YAML does not parse. */
export function yamlServices(yaml: string, onlyBuilt = false): string[] {
  const doc = parseDocument(yaml);
  const services = doc.get('services');
  if (doc.errors.length > 0 || !isMap(services)) {
    return [];
  }
  return services.items
    .map((item) => String(item.key))
    .filter((name) => !onlyBuilt || doc.hasIn(['services', name, 'build']));
}

export const buildServices = (yaml: string): string[] => yamlServices(yaml, true);

/** `owner/repo` from `git@host:owner/repo.git` or `ssh://git@host:port/owner/repo.git`; the URL itself when it has no two path segments. */
export function repoName(url: string): string {
  const path = url.startsWith('ssh://') ? url.replace(/^ssh:\/\/[^/]*\//, '') : url.replace(/^[^:]*:/, '');
  const parts = path.replace(/\.git$/, '').split('/').filter(Boolean);
  return parts.length >= 2 && path !== url ? parts.slice(-2).join('/') : url;
}

export type SourceLoadState = 'loading' | 'none' | 'present' | 'failed';

/**
 * Whether the page knows its Compose's Source. Deploy actions pick a mode from
 * it, so they wait for 'none' or 'present': 'loading' and 'failed' must never
 * read as "no Source".
 */
export function sourceLoadState(
  loaded: { composeId: number; value: ComposeSource | null } | undefined,
  failedComposeId: number | undefined,
  composeId: number,
): SourceLoadState {
  if (failedComposeId === composeId) {
    return 'failed';
  }
  if (!loaded || loaded.composeId !== composeId) {
    return 'loading';
  }
  return loaded.value === null ? 'none' : 'present';
}
