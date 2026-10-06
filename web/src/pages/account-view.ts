/**
 * The Account page's pure rules, kept out of the component so the values are
 * pinned without a router or the API: where each step of the turn-on flow sits
 * in the `Steps` bar, and the recovery command the page always shows.
 */

export type EnableStep = 'password' | 'scan' | 'verify';

/** The turn-on flow's order: Password → Scan → Verify. */
export function enableStepIndex(step: EnableStep): 0 | 1 | 2 {
  switch (step) {
    case 'password':
      return 0;
    case 'scan':
      return 1;
    case 'verify':
      return 2;
  }
}

/**
 * The host command that clears two-factor and the sign-in lock. Shown
 * permanently on the page: a lost authenticator is the one failure the panel
 * cannot fix from here, so the recovery path is visible before it is needed.
 */
export const RESET_COMMAND = 'docker compose exec panel relist-panel reset-2fa';
