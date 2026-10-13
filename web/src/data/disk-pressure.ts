import type { HostMetrics } from './types';

export const DISK_POLL_MS = 10_000;
export const DISK_MAX_AGE_MS = 30_000;
const DISK_WARNING_PERCENT = 80;

type Reading = { percent: number; freeBytes: number; at: number };
export type DiskPressure =
  | { kind: 'loading' }
  | { kind: 'unavailable'; reason: string }
  | ({ kind: 'healthy' | 'warning' } & Reading)
  | ({ kind: 'stale'; warning: boolean; reason: string } & Reading);

/** Latest statfs reading for the filesystem containing Panel/SQLite data. */
export function diskPressure(metrics: HostMetrics | undefined, error: unknown, now: number): DiskPressure {
  const failed = error !== undefined && error !== null;
  const errorReason = error instanceof Error ? error.message : 'Could not refresh disk usage.';
  if (!metrics) return failed ? { kind: 'unavailable', reason: errorReason } : { kind: 'loading' };
  const source = metrics.sources.diskSpace;
  if (!source?.available) {
    return { kind: 'unavailable', reason: source?.reason || 'The Panel data filesystem has not been sampled.' };
  }
  // Do not scan backwards past a missing last reading: it would conceal a
  // failed sampler behind an older good value. Timestamps also handle any
  // out-of-order envelope without claiming that the last array row is latest.
  const latest = metrics.samples.reduce<(typeof metrics.samples)[number] | undefined>(
    (previous, sample) => !previous || sample.at >= previous.at ? sample : previous, undefined,
  );
  if (!latest || !Number.isFinite(latest.at) || latest.at <= 0 || latest.at > now + DISK_POLL_MS ||
    latest.diskUsed === null || latest.diskTotal === null ||
    !Number.isFinite(latest.diskUsed) || !Number.isFinite(latest.diskTotal) ||
    latest.diskTotal <= 0 || latest.diskUsed < 0 || latest.diskUsed > latest.diskTotal) {
    return { kind: 'unavailable', reason: 'No disk usage reading is available for the Panel data filesystem.' };
  }
  const reading: Reading = {
    percent: latest.diskUsed / latest.diskTotal * 100,
    freeBytes: latest.diskTotal - latest.diskUsed,
    at: latest.at,
  };
  const warning = reading.percent >= DISK_WARNING_PERCENT;
  if (failed || now - latest.at > DISK_MAX_AGE_MS) {
    return { ...reading, kind: 'stale', warning, reason: failed ? errorReason : 'The latest disk sample is more than 30 seconds old.' };
  }
  return { ...reading, kind: warning ? 'warning' : 'healthy' };
}
