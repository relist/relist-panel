import { describe, expect, it } from 'vitest';

import { enableStepIndex, RESET_COMMAND } from './account-view';
import type { EnableStep } from './account-view';

/**
 * The pure rules of the Account page: where each step of the turn-on flow
 * sits in the `Steps` bar, and the exact recovery command the page shows. The
 * component itself is thin over the API, so this is where the values are
 * pinned.
 */

describe('enableStepIndex', () => {
  it('maps the three turn-on steps onto their steps-bar index', () => {
    expect(enableStepIndex('password')).toBe(0);
    expect(enableStepIndex('scan')).toBe(1);
    expect(enableStepIndex('verify')).toBe(2);
  });

  it('covers every step of the flow', () => {
    const steps: EnableStep[] = ['password', 'scan', 'verify'];
    expect(steps.map(enableStepIndex)).toEqual([0, 1, 2]);
  });
});

describe('RESET_COMMAND', () => {
  it('is the exact host recovery command', () => {
    expect(RESET_COMMAND).toBe('docker compose exec panel relist-panel reset-2fa');
  });
});
