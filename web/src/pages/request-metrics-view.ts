import type { MetricPoint, MetricSeries } from '../components/MetricChart';
import { summarizeMetric } from '../components/MetricChart';
import type { RequestMetrics, RequestWindow } from '../data/types';

/**
 * Screen decisions for the request metrics views, pure so they test in the
 * node env. Two rules matter: an hourly bucket is divided by 60 so a 24h and a
 * 7d chart read on one scale (requests per minute), and a null percentile is a
 * gap — the point is left out, never drawn as 0 ms.
 */

export const ALL_REQUEST_WINDOWS: { window: RequestWindow; minRetentionDays: number }[] = [
  { window: '15m', minRetentionDays: 0 },
  { window: '1h', minRetentionDays: 0 },
  { window: '6h', minRetentionDays: 0 },
  { window: '24h', minRetentionDays: 0 },
  { window: '7d', minRetentionDays: 7 },
  { window: '30d', minRetentionDays: 30 },
  { window: '90d', minRetentionDays: 90 },
];

export const DEFAULT_REQUEST_WINDOW: RequestWindow = '1h';

/** The windows the server will answer for this retention; it 400s on the rest. */
export function windowsFor(retentionDays: number): RequestWindow[] {
  return ALL_REQUEST_WINDOWS.filter((entry) => entry.minRetentionDays <= retentionDays).map((entry) => entry.window);
}

function isHourly(window: RequestWindow): boolean {
  return window === '7d' || window === '30d' || window === '90d';
}

/** 2xx / 3xx / 4xx / 5xx in requests per minute. 1xx and other are in the summary only. */
export function statusSeries(metrics: RequestMetrics): MetricSeries[] {
  const divisor = isHourly(metrics.window) ? 60 : 1;
  const line = (key: 'c2xx' | 'c3xx' | 'c4xx' | 'c5xx', label: string): MetricSeries => ({
    key,
    label,
    points: metrics.buckets.map((bucket) => ({ at: bucket.at, value: bucket[key] / divisor })),
  });
  return [line('c2xx', '2xx'), line('c3xx', '3xx'), line('c4xx', '4xx'), line('c5xx', '5xx')];
}

/** p50 / p90 / p99 in ms; a bucket without a percentile leaves a gap. */
export function latencySeries(metrics: RequestMetrics): MetricSeries[] {
  const line = (key: 'p50Ms' | 'p90Ms' | 'p99Ms', label: string): MetricSeries => {
    const points: MetricPoint[] = [];
    for (const bucket of metrics.buckets) {
      const value = bucket[key];
      if (value !== null && Number.isFinite(value)) points.push({ at: bucket.at, value });
    }
    return { key, label, points };
  };
  return [line('p50Ms', 'p50'), line('p90Ms', 'p90'), line('p99Ms', 'p99')];
}

/** The Status chart's header: all four classes added per bucket, in requests per minute. */
export function statusHeader(series: MetricSeries[]) {
  const totals = new Map<number, number>();
  for (const entry of series) {
    for (const point of entry.points) totals.set(point.at, (totals.get(point.at) ?? 0) + point.value);
  }
  return summarizeMetric([...totals].sort((x, y) => x[0] - y[0]).map(([at, value]) => ({ at, value })));
}

/** The Latency chart's header is the p50 line: the typical request. */
export function latencyHeader(series: MetricSeries[]) {
  return summarizeMetric(series.find((entry) => entry.key === 'p50Ms')?.points ?? []);
}

function ms(value: number | null): string {
  if (value === null) return '—';
  return value >= 1000 ? `${Number((value / 1000).toFixed(2))} s` : `${Math.round(value)} ms`;
}

export function summaryItems(metrics: RequestMetrics): { label: string; value: string }[] {
  const { summary } = metrics;
  const rate = summary.requests > 0 ? `${Number(((summary.c5xx / summary.requests) * 100).toFixed(2))}%` : '—';
  return [
    { label: 'Requests', value: summary.requests.toLocaleString('en-US') },
    { label: '5xx rate', value: rate },
    { label: 'p50', value: ms(summary.p50Ms) },
    { label: 'p90', value: ms(summary.p90Ms) },
    { label: 'p99', value: ms(summary.p99Ms) },
  ];
}
