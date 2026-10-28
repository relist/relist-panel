import { describe, expect, it } from 'vitest';

import { expiryState } from './certificates-view';

const now = new Date('2026-10-28T00:00:00Z');
const plus = (ms: number) => new Date(now.getTime() + ms).toISOString();
const DAY = 24 * 60 * 60 * 1000;

describe('expiryState', () => {
  it('is ok more than 30 days out', () => {
    expect(expiryState(plus(31 * DAY), now)).toBe('ok');
  });

  it('is soon under 30 days out', () => {
    expect(expiryState(plus(29 * DAY), now)).toBe('soon');
  });

  it('is expired once past', () => {
    expect(expiryState(plus(-1000), now)).toBe('expired');
  });
});
