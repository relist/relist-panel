// @vitest-environment jsdom
import '../test/dom-setup';

import { act, fireEvent, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Deployment } from '../data/types';
import { ComposeDeploymentsTab } from './ComposeDeploymentsTab';

const api = vi.hoisted(() => ({
  cancelDeployment: vi.fn(),
  deploymentLog: vi.fn(),
  deploymentQueue: vi.fn(),
  listDeployments: vi.fn(),
}));

vi.mock('../data/api', () => ({ api }));

const location = { composeId: 7, environmentId: 2, projectId: 1 };

function deployment(over: Partial<Deployment> = {}): Deployment {
  return {
    finishedAt: null,
    failedStep: '',
    freshVolumes: false,
    mode: 'build',
    noCache: false,
    number: 3,
    queuedAt: '2026-10-11T10:00:00Z',
    reason: '',
    sourceRef: 'a1b2c3d',
    sourceSubject: 'Fix the cart',
    startedAt: '2026-10-11T10:00:01Z',
    status: 'running',
    step: 'build:api',
    steps: ['fetch', 'build:api', 'deploy'],
    warnings: [],
    ...over,
  };
}

const compose = { id: 7 } as never;

function renderTab(props: { onFinished?: () => void; selected?: number } = {}) {
  const onFinished = props.onFinished ?? vi.fn();
  render(
    <App>
      <ComposeDeploymentsTab
        compose={compose}
        location={location}
        onFinished={onFinished}
        onSelect={vi.fn()}
        selected={props.selected}
      />
    </App>,
  );
  return onFinished;
}

/** Lets pending promises settle, then moves the poll clock on. */
async function tick(ms = 1000) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  api.deploymentQueue.mockResolvedValue([]);
  api.deploymentLog.mockResolvedValue({ done: true, offset: 0, text: '' });
});

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe('ComposeDeploymentsTab', () => {
  it('lists the history with number and status', async () => {
    api.listDeployments.mockResolvedValue([
      deployment({ number: 4, status: 'queued', step: '', ahead: 1 }),
      deployment({ number: 3, status: 'failed', failedStep: 'build:api', finishedAt: '2026-10-11T10:01:00Z' }),
    ]);
    renderTab();
    await tick(0);
    expect(screen.getAllByText('#4').length).toBeGreaterThan(0);
    expect(screen.getAllByText('#3').length).toBeGreaterThan(0);
    expect(screen.getByText('queued')).toBeTruthy();
    expect(screen.getByText('failed')).toBeTruthy();
  });

  it('marks the running step as the current one', async () => {
    api.listDeployments.mockResolvedValue([deployment()]);
    renderTab();
    await tick(0);
    const current = screen.getByText('Build api').closest('.ant-steps-item');
    expect(current?.className).toContain('ant-steps-item-active');
    expect(screen.getByText('Fetch').closest('.ant-steps-item')?.className).toContain('ant-steps-item-finish');
  });

  it('appends polled log chunks in order, and stops after done', async () => {
    api.listDeployments.mockResolvedValue([deployment()]);
    api.deploymentLog
      .mockResolvedValueOnce({ done: false, offset: 6, text: 'first\n' })
      .mockResolvedValueOnce({ done: true, offset: 13, text: 'second\n' });
    renderTab();
    await tick(0);
    expect(screen.getByTestId('deployment-log').textContent).toBe('first\n');
    await tick();
    expect(screen.getByTestId('deployment-log').textContent).toBe('first\nsecond\n');
    expect(api.deploymentLog).toHaveBeenLastCalledWith(location, 3, 6);
    await tick(5000);
    expect(api.deploymentLog).toHaveBeenCalledTimes(2);
  });

  it('fires onFinished once when the shown Deployment stops being active', async () => {
    api.listDeployments
      .mockResolvedValueOnce([deployment()])
      .mockResolvedValue([deployment({ status: 'succeeded', step: '', finishedAt: '2026-10-11T10:02:00Z' })]);
    const onFinished = renderTab();
    await tick(0);
    expect(onFinished).not.toHaveBeenCalled();
    await tick();
    expect(onFinished).toHaveBeenCalledTimes(1);
    await tick(5000);
    expect(onFinished).toHaveBeenCalledTimes(1);
    expect(api.listDeployments).toHaveBeenCalledTimes(2);
  });

  it('disables Cancel once the Deployment reaches deploy', async () => {
    api.listDeployments.mockResolvedValue([deployment({ step: 'deploy' })]);
    renderTab();
    await tick(0);
    expect((screen.getByRole('button', { name: /cancel/i }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('cancels a queued Deployment', async () => {
    api.listDeployments.mockResolvedValue([deployment({ status: 'queued', step: '', ahead: 2 })]);
    api.cancelDeployment.mockResolvedValue(undefined);
    renderTab();
    await tick(0);
    expect(screen.getByText(/Waiting — 2 ahead/)).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    });
    expect(api.cancelDeployment).toHaveBeenCalledWith(location, 3);
  });

  it('shows warnings in an Alert', async () => {
    api.listDeployments.mockResolvedValue([deployment({ status: 'succeeded', warnings: ['port 80 is taken'] })]);
    renderTab();
    await tick(0);
    const alert = screen.getByText('port 80 is taken').closest('.ant-alert');
    expect(alert).toBeTruthy();
  });

  it('shows the failure reason', async () => {
    api.listDeployments.mockResolvedValue([
      deployment({ status: 'failed', failedStep: 'build:api', reason: 'timeout' }),
    ]);
    renderTab();
    await tick(0);
    expect(screen.getByText('timeout')).toBeTruthy();
    expect(screen.getByText(/Failed at Build api/)).toBeTruthy();
  });
});
