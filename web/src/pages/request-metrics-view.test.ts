import { describe, expect, it } from 'vitest';

import type { RequestBucket, RequestMetrics } from '../data/types';
import { latencySeries, statusSeries, summaryItems, windowsFor } from './request-metrics-view';

const bucket = (over: Partial<RequestBucket> = {}): RequestBucket => ({
  at: 1_000,
  c1xx: 0,
  c2xx: 0,
  c3xx: 0,
  c4xx: 0,
  c5xx: 0,
  cother: 0,
  p50Ms: 10,
  p90Ms: 20,
  p99Ms: 30,
  ...over,
});

const metrics = (over: Partial<RequestMetrics> = {}): RequestMetrics => ({
  window: '1h',
  partial: false,
  accessLogEnabled: true,
  accessLogFiltered: false,
  retentionDays: 1,
  summary: { requests: 0, c1xx: 0, c2xx: 0, c3xx: 0, c4xx: 0, c5xx: 0, cother: 0, p50Ms: null, p90Ms: null, p99Ms: null },
  buckets: [],
  ...over,
});

describe('request metrics view', () => {
  it('windowsFor offers only what the retention allows', () => {
    expect(windowsFor(1)).toEqual(['15m', '1h', '6h', '24h']);
    expect(windowsFor(7)).toEqual(['15m', '1h', '6h', '24h', '7d']);
    expect(windowsFor(29)).toEqual(['15m', '1h', '6h', '24h', '7d']);
    expect(windowsFor(90)).toEqual(['15m', '1h', '6h', '24h', '7d', '30d', '90d']);
  });

  it('reads minute buckets as they are', () => {
    const [ok] = statusSeries(metrics({ buckets: [bucket({ c2xx: 600 })] }));
    expect(ok.points).toEqual([{ at: 1_000, value: 600 }]);
  });

  it('reads hourly buckets per minute', () => {
    const series = statusSeries(metrics({ window: '7d', buckets: [bucket({ c2xx: 600, c5xx: 30 })] }));
    expect(series.find((s) => s.key === 'c2xx')?.points[0].value).toBe(10);
    expect(series.find((s) => s.key === 'c5xx')?.points[0].value).toBe(0.5);
  });

  it('draws null percentiles as gaps, never zero', () => {
    const series = latencySeries(
      metrics({ buckets: [bucket({ at: 1 }), bucket({ at: 2, p99Ms: null }), bucket({ at: 3 })] }),
    );
    expect(series.find((s) => s.key === 'p99Ms')?.points.map((p) => p.at)).toEqual([1, 3]);
    expect(series.find((s) => s.key === 'p50Ms')?.points).toHaveLength(3);
  });

  it('summarises the 5xx rate and percentiles', () => {
    const items = summaryItems(
      metrics({ summary: { requests: 1000, c1xx: 0, c2xx: 995, c3xx: 0, c4xx: 0, c5xx: 5, cother: 0, p50Ms: 12, p90Ms: 80, p99Ms: 1250 } }),
    );
    expect(items.map((i) => i.value)).toEqual(['1,000', '0.5%', '12 ms', '80 ms', '1.25 s']);
    expect(summaryItems(metrics()).map((i) => i.value)).toEqual(['0', '—', '—', '—', '—']);
  });
});
