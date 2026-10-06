import { describe, expect, it } from 'vitest';

import type { SignInStep } from './signin-view';
import { backToPassword, nextStep, rateLimitMessage } from './signin-view';

/**
 * The rules of the two-step sign-in, kept pure so they can be exercised
 * without a router or a store: when the second step appears, what it holds,
 * and what Back clears. The held email and password live only in the step.
 */

const submitted = { email: 'admin@relist.local', password: 'hunter2' };

describe('nextStep', () => {
  it('moves the password step to the code step and holds the submitted email and password', () => {
    expect(nextStep({ step: 'password' }, submitted, { kind: 'codeRequired' })).toEqual({
      step: 'code',
      email: 'admin@relist.local',
      password: 'hunter2',
    });
  });

  it('keeps the current step on signedIn and rateLimited', () => {
    const current: SignInStep = { step: 'password' };
    expect(nextStep(current, submitted, { kind: 'signedIn' })).toEqual(current);
    expect(nextStep(current, submitted, { kind: 'rateLimited', retryAfterSeconds: 42 })).toEqual(current);
  });

  it('keeps the current code step and its held credentials when a code is required again', () => {
    const current: SignInStep = { step: 'code', email: 'admin@relist.local', password: 'hunter2' };
    expect(nextStep(current, { email: 'other@relist.local', password: 'other' }, { kind: 'codeRequired' })).toEqual(
      current,
    );
  });
});

describe('backToPassword', () => {
  it('resets held credentials on back', () => {
    expect(backToPassword()).toEqual({ step: 'password' });
  });
});

describe('rateLimitMessage', () => {
  it('renders the exact copy', () => {
    expect(rateLimitMessage(42)).toBe('Too many attempts. Try again in 42 seconds.');
  });
});
