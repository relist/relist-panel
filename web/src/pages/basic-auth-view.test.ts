import { describe, expect, it } from 'vitest';

import { ApiError } from '../data/api';
import type { AuthGroupDomain } from '../data/types';
import { generatePassword, inUseDomains, usedByLabel } from './basic-auth-view';

const used: AuthGroupDomain = {
  id: 1,
  host: 'a.example.com',
  path: '/',
  projectId: 1,
  environmentId: 2,
  composeId: 3,
  projectName: 'Shop',
  environmentName: 'prod',
  composeName: 'web',
};

describe('generatePassword', () => {
  it('makes 24 letters and digits by default', () => {
    const password = generatePassword();
    expect(password).toHaveLength(24);
    expect(password).toMatch(/^[A-Za-z0-9]+$/);
  });

  it('honours the length and never repeats', () => {
    expect(generatePassword(10)).toHaveLength(10);
    const seen = new Set(Array.from({ length: 50 }, () => generatePassword()));
    expect(seen.size).toBe(50);
  });
});

describe('usedByLabel', () => {
  it('shows the root path as nothing', () => {
    expect(usedByLabel(used)).toBe('a.example.com — Shop / prod / web');
  });

  it('keeps another path', () => {
    expect(usedByLabel({ ...used, path: '/api' })).toBe('a.example.com/api — Shop / prod / web');
  });
});

describe('inUseDomains', () => {
  it('reads the domains of a 409', () => {
    const error = new ApiError(409, 'in use', undefined, { error: 'in use', domains: [used] });
    expect(inUseDomains(error)).toEqual([used]);
  });

  it('is empty for any other error', () => {
    expect(inUseDomains(new ApiError(500, 'boom', undefined, { domains: [used] }))).toEqual([]);
    expect(inUseDomains(new ApiError(409, 'in use'))).toEqual([]);
  });
});
