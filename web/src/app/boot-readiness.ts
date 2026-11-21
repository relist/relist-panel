/**
 * When the boot splash may step aside, kept pure so the rule can be tested
 * without timers or a router.
 *
 * The splash waits on positive facts, never on "nothing is pending": between
 * the Project tree resolving and the Overview starting its Service reads there
 * is a commit with nothing in flight, and a pending-based check would let the
 * empty Overview through in that gap. Each screen that the first paint depends
 * on registers a hold naming what it is still waiting for, and lets go
 * (`null`) once that read has settled — success or failure, so a failed load
 * releases the splash and its own error Alert takes over. The cap is the
 * backstop for a read that never settles.
 */

/** How long the splash waits at most before showing the panel as it is. */
export const BOOT_CAP_MS = 15_000;

/** How long the splash stays at least, so Relist-chan is seen rather than flashed. */
export const BOOT_MIN_MS = 3_000;

/** The splash's wording while the Migration lock's first read is in flight. */
export const CHECKING_SIGN_IN = 'Checking sign-in';

/** The splash's wording once everything has answered but the minimum has not passed. */
export const ALMOST_READY = 'Almost ready';

export type BootFacts = {
  /** The Migration lock's first read has settled. */
  lockSettled: boolean;
  /** Every registered hold: what it is waiting for, or null once released. */
  holds: Iterable<string | null>;
  /** The cap has run out. */
  capped: boolean;
  /** The splash has been up for its minimum. */
  minShown: boolean;
};

/** What the splash is still waiting for, or null once the panel may show. */
export function bootWaitingOn({ lockSettled, holds, capped, minShown }: BootFacts): string | null {
  if (capped) return null;
  if (!lockSettled) return CHECKING_SIGN_IN;
  for (const waitingOn of holds) {
    if (waitingOn !== null) return waitingOn;
  }
  return minShown ? null : ALMOST_READY;
}
