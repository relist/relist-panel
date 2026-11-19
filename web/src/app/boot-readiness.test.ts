import { describe, expect, it } from 'vitest';

import { bootWaitingOn, CHECKING_SIGN_IN } from './boot-readiness';

describe('bootWaitingOn', () => {
  it('waits for the Migration lock before any hold', () => {
    expect(bootWaitingOn({ lockSettled: false, holds: ['Loading projects'], capped: false })).toBe(CHECKING_SIGN_IN);
  });

  it('names the first hold still waiting', () => {
    expect(bootWaitingOn({ lockSettled: true, holds: [null, 'Checking services', 'Loading projects'], capped: false }))
      .toBe('Checking services');
  });

  it('is ready once the lock has settled and every hold is released', () => {
    expect(bootWaitingOn({ lockSettled: true, holds: [null, null], capped: false })).toBeNull();
    expect(bootWaitingOn({ lockSettled: true, holds: [], capped: false })).toBeNull();
  });

  it('gives up waiting once the cap runs out', () => {
    expect(bootWaitingOn({ lockSettled: false, holds: ['Loading projects'], capped: true })).toBeNull();
  });
});
