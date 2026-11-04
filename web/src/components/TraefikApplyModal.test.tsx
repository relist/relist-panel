// @vitest-environment jsdom
import '../test/dom-setup';

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '../data/api';
import type { TraefikApplyRun } from '../data/types';
import { TraefikApplyModal } from './TraefikApplyModal';

const api = vi.hoisted(() => ({
  ackTraefikApply: vi.fn(),
  applyTraefikConfig: vi.fn(),
  traefikApplyStatus: vi.fn(),
}));

vi.mock('../data/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../data/api')>()),
  api,
}));

// The diff is Monaco; a marker stands in for it.
vi.mock('./DiffViewer', () => ({
  DiffViewer: ({ modified, original }: { modified: string; original: string }) => (
    <div data-testid="diff">{`${original}=>${modified}`}</div>
  ),
}));

function run(state: string, overrides: Partial<TraefikApplyRun> = {}): TraefikApplyRun {
  return { acked: false, id: 'run1', kind: 'apply', startedAt: '2026-11-04T10:00:00Z', state, ...overrides };
}

const onClose = vi.fn();
const onFinished = vi.fn();

const review = { current: '# old\n', draft: 'log: {level: DEBUG}\n', effective: '# new\n', revision: 'rev1' };

/** Lets pending promises settle without moving the clock. */
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

/** Moves the clock past one poll and lets its answer land. */
async function poll() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2000);
  });
}

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers();
  api.ackTraefikApply.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('TraefikApplyModal', () => {
  it('reviews the generated file and says Traefik restarts', () => {
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={review} runId={null} />);
    expect(screen.getByTestId('diff').textContent).toBe('# old\n=># new\n');
    expect(screen.getByText(/Every Domain and this panel drop for a few seconds while Traefik restarts/)).toBeTruthy();
  });

  it('applies the draft against its revision and follows the steps', async () => {
    api.applyTraefikConfig.mockResolvedValue({ id: 'run1' });
    api.traefikApplyStatus.mockResolvedValueOnce(run('writing')).mockResolvedValueOnce(run('checking'));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={review} runId={null} />);
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await flush();
    expect(api.applyTraefikConfig).toHaveBeenCalledWith('log: {level: DEBUG}\n', 'rev1');
    await poll();
    expect(document.querySelector('.ant-steps-item-process')?.textContent).toContain('Write');
    await poll();
    expect(document.querySelector('.ant-steps-item-process')?.textContent).toContain('Health check');
  });

  it('keeps polling through a restart and says it is reconnecting', async () => {
    api.traefikApplyStatus
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce(run('ok'));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={null} runId="run1" />);
    await poll();
    await poll();
    expect(screen.getByText('Traefik is restarting — reconnecting…')).toBeTruthy();
    await poll();
    expect(screen.queryByText('Traefik is restarting — reconnecting…')).toBeNull();
    expect(screen.getByText('Traefik restarted with the new config.')).toBeTruthy();
  });

  it('shows why it rolled back and the log', async () => {
    api.traefikApplyStatus.mockResolvedValue(
      run('rolled-back', { log: 'ERR Command error', reason: 'Traefik exited: Command error' }),
    );
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={null} runId="run1" />);
    await poll();
    expect(screen.getByText('Traefik exited: Command error')).toBeTruthy();
    expect(screen.getByText('ERR Command error')).toBeTruthy();
  });

  it('gives the recovery steps when the rollback failed too', async () => {
    api.traefikApplyStatus.mockResolvedValue(run('rollback-failed', { backup: '20261104T100000Z-abcdefgh', reason: 'no /healthz' }));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={null} runId="run1" />);
    await poll();
    const recovery = screen.getByText(/ssh -L/);
    expect(recovery.textContent).toContain('docker restart relist-traefik');
  });

  it('acknowledges the result on Close', async () => {
    api.traefikApplyStatus.mockResolvedValue(run('ok'));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={null} runId="run1" />);
    await poll();
    fireEvent.click(screen.getByText('Close').closest('button')!);
    await flush();
    expect(api.ackTraefikApply).toHaveBeenCalledWith('run1');
    expect(onClose).toHaveBeenCalled();
    expect(onFinished).toHaveBeenCalled();
  });

  it('stays open on Escape while the run is still going', async () => {
    api.traefikApplyStatus.mockResolvedValue(run('checking'));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={null} runId="run1" />);
    await poll();
    fireEvent.keyDown(document.querySelector('.ant-modal-wrap') ?? document.body, { key: 'Escape', keyCode: 27 });
    await flush();
    expect(api.ackTraefikApply).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('says the configuration changed when Apply gets a 409', async () => {
    api.applyTraefikConfig.mockRejectedValue(new ApiError(409, 'revision changed'));
    render(<TraefikApplyModal onClose={onClose} onFinished={onFinished} review={review} runId={null} />);
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    await flush();
    expect(screen.getByText('The configuration changed since you opened it. Reload to see the latest.')).toBeTruthy();
  });
});
