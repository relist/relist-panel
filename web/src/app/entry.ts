import type { AuthState } from '../data/store';

/**
 * The entry-flow decisions the auth screens share, kept pure so the monolith's
 * routing table (`authenticated → app, setupRequired → setup, else login`) can
 * be tested without a router.
 */

/** Which screen the visitor should be on, once the auth status has settled. */
export type EntryScreen = 'app' | 'setup' | 'signin';

export function entryScreen(auth: AuthState): EntryScreen {
  if (auth.authenticated) {
    return 'app';
  }
  return auth.setupRequired ? 'setup' : 'signin';
}

/**
 * Where sign-in sends the browser: back to the deep link the visitor was
 * redirected from, or the Overview. Only panel-absolute paths are honoured —
 * a `from` that is not one (or is protocol-relative, `//host`) falls back.
 */
export function signInTarget(from: unknown, fallback: string): string {
  if (typeof from !== 'string' || !from.startsWith('/') || from.startsWith('//')) {
    return fallback;
  }
  return from;
}
