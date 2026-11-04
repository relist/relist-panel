import { describe, expect, it } from 'vitest';

import { RECOVERY_TEXT, isTerminal, stepIndex, stepStatus } from './traefik-apply-view';

describe('stepIndex', () => {
  it('maps every state of a Traefik apply to its step', () => {
    expect(stepIndex('validating')).toBe(0);
    expect(stepIndex('dry-run')).toBe(0);
    expect(stepIndex('writing')).toBe(1);
    expect(stepIndex('restarting')).toBe(2);
    expect(stepIndex('checking')).toBe(3);
    expect(stepIndex('rolling-back')).toBe(3);
    expect(stepIndex('ok')).toBe(4);
    expect(stepIndex('rolled-back')).toBe(4);
    expect(stepIndex('rollback-failed')).toBe(4);
    expect(stepIndex('refused')).toBe(0);
  });
});

describe('stepStatus', () => {
  it('marks rollbacks and refusals as errors and ok as finished', () => {
    expect(stepStatus('checking')).toBe('process');
    expect(stepStatus('rolling-back')).toBe('error');
    expect(stepStatus('rolled-back')).toBe('error');
    expect(stepStatus('rollback-failed')).toBe('error');
    expect(stepStatus('refused')).toBe('error');
    expect(stepStatus('ok')).toBe('finish');
  });
});

describe('isTerminal', () => {
  it('is true only for end states', () => {
    for (const state of ['ok', 'rolled-back', 'rollback-failed', 'refused']) expect(isTerminal(state)).toBe(true);
    for (const state of ['validating', 'dry-run', 'writing', 'restarting', 'checking', 'rolling-back'])
      expect(isTerminal(state)).toBe(false);
  });
});

describe('RECOVERY_TEXT', () => {
  it('gives the SSH tunnel and the manual restore', () => {
    const text = RECOVERY_TEXT(8888, '20261104T100000Z-abcdefgh');
    expect(text).toContain('ssh -L 8888:127.0.0.1:8888 <host>');
    expect(text).toContain('20261104T100000Z-abcdefgh');
    expect(text).toContain('cp $D/history/<backup>/traefik.override.yml $D/history/<backup>/traefik.yml $D/');
    expect(text).toContain('docker restart relist-traefik');
  });

  it('leaves out the backup name when the run has none', () => {
    expect(RECOVERY_TEXT(8888)).not.toContain('already failed');
  });
});
