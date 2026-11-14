import type { ApiError } from '../data/api';
import type { AuthGroupDomain } from '../data/types';

const ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';

/**
 * A random password of letters and digits from `crypto.getRandomValues`.
 * Bytes past the largest multiple of the alphabet size are skipped, so every
 * character is equally likely.
 */
export function generatePassword(length = 24): string {
  const limit = 256 - (256 % ALPHABET.length);
  let out = '';
  while (out.length < length) {
    const bytes = crypto.getRandomValues(new Uint8Array(length * 2));
    for (const byte of bytes) {
      if (byte < limit && out.length < length) out += ALPHABET[byte % ALPHABET.length];
    }
  }
  return out;
}

/** `host/path — Project / Environment / Compose`; the root path shows as nothing. */
export function usedByLabel(domain: AuthGroupDomain): string {
  const path = domain.path === '/' ? '' : domain.path;
  return `${domain.host}${path} — ${domain.projectName} / ${domain.environmentName} / ${domain.composeName}`;
}

/** The Domains a refused delete (409) names, or none for any other error. */
export function inUseDomains(error: ApiError): AuthGroupDomain[] {
  if (error.status !== 409) return [];
  const body = error.body as { domains?: unknown } | undefined;
  return Array.isArray(body?.domains) ? (body.domains as AuthGroupDomain[]) : [];
}
