import type { AuthState } from '../data/store';

/**
 * The entry-flow decisions the auth screens share, kept pure so the routing
 * table (`authenticated → app, else sign-in`) can be tested without a router.
 * There is no setup screen: the admin comes from `relist-panel create-admin`.
 */

/** Which screen the visitor should be on, once the auth status has settled. */
export type EntryScreen = 'app' | 'signin';

export function entryScreen(auth: AuthState): EntryScreen {
  return auth.authenticated ? 'app' : 'signin';
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
