import { describe, expect, it } from 'vitest';

import { UPDATE_STEPS, finishedRun, pageState } from './panel-update';
import type { PanelUpdateRun, PanelUpdateStatus } from './panel-update';

const run = (over: Partial<PanelUpdateRun> = {}): PanelUpdateRun => ({
  id: 'r1',
  state: 'quiescing',
  terminal: false,
  error: '',
  rollbackError: '',
  waitingOn: '',
  note: '',
  fromVersion: 'v1.0.0',
  toVersion: 'v1.1.0',
  startedAt: 1,
  backupPath: '',
  backupFiles: [],
  ...over,
});

const status = (over: Partial<PanelUpdateStatus> = {}): PanelUpdateStatus => ({
  version: 'v1.0.0',
  development: false,
  available: null,
  checkEnabled: true,
  lastCheckAt: null,
  lastError: '',
  install: { available: true, reason: '' },
  locked: false,
  run: null,
  ...over,
});

describe('pageState', () => {
  it('is waiting for a quiescing run', () => {
    expect(pageState({ status: status({ run: run() }), awaitingRun: false })).toBe('waiting');
  });

  it('is updating for a run past quiescing, and while a started run has no status yet', () => {
    expect(pageState({ status: status({ run: run({ state: 'backing-up' }) }), awaitingRun: false })).toBe('updating');
    expect(pageState({ status: status(), awaitingRun: true })).toBe('updating');
  });

  it('lists the steps from quiescing, with no Updater start step', () => {
    expect(UPDATE_STEPS[0].state).toBe('quiescing');
    expect(UPDATE_STEPS.map((step) => step.state as string)).not.toContain('starting-updater');
  });
});

describe('finishedRun', () => {
  it('includes an aborted run', () => {
    const aborted = run({ state: 'aborted', terminal: true, error: 'cancelled' });
    expect(finishedRun(status({ run: aborted }))).toBe(aborted);
    expect(finishedRun(status({ run: run({ state: 'succeeded', terminal: true }) }))).toBeUndefined();
  });
});
