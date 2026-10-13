// @vitest-environment jsdom
import '../test/dom-setup';

import { act, fireEvent, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Deployment } from '../data/types';
import { ComposeDeploymentsTab } from './ComposeDeploymentsTab';

const api = vi.hoisted(() => ({
  cancelDeployment: vi.fn(),
  deploymentLog: vi.fn(),
  deploymentQueue: vi.fn(),
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

function renderTab(history: Deployment[], props: { selected?: number } = {}) {
  function Tab() {
    const [selected, setSelected] = useState(props.selected);
    return <ComposeDeploymentsTab compose={compose} location={location} history={history} onSelect={setSelected} selected={selected} />;
  }
  return render(<App><Tab /></App>);
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
  vi.resetAllMocks();
});

describe('ComposeDeploymentsTab', () => {
  it('opens logs on demand and stops polling when the drawer closes', async () => {
    api.deploymentLog.mockResolvedValue({ done: false, offset: 6, text: 'first\n' });
    renderTab([deployment()]);
    await tick(3000);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(api.deploymentLog).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'View logs for deployment #3' }));
    await tick(0);
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(screen.getByTestId('deployment-log').textContent).toBe('first\n');
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await tick(5000);
    expect(api.deploymentLog).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'View logs for deployment #3' }));
    await tick(0);
    expect(api.deploymentLog).toHaveBeenLastCalledWith(location, 3, 0);
  });

  it('closes and stops polling when its tab becomes inactive without reopening on return', async () => {
    api.deploymentLog.mockResolvedValue({ done: false, offset: 6, text: 'first\n' });
    const tab = (active: boolean, selected = 3) => <App><ComposeDeploymentsTab compose={compose} location={location} history={[deployment(), deployment({ number: 4 })]} onSelect={vi.fn()} selected={selected} active={active} /></App>;
    const result = render(tab(true));
    await tick(0);
    expect(screen.getByRole('dialog')).toBeTruthy();
    result.rerender(tab(false));
    await tick(5000);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(api.deploymentLog).toHaveBeenCalledTimes(1);
    result.rerender(tab(true));
    await tick(0);
    expect(screen.queryByRole('dialog')).toBeNull();
    result.rerender(tab(true, 4));
    await tick(0);
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(api.deploymentLog).toHaveBeenLastCalledWith(location, 4, 0);
  });

  it('discards a late log response when another deployment is selected', async () => {
    let resolveOld!: (chunk: { done: boolean; offset: number; text: string }) => void;
    api.deploymentLog.mockImplementation((_location, number) => number === 3
      ? new Promise((resolve) => { resolveOld = resolve; })
      : Promise.resolve({ done: true, offset: 4, text: 'new\n' }));
    renderTab([deployment({ number: 4 }), deployment()]);
    fireEvent.click(screen.getByRole('button', { name: 'View logs for deployment #3' }));
    await tick(0);
    // The parent may select a new Deployment immediately after enqueueing it.
    fireEvent.click(screen.getByRole('button', { name: 'View logs for deployment #4' }));
    await tick(0);
    await act(async () => { resolveOld({ done: true, offset: 4, text: 'old\n' }); });
    expect(screen.getByTestId('deployment-log').textContent).toBe('new\n');
    expect(api.deploymentLog).toHaveBeenLastCalledWith(location, 4, 0);
  });

  it('lists the history with number and status', async () => {
    renderTab([
      deployment({ number: 4, status: 'queued', step: '', ahead: 1 }),
      deployment({ number: 3, status: 'failed', failedStep: 'build:api', finishedAt: '2026-10-11T10:01:00Z' }),
    ]);
    await tick(0);
    expect(screen.getAllByText('#4').length).toBeGreaterThan(0);
    expect(screen.getAllByText('#3').length).toBeGreaterThan(0);
    expect(screen.getByText('queued')).toBeTruthy();
    expect(screen.getByText('failed')).toBeTruthy();
  });

  it('marks the running step as the current one', async () => {
    renderTab([deployment()], { selected: 3 });
    await tick(0);
    const current = screen.getByText('Build api').closest('.ant-steps-item');
    expect(current?.className).toContain('ant-steps-item-active');
    expect(screen.getByText('Fetch').closest('.ant-steps-item')?.className).toContain('ant-steps-item-finish');
  });

  it('appends polled log chunks in order, and stops after done', async () => {
    api.deploymentLog
      .mockResolvedValueOnce({ done: false, offset: 6, text: 'first\n' })
      .mockResolvedValueOnce({ done: true, offset: 13, text: 'second\n' });
    renderTab([deployment()], { selected: 3 });
    await tick(0);
    expect(screen.getByTestId('deployment-log').textContent).toBe('first\n');
    await tick();
    expect(screen.getByTestId('deployment-log').textContent).toBe('first\nsecond\n');
    expect(api.deploymentLog).toHaveBeenLastCalledWith(location, 3, 6);
    await tick(5000);
    expect(api.deploymentLog).toHaveBeenCalledTimes(2);
  });

  it('keeps Cancel enabled at deploy and shows the API message on a 409', async () => {
    api.cancelDeployment.mockRejectedValue(Object.assign(new Error('deployment can no longer be canceled'), { status: 409 }));
    renderTab([deployment({ step: 'deploy' })], { selected: 3 });
    await tick(0);
    const button = screen.getByRole('button', { name: /cancel/i }) as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    await act(async () => {
      fireEvent.click(button);
    });
    expect(api.cancelDeployment).toHaveBeenCalledWith(location, 3);
    expect(screen.getByText('deployment can no longer be canceled').closest('.ant-message-info')).toBeTruthy();
  });

  it('cancels a queued Deployment', async () => {
    api.cancelDeployment.mockResolvedValue(undefined);
    renderTab([deployment({ status: 'queued', step: '', ahead: 2 })], { selected: 3 });
    await tick(0);
    expect(screen.getByText(/Waiting — 2 ahead/)).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    });
    expect(api.cancelDeployment).toHaveBeenCalledWith(location, 3);
  });

  it('shows warnings in an Alert', async () => {
    renderTab([deployment({ status: 'succeeded', warnings: ['port 80 is taken'] })], { selected: 3 });
    await tick(0);
    const alert = screen.getByText('port 80 is taken').closest('.ant-alert');
    expect(alert).toBeTruthy();
  });

  it('shows the failure reason', async () => {
    renderTab([
      deployment({ status: 'failed', failedStep: 'build:api', reason: 'timeout' }),
    ], { selected: 3 });
    await tick(0);
    expect(screen.getByText('timeout')).toBeTruthy();
    expect(screen.getByText(/Failed at Build api/)).toBeTruthy();
  });
});
