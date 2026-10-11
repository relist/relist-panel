import { describe, expect, it } from 'vitest';

import type { ComposeSource, Deployment } from './types';
import { sourceCardState } from './source-view';

function git(overrides: Partial<ComposeSource> = {}): ComposeSource {
  return {
    kind: 'git',
    repoUrl: 'git@github.com:acme/shop.git',
    branch: 'main',
    submodules: false,
    publicKey: 'ssh-ed25519 AAAA',
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

  it('marks a zip not built when uploaded after the last build finished', () => {
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), build()).notBuilt).toBe(true);
    expect(sourceCardState(zip('2026-01-01T00:05:00Z'), build()).notBuilt).toBe(false);
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), undefined).notBuilt).toBe(true);
  });

  it('titles the card by kind and describes the last build', () => {
    const state = sourceCardState(git({ headSha: 'aaaaaaa' }), build({ sourceRef: 'aaaaaaa', sourceSubject: 'Fix cart' }));
    expect(state.kind).toBe('git');
    expect(state.title).toBe('git@github.com:acme/shop.git · main');
    expect(state.builtLine).toBe('Built aaaaaaa — Fix cart');
    expect(sourceCardState(git(), undefined).builtLine).toBe('Not built yet');
    expect(sourceCardState(zip('2026-01-01T00:20:00Z'), undefined).title).toBe('code.zip');
  });
});
