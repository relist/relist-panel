import { formatBytes } from '../components/MetricChart';
import { cliMessage, isNotDeployed } from '../components/volumes-view';
import type { ComposeVolume, ComposeVolumes, VolumeEntry } from '../data/types';

/**
 * The Volume Explorer's pure decisions: how the URL's source resolves against
 * the Compose's Volumes payload, which pre-flight outcomes gate a read, the
 * breadcrumb path math, and how one listing entry is described. Kept pure so
 * the rules are testable without a router, a store or a DOM.
 *
 * The screen's three rules (GUIDELINES §4.7) live here:
 *
 *  - **A symlink is shown, described, and followed only when it stays inside
 *    the Volume.** `linkEscapes` discloses the ones read as stored; the API's
 *    own refusal — `path escapes from parent` — is what the alert quotes.
 *  - **Nothing is silently resolved.** Errors are shown in the API's words.
 *  - **The pre-flight gate is one acknowledgement per visit**, only for a
 *    source the panel cannot check: an absolute bind outside the data
 *    directory, which a mount would create as `root:root 0755`.
 */

export type ExplorerView =
  /** The resolution (or the tree it needs) has not settled yet. */
  | { status: 'loading' }
  /** The feature cannot run at all (`available: false`): a bare binary or no Docker socket. */
  | { status: 'unavailable'; reason: string }
  /** The Compose CLI could not resolve: before the first deploy it is `couldn't find env file`. */
  | { status: 'not-deployed'; message: string }
  /** Any other resolution failure: stays on the screen as an Alert with Retry. */
  | { status: 'resolution-error'; message: string }
  /** The URL names a source this Compose does not mount (any more). */
  | { status: 'not-found' }
  /** The source is marked missing: mounting it would create it, so reads are refused. */
  | { status: 'refused'; message: string; volume: ComposeVolume }
  /** The Volume resolved and may be read (subject to the unverifiable gate). */
  | { status: 'ready'; volume: ComposeVolume };

/**
 * What the Explorer shows for the URL's `source`, from the page's one
 * resolution fetch: the payload, the error, or neither while it is in flight.
 */
export function explorerView(
  payload: ComposeVolumes | undefined,
  error: string | undefined,
  source: string,
): ExplorerView {
  if (error !== undefined) {
    return isNotDeployed(error)
      ? { status: 'not-deployed', message: cliMessage(error) }
      : { status: 'resolution-error', message: error };
  }
  if (payload === undefined) {
    return { status: 'loading' };
  }
  if (!payload.available) {
    return { status: 'unavailable', reason: payload.reason ?? '' };
  }
  const volume = payload.volumes.find((candidate) => candidate.source === source);
  if (volume === undefined) {
    return { status: 'not-found' };
  }
  const refusal = operationRefusal(volume);
  return refusal === undefined ? { status: 'ready', volume } : { status: 'refused', message: refusal, volume };
}

/** The API's own 409 wording for a source the pre-flight marks missing. */
export function operationRefusal(volume: ComposeVolume): string | undefined {
  if (volume.preflight.status !== 'missing') {
    return undefined;
  }
  return volume.preflight.warning
    ? `volume ${volume.source} is missing: ${volume.preflight.warning}`
    : `volume ${volume.source} is missing`;
}

/**
 * Whether the first read needs the operator's acknowledgement: the panel
 * cannot `stat` an absolute bind outside its data directory, and probing it
 * with a mount is what would create it. `missing` is not a gate — it is a
 * refusal — and `ok` needs nothing.
 */
export function requiresConfirmation(volume: ComposeVolume): boolean {
  return volume.preflight.status === 'unverifiable';
}

/** A relative path's segments, in order; the Volume root (`''`) is the empty list. */
export function pathSegments(path: string): string[] {
  return path.split('/').filter(Boolean);
}

/** One level up; the root stays the root. */
export function parentPath(path: string): string {
  return pathSegments(path).slice(0, -1).join('/');
}

/** A child path: `'' / name` is just `name`, and trailing slashes never double. */
export function joinPath(parent: string, name: string): string {
  return [parent.replace(/\/+$/, ''), name].filter(Boolean).join('/');
}

/** One entry's display facts — what a row renders, derived once per entry. */
export type EntryFacts = {
  name: string;
  kind: VolumeEntry['kind'];
  /** Human size; a directory shows an em dash, because its own size is not its contents'. */
  size: string;
  /** The permission bits, octal, the way a filesystem lists them. */
  mode: string;
  owner: string;
  /** The wire timestamp, handed unchanged to `RelativeTime`. */
  modified: string;
  /** A symlink's target as stored; absent for everything else. */
  target?: string;
  /** A symlink whose target, read as written, must leave the Volume root. */
  escapes: boolean;
};

export function entryFacts(entry: VolumeEntry, cwd: string): EntryFacts {
  return {
    name: entry.name,
    kind: entry.kind,
    size: entry.kind === 'directory' ? '—' : formatBytes(entry.size),
    mode: formatMode(entry.mode),
    owner: `${entry.uid}:${entry.gid}`,
    modified: entry.mtime,
    target: entry.kind === 'symlink' ? (entry.link ?? '') : undefined,
    escapes: linkEscapes(entry, cwd),
  };
}

/**
 * The permission bits in octal. The wire's `mode` is the raw `fs.FileMode`,
 * whose high bits carry the entry's type; only the low nine say anything the
 * Explorer shows, and the `kind` column already names the type.
 */
export function formatMode(mode: number): string {
  return `0${(mode & 0o777).toString(8).padStart(3, '0')}`;
}

export function isSymlink(entry: VolumeEntry): boolean {
  return entry.kind === 'symlink';
}

/**
 * Whether a symlink's target, read exactly as stored, must leave the Volume
 * root: an absolute target (the root rejects those outright) or a relative
 * one whose normalization climbs above the root. A link this predicts inside
 * can still be refused after following another link — this is the row's
 * disclosure, not a gate; the API's own refusal is what a click surfaces.
 */
export function linkEscapes(entry: VolumeEntry, cwd: string): boolean {
  if (!isSymlink(entry) || !entry.link) {
    return false;
  }
  if (entry.link.startsWith('/')) {
    return true;
  }
  const normalized = normalize(joinPath(cwd, entry.link));
  return normalized === '..' || normalized.startsWith('../');
}

/** Whether an API refusal is the Volume root refusing to resolve a path outside itself. */
export function isEscapeRefusal(message: string): boolean {
  return message.includes('path escapes from parent');
}

/**
 * Whether an API refusal is the kernel's "that is not a directory" — a
 * symlink that the root resolved to a file rather than a directory. The
 * Explorer reads it as "the link is an entry to download, not one to enter".
 */
export function isNotDirectoryRefusal(message: string): boolean {
  return message.includes('not a directory');
}

/**
 * The alert copy for an entry that could not be opened. An escaping symlink
 * gets its target disclosed alongside the API's own words; anything else is
 * shown as the API worded it.
 */
export function openFailure(entry: VolumeEntry, error: string): { message: string; description: string } {
  if (isSymlink(entry) && isEscapeRefusal(error)) {
    return {
      message: `${entry.name} points outside this Volume`,
      description: `${entry.name} → ${entry.link ?? ''} was refused rather than silently resolved: ${error}`,
    };
  }
  return { message: `Could not open ${entry.name}`, description: error };
}

/** A relative path normalized without touching the filesystem (`..` at the root is kept). */
function normalize(path: string): string {
  const out: string[] = [];
  for (const segment of path.split('/')) {
    if (segment === '' || segment === '.') {
      continue;
    }
    if (segment === '..') {
      if (out.length > 0 && out[out.length - 1] !== '..') {
        out.pop();
      } else {
        out.push('..');
      }
    } else {
      out.push(segment);
    }
  }
  return out.join('/');
}
