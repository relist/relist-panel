import { describe, expect, it } from 'vitest';

import type { Deployment } from './types';
import { deploymentButtonLabel, deploymentHeadline, isActive, stepLabel } from './deployments-view';

function deployment(overrides: Partial<Deployment> = {}): Deployment {
  return {
    number: 1,
    mode: 'deploy',
    noCache: false,
    freshVolumes: false,
    status: 'succeeded',
    step: '',
    steps: [],
    failedStep: '',
    reason: '',
    sourceRef: '',
    sourceSubject: '',
    warnings: [],
    queuedAt: '2026-01-01T00:00:00Z',
    startedAt: null,
    finishedAt: null,
    ...overrides,
  };
}

describe('stepLabel', () => {
  it('capitalises plain steps and names build steps by Service', () => {
    expect(stepLabel('fetch')).toBe('Fetch');
    expect(stepLabel('build:api')).toBe('Build api');
    expect(stepLabel('deploy')).toBe('Deploy');
  });
});

describe('deploymentHeadline', () => {
  it('says how many are ahead and what is running', () => {
    const d = deployment({ status: 'queued', ahead: 2 });
    expect(deploymentHeadline(d, { composeName: 'shop', number: 14, step: 'build:api' })).toBe(
      'Waiting — 2 ahead (shop #14 building)',
    );
  });

  it('omits the count when the API sends none', () => {
    expect(deploymentHeadline(deployment({ status: 'queued' }))).toBe('Waiting');
  });

  it('says only how many are ahead when the active one is unknown', () => {
    expect(deploymentHeadline(deployment({ status: 'queued', ahead: 1 }))).toBe('Waiting — 1 ahead');
  });

  it('names the running step', () => {
    expect(deploymentHeadline(deployment({ status: 'running', step: 'build:api' }))).toBe('Building api');
    expect(deploymentHeadline(deployment({ status: 'running', step: 'fetch' }))).toBe('Fetch');
  });

  it('names the failed step', () => {
    expect(deploymentHeadline(deployment({ status: 'failed', failedStep: 'build:web' }))).toBe('Failed at Build web');
  });

  it('reads the finished states', () => {
    expect(deploymentHeadline(deployment({ status: 'canceled' }))).toBe('Canceled');
    expect(deploymentHeadline(deployment({ status: 'succeeded' }))).toBe('Succeeded');
  });
});

describe('isActive', () => {
  it('is true only while queued or running', () => {
    expect(isActive(deployment({ status: 'queued' }))).toBe(true);
    expect(isActive(deployment({ status: 'running' }))).toBe(true);
    expect(isActive(deployment({ status: 'failed' }))).toBe(false);
    expect(isActive(deployment({ status: 'succeeded' }))).toBe(false);
    expect(isActive(deployment({ status: 'canceled' }))).toBe(false);
  });
});

describe('deploymentButtonLabel', () => {
  it('shows the count from list data while queued', () => {
    expect(deploymentButtonLabel(deployment({ status: 'queued', ahead: 2 }))).toBe('Queued · 2 ahead');
    expect(deploymentButtonLabel(deployment({ status: 'queued' }))).toBe('Queued');
  });

  it('is the headline otherwise', () => {
    expect(deploymentButtonLabel(deployment({ status: 'running', step: 'build:api' }))).toBe('Building api');
  });
});
