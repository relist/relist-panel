import { describe, expect, it } from 'vitest';
import type { HostMetrics, MetricSample } from './types';
import { diskPressure } from './disk-pressure';

const now = 100_000;
const sample = (used: number | null, total: number | null, at = now): MetricSample => ({
  at, cpu: null, memUsed: null, memTotal: null, diskUsed: used, diskTotal: total,
  dockerUsed: null, dockerTotal: null, blkRead: null, blkWrite: null, netRx: null, netTx: null,
});
const metrics = (...samples: MetricSample[]): HostMetrics => ({
  window: '15m', from: 'ring', intervalSeconds: 10, partial: true,
  sources: { diskSpace: { available: true } }, samples,
});

describe('Panel data filesystem pressure', () => {
  it.each([[799, 'healthy'], [800, 'warning'], [901, 'warning']] as const)('classifies %s of 1000 bytes at the exact 80%% threshold', (used, kind) => {
    const pressure = diskPressure(metrics(sample(used, 1000)), undefined, now);
    expect(pressure).toMatchObject({ kind, freeBytes: 1000 - used });
    expect('percent' in pressure && pressure.percent).toBeCloseTo(used / 10);
  });

  it.each([[null, 1000], [800, null], [800, 0], [-1, 1000], [1100, 1000], [NaN, 1000], [800, Infinity]])('does not invent disk readings from %s / %s', (used, total) => {
    expect(diskPressure(metrics(sample(used, total)), undefined, now).kind).toBe('unavailable');
  });

  it('uses the latest sample rather than searching backwards for a favorable reading', () => {
    expect(diskPressure(metrics(sample(900, 1000, now - 10_000), sample(null, null)), undefined, now).kind).toBe('unavailable');
    expect(diskPressure(metrics(sample(900, 1000), sample(200, 1000, now - 10_000)), undefined, now)).toMatchObject({ kind: 'warning', percent: 90 });
  });

  it('honors the source status and its reason even with cached valid samples', () => {
    const payload = metrics(sample(900, 1000));
    payload.sources.diskSpace = { available: false, reason: 'statfs denied' };
    expect(diskPressure(payload, undefined, now)).toEqual({ kind: 'unavailable', reason: 'statfs denied' });
    delete payload.sources.diskSpace;
    expect(diskPressure(payload, undefined, now).kind).toBe('unavailable');
  });

  it('marks old readings and request failures stale without claiming healthy current usage', () => {
    expect(diskPressure(metrics(sample(900, 1000, now - 30_001)), undefined, now)).toMatchObject({ kind: 'stale', percent: 90, warning: true });
    expect(diskPressure(metrics(sample(200, 1000)), new Error('offline'), now)).toMatchObject({ kind: 'stale', percent: 20, warning: false, reason: 'offline' });
    expect(diskPressure(metrics(sample(200, 1000, now - 30_000)), undefined, now).kind).toBe('healthy');
  });

  it('shows unknown or loading states instead of zero and recovers on a fresh sample', () => {
    expect(diskPressure(undefined, undefined, now).kind).toBe('loading');
    expect(diskPressure(undefined, new Error('offline'), now)).toEqual({ kind: 'unavailable', reason: 'offline' });
    expect(diskPressure(metrics(), undefined, now).kind).toBe('unavailable');
    expect(diskPressure(metrics(sample(700, 1000)), undefined, now).kind).toBe('healthy');
  });
});
