import type { LoginResult } from '../data/api';

/**
 * The two-step sign-in's state machine and copy, kept pure so the rules can be
 * tested without a router or a store. The held email and password live in the
 * step itself and nowhere else — not in the store, not in storage — so Back
 * discards them by returning a credential-free password step.
 */

export type SignInStep =
  | { step: 'password' }
  | { step: 'code'; email: string; password: string };

/**
 * Where a sign-in result leaves the screen. `codeRequired` turns the password
 * step into the code step, holding the credentials just submitted; if the code
 * step is already showing, it keeps its held credentials instead of overwriting
 * them. `signedIn` and `rateLimited` change nothing — the first navigates away
 * on the next render, the second shows its message on the current step.
 */
export function nextStep(
  current: SignInStep,
  submitted: { email: string; password: string },
  result: LoginResult,
): SignInStep {
  if (result.kind !== 'codeRequired') {
    return current;
  }
  if (current.step === 'code') {
    return current;
  }
  return { step: 'code', email: submitted.email, password: submitted.password };
}

/** Back from the code step: a fresh password step, with no credentials held. */
export function backToPassword(): SignInStep {
  return { step: 'password' };
}

/** The lock's message, in the API's own words. */
export function rateLimitMessage(seconds: number): string {
  return `Too many attempts. Try again in ${seconds} seconds.`;
}
