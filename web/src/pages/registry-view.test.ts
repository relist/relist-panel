import { describe, expect, it } from 'vitest';

import type { ExternalRegistry } from '../data/types';
import { CHECK_STEP, REGISTRY_STATUS, TOKEN_HINT, loginActionSnippet, loginCommand, sharedTags, shortDigest, testState } from './registry-view';

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

describe('Panel registry helpers', () => {
  it('builds the docker login command and the login-action snippet', () => {
    expect(loginCommand('registry.example.com', 'github-ci')).toBe('docker login registry.example.com -u github-ci');
    expect(loginActionSnippet('registry.example.com', 'github-ci')).toBe(
      [
        '- uses: docker/login-action@v3',
        '  with:',
        '    registry: registry.example.com',
        '    username: github-ci',
        '    password: ${{ secrets.RELIST_REGISTRY_TOKEN }}',
      ].join('\n'),
    );
  });

  it('lists every tag sharing the digest, the asked tag first', () => {
    const tags = [
      { created: '', digest: 'sha256:a', inUseBy: [], kept: false, size: 1, tag: 'v1' },
      { created: '', digest: 'sha256:b', inUseBy: [], kept: false, size: 1, tag: 'v2' },
      { created: '', digest: 'sha256:a', inUseBy: [], kept: false, size: 1, tag: 'latest' },
    ];
    expect(sharedTags(tags, 'v1')).toEqual(['v1', 'latest']);
    expect(sharedTags(tags, 'v2')).toEqual(['v2']);
  });

  it('shortens a digest to 12 hex characters', () => {
    expect(shortDigest('sha256:0123456789abcdef0123')).toBe('0123456789ab');
  });

  it('labels each status and check step', () => {
    expect(REGISTRY_STATUS.running.label).toBe('Running');
    expect(REGISTRY_STATUS['read-only'].label).toBe('Read-only (cleanup)');
    expect(REGISTRY_STATUS.drift.label).toBe('Drift');
    expect(CHECK_STEP.dns).toBe('DNS');
    expect(CHECK_STEP.tls).toBe('TLS');
    expect(CHECK_STEP.auth).toBe('Login');
  });
});
