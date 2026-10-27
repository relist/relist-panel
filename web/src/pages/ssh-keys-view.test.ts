import { describe, expect, it } from 'vitest';

import type { SshKey, SshKeyUse } from '../data/types';
import { deleteWarning, shortPublicKey, usedByLabel } from './ssh-keys-view';

const use = (n: number): SshKeyUse => ({
  composeId: n,
  composeName: `c${n}`,
  environmentId: 1,
  projectId: 1,
  projectName: 'p',
});

const key = (uses: number): SshKey => ({
  createdAt: '2026-01-01T00:00:00Z',
  id: 1,
  name: 'ci',
  publicKey: 'ssh-ed25519 AAAA relist-ci',
  usedBy: Array.from({ length: uses }, (_, i) => use(i + 1)),
});

describe('usedByLabel', () => {
  it.each([
    [0, 'Not used'],
    [1, 'c1'],
    [2, 'c1, c2'],
    [4, 'c1, c2 +2 more'],
  ])('with %i uses', (uses, label) => {
    expect(usedByLabel(key(uses))).toBe(label);
  });
});

describe('deleteWarning', () => {
  it('is null when unused and the in-use copy otherwise', () => {
    expect(deleteWarning(key(0))).toBeNull();
    for (const uses of [1, 2, 4]) {
      expect(deleteWarning(key(uses))).toBe(
        "These Sources will have no SSH key and can't deploy until you choose another.",
      );
    }
  });
});

describe('shortPublicKey', () => {
  it('keeps the type and comment and trims the base64 body to its last 12 characters', () => {
    expect(shortPublicKey('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOnlyTheBodyWXYZ relist-ci')).toBe(
      'ssh-ed25519 …yTheBodyWXYZ relist-ci',
    );
  });

  it('returns a body of 12 characters or fewer whole', () => {
    expect(shortPublicKey('ssh-ed25519 AAAABBBBCCCC relist-ci')).toBe('ssh-ed25519 AAAABBBBCCCC relist-ci');
    expect(shortPublicKey('ssh-ed25519 AAAA')).toBe('ssh-ed25519 AAAA');
  });
});
