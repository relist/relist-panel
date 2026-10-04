import { describe, expect, it } from 'vitest';

import type { AuthState } from '../data/store';
import { entryScreen, signInTarget } from './entry';

const auth = (overrides: Partial<AuthState> = {}): AuthState => ({
  checked: true,
  setupRequired: false,
  authenticated: false,
  ...overrides,
});

describe('entryScreen', () => {
  it('sends an authenticated visitor to the app whatever the setup state is', () => {
    expect(entryScreen(auth({ authenticated: true }))).toBe('app');
    expect(entryScreen(auth({ authenticated: true, setupRequired: true }))).toBe('app');
  });

  it('lands a first-run visitor on setup, as the monolith did', () => {
    expect(entryScreen(auth({ setupRequired: true }))).toBe('setup');
  });

  it('lands a visitor with an account on sign-in', () => {
    expect(entryScreen(auth())).toBe('signin');
    expect(entryScreen(auth({ setupRequired: false }))).toBe('signin');
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
