import type { ComposeVolume, ComposeVolumes } from '../data/types';

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
  /** The Compose CLI could not resolve because no deploy has written the env file yet. */
  | { status: 'not-deployed'; message: string }
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
  if (payload.volumes.length === 0) {
    return { status: 'empty' };
  }
  return { status: 'ready', volumes: payload.volumes, warnings: volumeWarnings(payload.volumes) };
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
