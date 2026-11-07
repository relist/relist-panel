import { describe, expect, it } from 'vitest';

import type { ExternalRegistry } from '../data/types';
import { TOKEN_HINT, testState } from './registry-view';

const base: ExternalRegistry = { host: 'ghcr.io', id: 1, lastTestAt: null, lastTestError: '', lastTestOk: false, username: 'ci' };

describe('testState', () => {
  it('is untested before the first Test', () => {
    expect(testState(base)).toBe('untested');
  });
  it('passes or fails by the last result', () => {
    expect(testState({ ...base, lastTestAt: '2026-11-06T00:00:00Z', lastTestOk: true })).toBe('passed');
    expect(testState({ ...base, lastTestAt: '2026-11-06T00:00:00Z', lastTestError: 'denied' })).toBe('failed');
  });
});

describe('TOKEN_HINT', () => {
  it('asks for a pull-only token', () => {
    expect(TOKEN_HINT).toBe('Use a token that can only pull, such as a GitHub PAT with read:packages.');
  });
});
