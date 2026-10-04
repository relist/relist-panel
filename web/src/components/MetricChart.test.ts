import { describe, expect, it } from 'vitest';

import {
  axisMax,
  downsample,
  formatAxisValue,
  formatBytes,
  formatMetricValue,
  linePathFor,
  markersInWindow,
  niceMax,
  stepLinePathFor,
  summarizeMetric,
} from './MetricChart';
import type { MetricPoint } from './MetricChart';

function points(values: number[], startAt = 0, stepMs = 1_000): MetricPoint[] {
  return values.map((value, index) => ({ at: startAt + index * stepMs, value }));
}

describe('downsample — the min/max rule', () => {
  it('passes short series through untouched', () => {
    const series = points([1, 2, 3]);
    expect(downsample(series, 600)).toBe(series);
    expect(downsample(series, 3)).toBe(series);
  });

  it("keeps the spec's case: 360 samples in, at most one min+max pair per half-pixel bucket out", () => {
    const series = points(Array.from({ length: 360 }, (_, index) => 10 + (index % 7)));
    const out = downsample(series, 600);
    // Half the plot width in buckets, two points per bucket, at most.
    expect(out.length).toBeLessThanOrEqual(300 * 2);
    expect(out.every((point, index) => index === 0 || point.at > out[index - 1].at)).toBe(true);
  });

  it('preserves a one-sample spike that averaging would erase', () => {
    const values = Array.from({ length: 100 }, () => 5);
    values[37] = 95;
    const out = downsample(points(values), 50);
    expect(Math.max(...out.map((point) => point.value))).toBe(95);
    expect(Math.min(...out.map((point) => point.value))).toBe(5);
  });

  it("keeps every bucket's minimum and its maximum, in time order", () => {
    const out = downsample(points([9, 1, 2, 8, 3, 7, 4, 6]), 4);
    // Two buckets of four: [9,1,2,8] → 1 and 9 (9 first: earlier in time);
    // [3,7,4,6] → 3 and 7.
    expect(out.map((point) => point.value)).toEqual([9, 1, 3, 7]);
    expect(out.map((point) => point.at)).toEqual([0, 1_000, 4_000, 5_000]);
  });

  it('emits a single point for a flat bucket', () => {
    const out = downsample(points([4, 4, 4, 4, 9, 9, 9, 9]), 4);
    expect(out.map((point) => point.value)).toEqual([4, 9]);
    expect(out).toHaveLength(2);
  });

  it('still emits the min/max pair when the width forces a single bucket', () => {
    const out = downsample(points([1, 2]), 1);
    expect(out.map((point) => point.value)).toEqual([1, 2]);
  });
});

describe('unit formatting', () => {
  it('scales bytes up the 1024 ladder', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(2048)).toBe('2.0 kB');
    expect(formatBytes(5 * 1024 ** 2)).toBe('5.0 MB');
    expect(formatBytes(1.5 * 1024 ** 3)).toBe('1.5 GB');
    expect(formatBytes(120 * 1024 ** 3)).toBe('120 GB');
  });

  it('formats the header reading with its unit', () => {
    expect(formatMetricValue(12.34, 'percent')).toBe('12.3%');
    expect(formatMetricValue(2_500, 'bytesPerSecond')).toBe('2.4 kB/s');
    expect(formatMetricValue(5 * 1024 ** 3, 'bytes')).toBe('5.0 GB');
  });

  it('drops the unit on axis labels', () => {
    expect(formatAxisValue(0, 'percent')).toBe('0%');
    expect(formatAxisValue(75.4, 'percent')).toBe('75%');
    expect(formatAxisValue(2048, 'bytesPerSecond')).toBe('2.0 kB');
  });
});

describe('axis scaling', () => {
  it('ends byte-rate axes at a nice maximum above the data', () => {
    expect(niceMax(0)).toBe(1);
    expect(niceMax(4.2)).toBe(5);
    expect(niceMax(7)).toBe(10);
    expect(niceMax(150)).toBe(200);
    expect(axisMax('bytesPerSecond', 'fixed100', 150)).toBe(200);
  });

  it('pins percent axes at 0–100 unless the caller asks for auto', () => {
    expect(axisMax('percent', 'fixed100', 150)).toBe(100);
    expect(axisMax('percent', 'auto', 150)).toBe(200);
  });

  it("keeps a quiet container's auto percent axis off fractions of a percent", () => {
    expect(axisMax('percent', 'auto', 0.4)).toBe(5);
  });
});

describe('summaries', () => {
  it('averages, and reads current from the last point', () => {
    expect(summarizeMetric(points([2, 4, 6, 8]))).toEqual({ avg: 5, current: 8, max: 8, min: 2 });
  });

  it('summarises an empty series as zeroes, not NaN', () => {
    expect(summarizeMetric([])).toEqual({ avg: 0, current: 0, max: 0, min: 0 });
  });
});

describe('markers — the deploy line', () => {
  const first = Date.parse('2026-09-24T09:00:00Z');
  const last = Date.parse('2026-09-24T10:00:00Z');

  it('keeps markers that fall inside the window', () => {
    const inside = { at: Date.parse('2026-09-24T09:30:00Z'), label: 'Deployed' };
    expect(markersInWindow([inside], first, last)).toEqual([inside]);
  });

  it('drops markers outside the window, on either side', () => {
    const before = { at: first - 1, label: 'Deployed' };
    const after = { at: last + 1, label: 'Deployed' };
    expect(markersInWindow([before, after], first, last)).toEqual([]);
  });
});

describe('paths', () => {
  const x = (at: number) => at;
  const y = (value: number) => 100 - value;

  it('draws the polyline through every point', () => {
    expect(linePathFor(points([10, 20], 5, 1), x, y)).toBe('M5.0,90.0 L6.0,80.0');
  });

  it('draws a carried-forward series as the step it is: hold, then jump', () => {
    // Three samples of 10, then two of 30 — a cached reading refreshed once.
    const path = stepLinePathFor(points([10, 10, 10, 30, 30], 0, 1), x, y);
    expect(path).toBe('M0.0,90.0 L1.0,90.0 L1.0,90.0 L2.0,90.0 L2.0,90.0 L3.0,90.0 L3.0,70.0 L4.0,70.0 L4.0,70.0');
  });

  it('emits an empty path for no points', () => {
    expect(stepLinePathFor([], x, y)).toBe('');
  });
});
