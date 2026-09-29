import { formatBytes } from '../components/MetricChart';
import { cliMessage, isNotDeployed } from '../components/volumes-view';
import { ApiError } from '../data/api';
import type { ComposeStatus, ComposeVolume, ComposeVolumes, VolumeEntry } from '../data/types';

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

// ---- Writes (ticket 08) -------------------------------------------------------
//
// The write actions' pure decisions: the 1 GiB upload cap as a client-side fast
// path, the alert copy for a refused upload or delete — the API's own words
// with the limit that was hit named — and the running-stack warning's condition
// and Service list.

/** The API's hard-coded `uploadCap`, repeated here only for the fast path; the server stays the enforcer. */
export const UPLOAD_MAX_BYTES = 1 << 30;

/** One failed write's alert copy, ready for an `Alert`'s `message` and `description`. */
export type VolumeFailure = {
  message: string;
  description: string;
};

/**
 * The client-side half of the upload cap: a file already over 1 GiB is refused
 * before a request is made, because sending it would only earn the API's own
 * 413. The API's check is the one that counts — the browser is not the limit.
 */
export function uploadTooLarge(file: { name: string; size: number }): VolumeFailure | undefined {
  if (file.size <= UPLOAD_MAX_BYTES) {
    return undefined;
  }
  return {
    message: 'Upload exceeds the 1 GiB cap',
    description: `${file.name} is ${formatBytes(file.size)} — larger than the 1 GiB cap on one upload, so it was never sent.`,
  };
}

/**
 * The alert copy for a failed upload. The API's status picks the limit that was
 * hit — 413 the 1 GiB cap, 507 the Volume's free space — and the API's own
 * message is kept in the description, because it carries the helper's
 * need-vs-available numbers and the operator can act on those.
 */
export function uploadFailure(error: unknown): VolumeFailure {
  return writeFailure('upload', error);
}

/** The same mapping for a failed delete: 400 is the root-path refusal the API worded. */
export function deleteFailure(error: unknown): VolumeFailure {
  return writeFailure('delete', error);
}

/** The same mapping for a failed edit-save: the write family's statuses, worded for a save. */
export function editFailure(error: unknown): VolumeFailure {
  return writeFailure('edit', error);
}

function writeFailure(action: 'upload' | 'delete' | 'edit', error: unknown): VolumeFailure {
  const status = error instanceof ApiError ? error.status : undefined;
  const api = error instanceof Error ? error.message : String(error);
  switch (status) {
    case 413:
      return {
        message: action === 'upload' ? 'Upload exceeds the 1 GiB cap' : 'The save exceeds the 1 GiB cap',
        description: `The panel caps one write at 1 GiB, so nothing was written. The API said: ${api}`,
      };
    case 507:
      return {
        message: 'The Volume is out of space',
        description: `The write was refused rather than left half-done — a refused write removes its partial file. The API said: ${api}`,
      };
    case 400:
      return {
        message:
          action === 'upload' ? 'The upload was refused' : action === 'delete' ? 'The delete was refused' : 'The save was refused',
        description: api,
      };
    case 409:
      return {
        message: 'This Volume’s source is missing',
        description: `${api} — mounting it is what would create it, so the panel refuses.`,
      };
    case 422:
      // The restore family's refusal (an archive the helper will not extract);
      // mapped here so a future caller gets the helper's own words.
      return {
        message: 'The Volume refused the file',
        description: api,
      };
    default:
      return {
        message: action === 'upload' ? 'Upload failed' : action === 'delete' ? 'Delete failed' : 'Save failed',
        description: api,
      };
  }
}

// ---- The editor (ticket 08) ---------------------------------------------------
//
// The text editor's pure decisions: the 1 MB entry gate, the rule that decides
// whether fetched bytes are text, dirty as a draft/original comparison, and the
// save's state. The page and the editor component read these; no DOM is needed
// to test any of them.

/**
 * The editor's cap (spec: Caps — "editable text file 1 MB"). A file at exactly
 * the cap opens; one byte more is refused before it is fetched, because the
 * whole file is held as a JavaScript string and Monaco would be unusable well
 * before the API's 1 GiB write cap.
 */
export const EDITOR_MAX_BYTES = 1 << 20;

/** How much of a file's head decides "binary": the first 8 KiB, the `file(1)`-style glance. */
export const EDITOR_SNIFF_BYTES = 8192;

/**
 * The entry-level gate, applied before any fetch: a file above 1 MB is never
 * read. `path` is the relative target the message names. A smaller file passes
 * — the content check happens once its bytes are in hand.
 */
export function editorGate(entry: VolumeEntry, path: string): VolumeFailure | undefined {
  if (entry.size <= EDITOR_MAX_BYTES) {
    return undefined;
  }
  return {
    message: 'Too large to edit',
    description: `${path} is ${formatBytes(entry.size)} — past the 1 MB the editor opens, so it was not opened. Download it instead.`,
  };
}

/** The fetched file, or why it will not be edited. */
export type EditorContent = { status: 'editable'; text: string } | { status: 'refused'; failure: VolumeFailure };

/**
 * Whether fetched bytes are a text file the editor can hold, and the decoded
 * text when they are. The rule, decided once and documented here:
 *
 *  - a NUL byte anywhere in the first 8 KiB marks the file binary — the
 *    classic sniff, and NUL is the one byte no text format uses;
 *  - otherwise the bytes must be valid UTF-8. The editor's model is Unicode
 *    text, so decoding invalid bytes would substitute replacement characters
 *    and a save would write those replacements back: data loss disguised as an
 *    edit. A file that fails either check is refused, never rendered as
 *    mojibake.
 */
export function editorContent(path: string, bytes: Uint8Array): EditorContent {
  if (bytes.subarray(0, EDITOR_SNIFF_BYTES).includes(0)) {
    return {
      status: 'refused',
      failure: {
        message: 'This file is not text',
        description: `${path} holds a NUL byte in its first ${formatBytes(EDITOR_SNIFF_BYTES)}, so it is binary — the editor refuses it rather than show mojibake. Download it instead.`,
      },
    };
  }
  try {
    return { status: 'editable', text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) };
  } catch {
    return {
      status: 'refused',
      failure: {
        message: 'This file is not UTF-8 text',
        description: `${path} is not valid UTF-8. Showing it would replace bytes the editor cannot represent, and saving would write the replacements — so it was not opened. Download it instead.`,
      },
    };
  }
}

/** Dirty is the one comparison that matters: the draft against the last saved state. */
export function isDirty(original: string, draft: string): boolean {
  return original !== draft;
}

/**
 * The editor's save state, derived from the three facts the page holds. A save
 * in flight outranks everything (the buttons must not race it) and a failure
 * outranks dirty (the draft is still unsaved, and the error stays on screen
 * until the next attempt).
 */
export type EditorSaveStatus = 'clean' | 'dirty' | 'saving' | 'failed';

export function editorSaveStatus(state: {
  dirty: boolean;
  failure: VolumeFailure | undefined;
  saving: boolean;
}): EditorSaveStatus {
  if (state.saving) {
    return 'saving';
  }
  if (state.failure !== undefined) {
    return 'failed';
  }
  return state.dirty ? 'dirty' : 'clean';
}

/**
 * The running-stack warning: whether it appears, and which Services it names.
 *
 * The condition is the Compose's own `status.running` — the field the API
 * refreshes from Docker on every read — so any Service up raises the banner.
 * The names are the Services that declare a use of this Volume, deduplicated:
 * those are the ones a write can reach. `GUIDELINES` §8 is why the copy names
 * the consequence rather than telling the operator to be careful, and why this
 * is a warning and never a gate.
 */
export type RunningWriteWarning = {
  /** Services this Volume is mounted by, in declaration order, each named once. */
  services: string[];
  message: string;
  description: string;
};

export function runningWriteWarning(
  status: ComposeStatus | undefined,
  volume: ComposeVolume | undefined,
): RunningWriteWarning | undefined {
  if (status === undefined || status.running <= 0) {
    return undefined;
  }
  const services = volume ? Array.from(new Set(volume.uses.map((use) => use.service))) : [];
  const consequence = (object: string) =>
    `Writing files a live process is using — a database’s data directory above all — can corrupt ${object}. ` +
    'There is no confirmation gate on a write: this banner is the whole warning.';
  if (services.length === 0) {
    return {
      description: `A Service in this Compose is up. ${consequence('it')}`,
      message: 'This Compose is running — writing can corrupt a live Service',
      services,
    };
  }
  const named = services.join(', ');
  const verb = services.length === 1 ? 'mounts' : 'mount';
  const object = services.length === 1 ? 'it' : 'them';
  return {
    description: `${named} ${verb} this Volume, and the Compose is up. ${consequence(object)}`,
    message: `This Compose is running — writing can corrupt ${named}`,
    services,
  };
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
