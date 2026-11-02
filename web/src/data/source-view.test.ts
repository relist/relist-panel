import { describe, expect, it } from 'vitest';

import type { ComposeSource, Deployment } from './types';
import { buildServices, repoName, sourceCardState, sourceLoadState } from './source-view';

function git(overrides: Partial<ComposeSource> = {}): ComposeSource {
  return {
    kind: 'git',
    repoUrl: 'git@github.com:acme/shop.git',
    branch: 'main',
    submodules: false,
    sparsePaths: [],
    sshKey: null,
    headSha: '',
    lastTest: null,
    zip: null,
    ...overrides,
  };
}

function zip(uploadedAt: string): ComposeSource {
  return git({
    kind: 'zip',
    repoUrl: '',
    branch: '',
    zip: { name: 'code.zip', size: 10, files: 3, uploadedAt },
  });
}

function build(overrides: Partial<Deployment> = {}): Deployment {
  return {
    number: 3,
    mode: 'build',
    noCache: false,
    freshVolumes: false,
    status: 'succeeded',
    step: '',
    steps: [],
    failedStep: '',
    reason: '',
    sourceRef: 'aaaaaaa',
    sourceSubject: '',
    warnings: [],
    queuedAt: '2026-01-01T00:00:00Z',
    startedAt: '2026-01-01T00:00:01Z',
    finishedAt: '2026-01-01T00:10:00Z',
    ...overrides,
  };
}

describe('sourceCardState', () => {
  it('flags a git Source without an SSH key', () => {
    expect(sourceCardState(git({ sshKey: null }), undefined).noKey).toBe(true);
    expect(sourceCardState(git({ sshKey: { id: 1, name: 'ci', publicKey: 'k' } }), undefined).noKey).toBe(false);
    expect(sourceCardState(zip('2026-01-01T00:00:00Z'), undefined).noKey).toBe(false);
    expect(sourceCardState(null, undefined).noKey).toBe(false);
  });

  it('is none without a Source', () => {
    const state = sourceCardState(null, undefined);
    expect(state.kind).toBe('none');
    expect(state.newCommits).toBe(false);
    expect(state.notBuilt).toBe(false);
  });

  it('flags new commits only when the head is known and differs from the last build', () => {
    expect(sourceCardState(git({ headSha: 'bbbbbbb' }), build()).newCommits).toBe(true);
    expect(sourceCardState(git({ headSha: 'aaaaaaa' }), build()).newCommits).toBe(false);
    expect(sourceCardState(git({ headSha: '' }), build()).newCommits).toBe(false);
  });

  it('does not flag new commits against a build that did not succeed', () => {
    expect(sourceCardState(git({ headSha: 'bbbbbbb' }), build({ status: 'failed' })).newCommits).toBe(false);
  });

  it('marks a zip not built when uploaded after the last build started', () => {
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), build()).notBuilt).toBe(true);
    // Uploaded during the build (started 00:00:01, finished 00:10:00).
    expect(sourceCardState(zip('2026-01-01T00:05:00Z'), build()).notBuilt).toBe(true);
    expect(sourceCardState(zip('2026-01-01T00:00:00Z'), build()).notBuilt).toBe(false);
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), undefined).notBuilt).toBe(true);
  });

  it('titles the card by kind and describes the last build', () => {
    const state = sourceCardState(git({ headSha: 'aaaaaaa' }), build({ sourceRef: 'aaaaaaa', sourceSubject: 'Fix cart' }));
    expect(state.kind).toBe('git');
    expect(state.title).toBe('acme/shop · main');
    expect(state.builtLine).toBe('Built aaaaaaa — Fix cart');
    expect(sourceCardState(git(), undefined).builtLine).toBe('Not built yet');
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), undefined).title).toBe('code.zip');
  });
});

describe('buildServices', () => {
  it('lists Services with build:, keeping YAML order', () => {
    const yaml = 'services:\n  db:\n    image: postgres\n  api:\n    build: ./src/api\n  web:\n    build:\n      context: ./src/web\n';
    expect(buildServices(yaml)).toEqual(['api', 'web']);
  });

  it('is empty for broken YAML', () => {
    expect(buildServices('services: [')).toEqual([]);
  });
});

describe('repoName', () => {
  it('reads owner/repo from scp-style and ssh:// URLs', () => {
    expect(repoName('git@github.com:acme/shop.git')).toBe('acme/shop');
    expect(repoName('ssh://git@host.io:2222/team/sub/shop.git')).toBe('sub/shop');
  });

  it('falls back to the URL', () => {
    expect(repoName('git@host:shop.git')).toBe('git@host:shop.git');
  });
});

describe('sourceLoadState', () => {
  it('is loading until this Compose answered, and for another Compose\'s answer', () => {
    expect(sourceLoadState(undefined, undefined, 1)).toBe('loading');
    expect(sourceLoadState({ composeId: 2, value: null }, undefined, 1)).toBe('loading');
  });

  it('tells none from present once loaded', () => {
    expect(sourceLoadState({ composeId: 1, value: null }, undefined, 1)).toBe('none');
    expect(sourceLoadState({ composeId: 1, value: git() }, undefined, 1)).toBe('present');
  });

  it('is failed after a failed load, even over an older answer, but only for that Compose', () => {
    expect(sourceLoadState({ composeId: 1, value: null }, 1, 1)).toBe('failed');
    expect(sourceLoadState(undefined, 1, 2)).toBe('loading');
  });
});
