// @vitest-environment jsdom
import '../test/dom-setup';

import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Deployment } from '../data/types';
import { useDeploymentHistory } from './useDeploymentHistory';

const api = vi.hoisted(() => ({ deploymentQueue: vi.fn(), listDeployments: vi.fn() }));
vi.mock('../data/api', () => ({ api }));

const path = { composeId: 7, environmentId: 2, projectId: 1 };

function deployment(over: Partial<Deployment> = {}): Deployment {
  return {
    failedStep: '',
    finishedAt: null,
    freshVolumes: false,
    mode: 'build',
    noCache: false,
    number: 3,
    queuedAt: '2026-10-11T10:00:00Z',
    reason: '',
    sourceRef: '',
    sourceSubject: '',
    startedAt: null,
    status: 'running',
    step: 'fetch',
    steps: ['fetch', 'deploy'],
    warnings: [],
    ...over,
  };
}

async function tick(ms = 1000) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  api.deploymentQueue.mockResolvedValue([]);
});
afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  vi.resetAllMocks();
});

describe('useDeploymentHistory', () => {
  it('fires onFinished once when a Deployment leaves active, then stops polling', async () => {
    api.listDeployments
      .mockResolvedValueOnce([deployment()])
      .mockResolvedValue([deployment({ status: 'succeeded' })]);
    const onFinished = vi.fn();
    const { result } = renderHook(() => useDeploymentHistory(path, onFinished));
    await tick(0);
    expect(result.current.latest?.status).toBe('running');
    expect(onFinished).not.toHaveBeenCalled();
    await tick();
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(result.current.latest?.status).toBe('succeeded');
    await tick(10000);
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(api.listDeployments).toHaveBeenCalledTimes(2);
  });

  it('does not poll when nothing is active', async () => {
    api.listDeployments.mockResolvedValue([deployment({ status: 'succeeded' })]);
    renderHook(() => useDeploymentHistory(path, vi.fn()));
    await tick(10000);
    expect(api.listDeployments).toHaveBeenCalledTimes(1);
  });

  it('stops retrying failed reads when nothing is known to be active', async () => {
    api.listDeployments.mockRejectedValue(new Error('down'));
    renderHook(() => useDeploymentHistory(path, vi.fn()));
    await tick(10000);
    expect(api.listDeployments).toHaveBeenCalledTimes(1);
  });

  it('backs off to 5 s after three failures while something is active', async () => {
    api.listDeployments
      .mockResolvedValueOnce([deployment()])
      .mockRejectedValue(new Error('down'));
    renderHook(() => useDeploymentHistory(path, vi.fn()));
    await tick(0); // ok
    await tick(1000); // fail 1
    await tick(1000); // fail 2
    await tick(1000); // fail 3
    expect(api.listDeployments).toHaveBeenCalledTimes(4);
    await tick(4000);
    expect(api.listDeployments).toHaveBeenCalledTimes(4);
    await tick(1000);
    expect(api.listDeployments).toHaveBeenCalledTimes(5);
  });
});
