import type { ComposeSource, Deployment } from './types';

export type SourceCardState = {
  kind: 'none' | 'git' | 'zip';
  title: string;
  builtLine: string;
  newCommits: boolean;
  notBuilt: boolean;
};

/** What the Source card shows, given the Source and the Compose's last successful build (if any). */
export function sourceCardState(src: ComposeSource | null, lastBuilt: Deployment | undefined): SourceCardState {
  if (!src) {
    return { kind: 'none', title: 'No Source', builtLine: '', newCommits: false, notBuilt: false };
  }
  const built = lastBuilt?.status === 'succeeded' ? lastBuilt : undefined;
  const builtLine = built
    ? `Built ${built.sourceRef.slice(0, 7)}${built.sourceSubject ? ` — ${built.sourceSubject}` : ''}`.trim()
    : 'Not built yet';

  if (src.kind === 'zip') {
    const uploadedAt = src.zip ? Date.parse(src.zip.uploadedAt) : NaN;
    const finishedAt = built?.finishedAt ? Date.parse(built.finishedAt) : NaN;
    const notBuilt = !built || !built.finishedAt || (Number.isFinite(uploadedAt) && uploadedAt > finishedAt);
    return { kind: 'zip', title: src.zip?.name ?? 'Uploaded zip', builtLine, newCommits: false, notBuilt };
  }

  const newCommits = src.headSha !== '' && built !== undefined && src.headSha !== built.sourceRef;
  return {
    kind: 'git',
    title: `${src.repoUrl} · ${src.branch}`,
    builtLine,
    newCommits,
    notBuilt: !built,
  };
}
