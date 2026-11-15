import type { SshKey } from '../data/types';

const SHOWN_USES = 2;
const KEY_TAIL = 12;

export const DELETE_IN_USE_WARNING = "These Sources will have no SSH key and can't deploy until you choose another.";

/** Who uses the key: the first two Compose names, then how many more. */
export function usedByLabel(key: SshKey): string {
  const destinations = key.usedByDestinations.map((name) => `Backup destination: ${name}`);
  if (key.usedBy.length === 0) {
    return destinations.length === 0 ? 'Not used' : destinations.join(', ');
  }
  const names = key.usedBy.slice(0, SHOWN_USES).map((use) => use.composeName).join(', ');
  const more = key.usedBy.length - SHOWN_USES;
  return [more > 0 ? `${names} +${more} more` : names, ...destinations].join(', ');
}

/** Why a key cannot be deleted: a Backup destination logs in with it. Null when it can. */
export function deleteBlockedReason(key: SshKey): string | null {
  if (key.usedByDestinations.length === 0) return null;
  return `Used by Backup destinations: ${key.usedByDestinations.join(', ')}. Change those destinations first.`;
}

/**
 * `ssh-ed25519 …WXYZ relist-ci`: the base64 body is cut to its last 12 characters,
 * because every ed25519 body starts with the same prefix.
 */
export function shortPublicKey(publicKey: string): string {
  const [type, body, ...comment] = publicKey.trim().split(/\s+/);
  if (body === undefined || body.length <= KEY_TAIL) {
    return publicKey.trim();
  }
  return [type, `…${body.slice(-KEY_TAIL)}`, ...comment].join(' ');
}

export function deleteWarning(key: SshKey): string | null {
  return key.usedBy.length > 0 ? DELETE_IN_USE_WARNING : null;
}
