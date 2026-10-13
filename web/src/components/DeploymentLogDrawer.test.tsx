// @vitest-environment jsdom
import '../test/dom-setup';

import { act, fireEvent, render, screen } from '@testing-library/react';
import { App } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../data/api';
import type { Deployment } from '../data/types';
import { DeploymentLogDrawer } from './DeploymentLogDrawer';

vi.mock('../data/api', () => ({ api: { deploymentLog: vi.fn(), getDeployment: vi.fn(), cancelDeployment: vi.fn() } }));
const location = { projectId: 1, environmentId: 2, composeId: 3 };
const d: Deployment = {
  number: 1, status: 'running', step: 'build:api', steps: ['fetch', 'build:api', 'deploy'],
  mode: 'build', noCache: false, freshVolumes: false, failedStep: '', reason: '',
  sourceRef: '', sourceSubject: '', warnings: [], queuedAt: '2026-10-13T10:00:00Z', startedAt: null, finishedAt: null,
};
async function tick(ms = 0) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
beforeEach(() => {
  vi.useFakeTimers();
  vi.mocked(api.deploymentLog).mockResolvedValue({ text: 'log\n', done: true, offset: 4 });
});
afterEach(() => { vi.useRealTimers(); vi.resetAllMocks(); });

describe('DeploymentLogDrawer', () => {
  it('loads a queue selection and follows its status until it finishes', async () => {
    vi.mocked(api.getDeployment).mockResolvedValueOnce(d).mockResolvedValue({ ...d, status: 'succeeded' });
    render(<App><DeploymentLogDrawer target={{ location, number: 1 }} follow onClose={vi.fn()} /></App>);
    await tick();
    expect(screen.getByText('Building api')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeTruthy();
    await tick(1000);
    expect(screen.getByText('Succeeded')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
    await tick(5000);
    expect(api.getDeployment).toHaveBeenCalledTimes(2);
  });

  it('isolates the same deployment number in different Composes and ignores late detail/log responses', async () => {
    let detail!: (value: Deployment) => void;
    let log!: (value: { text: string; done: boolean; offset: number }) => void;
    vi.mocked(api.getDeployment).mockImplementation(({ composeId }) => composeId === 3
      ? new Promise((resolve) => { detail = resolve; }) : Promise.resolve({ ...d, status: 'failed', reason: 'new reason' }));
    vi.mocked(api.deploymentLog).mockImplementation(({ composeId }) => composeId === 3
      ? new Promise((resolve) => { log = resolve; }) : Promise.resolve({ text: 'new log', done: true, offset: 7 }));
    const drawer = (composeId: number) => <App><DeploymentLogDrawer target={{ location: { ...location, composeId }, number: 1 }} follow onClose={vi.fn()} /></App>;
    const result = render(drawer(3));
    await tick();
    result.rerender(drawer(4));
    await tick();
    await act(async () => { detail({ ...d, status: 'failed', reason: 'old reason' }); log({ text: 'old log', done: true, offset: 7 }); });
    expect(screen.getByText('new reason')).toBeTruthy();
    expect(screen.queryByText('old reason')).toBeNull();
    expect(screen.getByTestId('deployment-log').textContent).toBe('new log');
  });

  it('shows detail failures with Retry and stops both reads when closed', async () => {
    vi.mocked(api.getDeployment).mockRejectedValueOnce(new Error('not available')).mockResolvedValue(d);
    const drawer = (open: boolean) => <App><DeploymentLogDrawer target={open ? { location, number: 1 } : undefined} follow onClose={vi.fn()} /></App>;
    const result = render(drawer(true));
    await tick();
    expect(screen.getByText(/not available/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await tick();
    expect(screen.getByText('Building api')).toBeTruthy();
    result.rerender(drawer(false));
    await tick(6000);
    expect(api.getDeployment).toHaveBeenCalledTimes(2);
    expect(api.deploymentLog).toHaveBeenCalledTimes(1);
  });
});
