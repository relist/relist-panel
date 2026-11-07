import type { ComposeVolume, ComposeVolumes, OrphanVolume } from '../data/types';

/**
 * View decisions for the Compose screen's Volumes tab, kept pure so the
 * design's rules — when the tab is hidden, what the before-first-deploy state
 * says, which pre-flight outcomes are warned about and which refuse a backup —
 * are testable without a component or a store.
 *
 * Lives beside the panel (not in `pages/`) because the Volume Explorer, which
 * session 2 adds, reads the same resolution and the same pre-flight statuses.
 */

/** The CLI's phrase when the generated env file is not there yet — the before-first-deploy signature. */
const ENV_FILE_MISSING = "couldn't find env file";

/** The panel's wrapper around the CLI's resolution error. */
const RESOLUTION_PREFIX = 'volume resolution failed: ';

/** One pre-flight outcome that must be said out loud. */
export type VolumeWarning = {
  status: 'missing' | 'unverifiable';
  volume: ComposeVolume;
  /** The API's own explanation, naming the path and the consequence. */
  message: string;
};

export type VolumesView =
  /** The page's one fetch has not settled. */
  | { status: 'loading' }
  /** The feature cannot run at all (`available: false`); the page hides the tab. */
  | { status: 'hidden'; reason: string }
  /**
   * No deploy has written the env file yet. The API says so (`deployed: false`);
   * `message` is the CLI's own words only when the CLI was asked and refused.
   */
  | { status: 'not-deployed'; message?: string }
  /** Any other resolution failure: stays on the tab as an Alert with Retry. */
  | { status: 'error'; message: string }
  /** The Compose declares no Volumes — a fact about the file, not a failure. */
  | { status: 'empty' }
  | { status: 'ready'; volumes: ComposeVolume[]; warnings: VolumeWarning[] };

/** The API's own message, without the panel's `volume resolution failed:` wrapper. */
export function cliMessage(message: string): string {
  return message.startsWith(RESOLUTION_PREFIX) ? message.slice(RESOLUTION_PREFIX.length) : message;
}

/** Whether a resolution failure is the CLI's before-first-deploy signature. */
export function isNotDeployed(message: string): boolean {
  return message.includes(ENV_FILE_MISSING);
}

/** What the tab renders, from the page's single fetch: the payload, its error, or neither while it is in flight. */
export function volumesView(payload: ComposeVolumes | undefined, error: string | undefined): VolumesView {
  if (error !== undefined) {
    return isNotDeployed(error)
      ? { status: 'not-deployed', message: cliMessage(error) }
      : { status: 'error', message: error };
  }
  if (payload === undefined) {
    return { status: 'loading' };
  }
  if (!payload.available) {
    return { status: 'hidden', reason: payload.reason ?? '' };
  }
  if (!payload.deployed) {
    return { status: 'not-deployed' };
  }
  if (payload.volumes.length === 0) {
    return { status: 'empty' };
  }
  return { status: 'ready', volumes: payload.volumes, warnings: volumeWarnings(payload.volumes) };
}

export type OrphansView =
  | { status: 'loading' }
  /** The API's own message (409 colliding identity, 502 before the first deploy, …). */
  | { status: 'error'; message: string }
  | { status: 'empty' }
  | { status: 'ready'; orphans: OrphanVolume[] };

/** What the orphan section renders, from the page's one fetch of it. */
export function orphansView(payload?: { orphans: OrphanVolume[] }, error?: string): OrphansView {
  if (error !== undefined) {
    return { status: 'error', message: error };
  }
  if (payload === undefined) {
    return { status: 'loading' };
  }
  return payload.orphans.length === 0 ? { status: 'empty' } : { status: 'ready', orphans: payload.orphans };
}

/** The pre-flight outcomes that must be said out loud, in Volume order; `ok` is the silent majority. */
export function volumeWarnings(volumes: readonly ComposeVolume[]): VolumeWarning[] {
  return volumes.flatMap((volume) =>
    volume.preflight.status === 'ok'
      ? []
      : [{ message: volume.preflight.warning, status: volume.preflight.status, volume }],
  );
}

/** One Volume's mountable path: the bind's own source, or the named volume's `_data` directory. */
export function resolvedVolumePath(volume: ComposeVolume): string {
  return volume.kind === 'bind' ? volume.source : `/var/lib/docker/volumes/${volume.source}/_data`;
}

/** The name a row leads with: the declared logical name, or the resolved source for a bind. */
export function volumeLabel(volume: ComposeVolume): string {
  return volume.logicalName || volume.source;
}

/** Why this Volume's backup is refused, if it is: a missing source would be created by the mount. */
export function downloadRefusal(volume: ComposeVolume): string | undefined {
  if (volume.preflight.status !== 'missing') {
    return undefined;
  }
  return volume.preflight.warning || `the source ${volume.source} is missing`;
}

/** Docker identities must be an explicit numeric uid:gid; never infer root. */
export function volumeIdentityValid(user: string | undefined): user is string {
  return (
    user !== undefined &&
    /^[0-9]+:[0-9]+$/.test(user) &&
    user.split(':').every((id) => Number(id) <= 0xffffffff)
  );
}

/**
 * Why a Service's resolved user cannot be the operation identity, if it
 * cannot. `--user` resolves a name (and a bare uid's group) against the
 * helper image's `/etc/passwd`, not the Service's own, so only an explicit
 * numeric uid:gid is safe to run as; the API refuses the same shapes.
 */
export function serviceUserRefusal(user: string | undefined): string | undefined {
  if (user === undefined || volumeIdentityValid(user)) {
    return undefined;
  }
  return `This Service’s resolved user is ${user}, which is not a numeric uid:gid. The helper cannot run as a name or a bare uid — pick “Numeric uid:gid override” and enter the account this Service actually runs as.`;
}

/** Both the Explorer and direct archive launcher gate every mount on these risks. */
export function volumeAccessReady(
  user: string | undefined,
  unverifiable: boolean,
  access: { rootConfirmed?: boolean; bindConfirmed?: boolean },
): boolean {
  return (
    volumeIdentityValid(user) &&
    (Number(user.split(':')[0]) !== 0 || access.rootConfirmed === true) &&
    (!unverifiable || access.bindConfirmed === true)
  );
}
