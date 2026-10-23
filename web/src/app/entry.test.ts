import { describe, expect, it } from 'vitest';

import type { AuthState } from '../data/store';
import { entryScreen, signInTarget } from './entry';

const auth = (overrides: Partial<AuthState> = {}): AuthState => ({
  checked: true,
  authenticated: false,
  ...overrides,
});

describe('entryScreen', () => {
  it('sends an authenticated visitor to the app', () => {
    expect(entryScreen(auth({ authenticated: true }))).toBe('app');
  });

  it('lands everyone else on sign-in, even a panel with no admin yet', () => {
    expect(entryScreen(auth())).toBe('signin');
  });
});

describe('signInTarget', () => {
  it('honours a panel-absolute deep link', () => {
    expect(signInTarget('/projects/2/env/3/settings', '/')).toBe('/projects/2/env/3/settings');
    expect(signInTarget('/', '/')).toBe('/');
  });

  it('falls back on anything that is not a panel-absolute path', () => {
    const fallback = '/';
    expect(signInTarget(undefined, fallback)).toBe(fallback);
    expect(signInTarget(null, fallback)).toBe(fallback);
    expect(signInTarget('', fallback)).toBe(fallback);
    expect(signInTarget('https://evil.example/', fallback)).toBe(fallback);
    // Protocol-relative URLs leave the panel.
    expect(signInTarget('//evil.example', fallback)).toBe(fallback);
    expect(signInTarget(42, fallback)).toBe(fallback);
  });
});
