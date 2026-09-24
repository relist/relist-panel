import { describe, expect, it } from 'vitest';

import type { HostMetrics, MetricSample, ServiceMetrics, ServiceSample } from '../data/types';
import { METRIC_WINDOWS, SERVICE_METRIC_WINDOWS } from '../data/types';
import { collectedMinutes, hostChartSpecs, latestPids, serviceChartSpecs, sourceProblem } from './monitor-view';

const HOUR_WINDOW = '1h' as const;

function hostSamples(values: Partial<MetricSample>[]): MetricSample[] {
  return values.map((value, index) => ({
    at: Date.parse('2026-09-24T10:00:00Z') + index * 10_000,
    cpu: 20,
    memUsed: 4 * 1024 ** 3,
    memTotal: 16 * 1024 ** 3,
    diskUsed: 100 * 1024 ** 3,
    diskTotal: 400 * 1024 ** 3,
    dockerUsed: 40 * 1024 ** 3,
    dockerTotal: 400 * 1024 ** 3,
    blkRead: 1_000,
    blkWrite: 2_000,
    netRx: 3_000,
    netTx: 4_000,
    ...value,
  }));
}

function hostEnvelope(overrides: Partial<HostMetrics> & { samples?: MetricSample[] }): HostMetrics {
  return {
    window: HOUR_WINDOW,
    from: 'ring',
    intervalSeconds: 10,
    partial: false,
    sources: {
      blockIo: { available: true },
      cpu: { available: true },
      dockerDisk: { available: true },
      diskSpace: { available: true },
      memory: { available: true },
      network: { available: true },
    },
    samples: hostSamples(Array.from({ length: 360 }, () => ({}))),
    ...overrides,
  };
}

const TITLES = ['CPU Usage', 'Memory Usage', 'Disk Space', 'Docker Disk Usage', 'Block I/O', 'Network I/O'];

function specOf(specs: ReturnType<typeof hostChartSpecs>, title: string) {
  return specs.find((spec) => spec.title === title);
}

describe('host charts', () => {
  it("builds the design's six charts in order", () => {
    const specs = hostChartSpecs(hostEnvelope({}), HOUR_WINDOW);
    expect(specs.map((spec) => spec.title)).toEqual(TITLES);
    expect(specs.every((spec) => spec.state === 'ok')).toBe(true);
  });

  it('keeps the percent charts on the fixed 0–100 axis and the byte rates auto-scaling', () => {
    const specs = hostChartSpecs(hostEnvelope({}), HOUR_WINDOW);
    for (const title of ['CPU Usage', 'Memory Usage', 'Disk Space', 'Docker Disk Usage']) {
      expect(specOf(specs, title)?.unit).toBe('percent');
      // The host charts pass no `axis` — MetricChart defaults to fixed100.
      expect(specOf(specs, title)?.axis).toBeUndefined();
    }
    for (const title of ['Block I/O', 'Network I/O']) {
      expect(specOf(specs, title)?.unit).toBe('bytesPerSecond');
    }
  });

  it('splits I/O into read/write lines and summarises their total', () => {
    const specs = hostChartSpecs(hostEnvelope({}), HOUR_WINDOW);
    expect(specOf(specs, 'Block I/O')?.series.map((series) => series.label)).toEqual(['Read', 'Write']);
    expect(specOf(specs, 'Network I/O')?.series.map((series) => series.label)).toEqual(['Received', 'Sent']);
    expect(specOf(specs, 'Block I/O')?.summary.current).toBe(3_000);
  });

  it('carries the absolute readings: memory shows both, disk and Docker show used-of-total only', () => {
    const specs = hostChartSpecs(hostEnvelope({}), HOUR_WINDOW);
    expect(specOf(specs, 'Memory Usage')?.absolute).toEqual({
      total: 16 * 1024 ** 3,
      used: 4 * 1024 ** 3,
    });
    expect(specOf(specs, 'Disk Space')?.absolute).toEqual({ only: true, total: 400 * 1024 ** 3, used: 100 * 1024 ** 3 });
    expect(specOf(specs, 'Docker Disk Usage')?.absolute).toEqual({
      only: true,
      total: 400 * 1024 ** 3,
      used: 40 * 1024 ** 3,
    });
    expect(specOf(specs, 'CPU Usage')?.absolute).toBeUndefined();
  });

  it('warns from the current reading at the 85% pressure threshold, never from a past peak', () => {
    const envelope = hostEnvelope({
      samples: hostSamples([{ cpu: 10 }, { cpu: 95 }, { cpu: 12 }]),
    });
    expect(specOf(hostChartSpecs(envelope, HOUR_WINDOW), 'CPU Usage')?.warn).toBe(false);

    const hot = hostEnvelope({ samples: hostSamples([{ cpu: 10 }, { cpu: 90 }]) });
    expect(specOf(hostChartSpecs(hot, HOUR_WINDOW), 'CPU Usage')?.warn).toBe(true);
  });

  it('renders the Docker disk series as the step it is', () => {
    expect(specOf(hostChartSpecs(hostEnvelope({}), HOUR_WINDOW), 'Docker Disk Usage')?.step).toBe(true);
    expect(specOf(hostChartSpecs(hostEnvelope({}), HOUR_WINDOW), 'CPU Usage')?.step).toBeUndefined();
  });

  it("marks an unavailable source with the endpoint's own reason and never plots it", () => {
    const envelope = hostEnvelope({
      sources: {
        ...hostEnvelope({}).sources,
        network: { available: false, reason: '/host/proc/1/net/dev is not readable: stat /host/proc/1/net/dev: no such file or directory' },
      },
    });
    const specs = hostChartSpecs(envelope, HOUR_WINDOW);
    const net = specOf(specs, 'Network I/O');
    expect(net?.state).toBe('unavailable');
    expect(net?.unavailableReason).toContain('/host/proc/1/net/dev');
    // The unavailable chart still exists (its card renders the EmptyState);
    // the readable ones are untouched.
    expect(specOf(specs, 'CPU Usage')?.state).toBe('ok');
  });

  it('reports the restart seam: a short window collects, with minutes on the caption', () => {
    // Three minutes of a one-hour window at 10s resolution.
    const envelope = hostEnvelope({ samples: hostSamples(Array.from({ length: 18 }, () => ({}))), partial: true });
    const specs = hostChartSpecs(envelope, HOUR_WINDOW);
    for (const title of TITLES) {
      expect(specOf(specs, title)?.state).toBe('partial');
      expect(specOf(specs, title)?.collecting).toEqual({ haveMinutes: 3, ofMinutes: 60 });
    }
  });

  it('gives a full window no collecting caption', () => {
    const specs = hostChartSpecs(hostEnvelope({}), HOUR_WINDOW);
    expect(specs.every((spec) => spec.collecting === undefined && spec.state === 'ok')).toBe(true);
  });

  it('drops absent readings (JSON null) instead of plotting them as zeroes', () => {
    const envelope = hostEnvelope({
      samples: hostSamples([{ cpu: null }, { cpu: 50 }, { netRx: null }, { netTx: null }]),
    });
    const specs = hostChartSpecs(envelope, HOUR_WINDOW);
    const cpuPoints = specOf(specs, 'CPU Usage')?.series[0].points.map((point) => point.value) ?? [];
    expect(cpuPoints).not.toContain(0);
    expect(cpuPoints[0]).toBe(50);
    // The I/O headline needs both readings; a sample missing either has none.
    expect(specOf(specs, 'Block I/O')?.summary.current).toBe(3_000);
  });
});

describe('service charts', () => {
  function serviceEnvelope(overrides: Partial<ServiceMetrics> & { samples?: ServiceSample[] }): ServiceMetrics {
    return {
      window: HOUR_WINDOW,
      from: 'ring',
      intervalSeconds: 30,
      partial: false,
      sources: { dockerStats: { available: true } },
      samples: [
        { at: 1, cpu: 55, memUsed: 300 * 1024 ** 2, memLimit: 2 * 1024 ** 3, netRx: 100, netTx: 200, blkRead: 10, blkWrite: 20, pids: 5 },
      ],
      ...overrides,
    };
  }

  it('builds the four per-service charts', () => {
    const specs = serviceChartSpecs(serviceEnvelope({}), HOUR_WINDOW, []);
    expect(specs.map((spec) => spec.title)).toEqual(['CPU Usage', 'Memory Usage', 'Network I/O', 'Block I/O']);
  });

  it('puts the container CPU on the auto axis — one container can pass one core', () => {
    const specs = serviceChartSpecs(serviceEnvelope({ samples: [{ at: 1, cpu: 150 } as ServiceSample] }), HOUR_WINDOW, []);
    expect(specs[0].axis).toBe('auto');
  });

  it("reads memory against the container's own limit", () => {
    const specs = serviceChartSpecs(serviceEnvelope({}), HOUR_WINDOW, []);
    expect(specs[1].absolute).toEqual({ total: 2 * 1024 ** 3, used: 300 * 1024 ** 2 });
  });

  it('marks every chart unavailable when the stats sweep cannot be read', () => {
    const envelope = serviceEnvelope({
      sources: { dockerStats: { available: false, reason: 'docker stats: container exited mid-sweep' } },
    });
    const specs = serviceChartSpecs(envelope, HOUR_WINDOW, []);
    expect(specs.every((spec) => spec.state === 'unavailable')).toBe(true);
    expect(specs.every((spec) => spec.unavailableReason?.includes('docker stats'))).toBe(true);
  });

  it('carries the deploy marker to all four charts and caps the caption at the window', () => {
    const marker = { at: Date.parse('2026-09-24T09:58:00Z'), label: 'Deployed' };
    const specs = serviceChartSpecs(serviceEnvelope({}), HOUR_WINDOW, [marker]);
    expect(specs.every((spec) => spec.markers?.[0] === marker)).toBe(true);

    // 40 minutes of a 6h window at 1-minute rollups: the caption says 40 of 360.
    const specs6h = serviceChartSpecs(
      serviceEnvelope({ window: '6h', from: 'rollups', intervalSeconds: 60, partial: true, samples: Array.from({ length: 40 }, (_, index) => ({ at: index, cpu: 1, memUsed: 1, memLimit: 1, netRx: 0, netTx: 0, blkRead: 0, blkWrite: 0, pids: 1 })) }),
      '6h',
      [],
    );
    expect(specs6h.every((spec) => spec.collecting)).toEqual(true);
    expect(specs6h[0].collecting).toEqual({ haveMinutes: 40, ofMinutes: 360 });
  });

  it('exposes the latest PIDs reading for the facts strip', () => {
    const samples: ServiceSample[] = [
      { at: 1, cpu: 1, memUsed: 1, memLimit: 1, netRx: 0, netTx: 0, blkRead: 0, blkWrite: 0, pids: 4 },
      { at: 2, cpu: 1, memUsed: 1, memLimit: 1, netRx: 0, netTx: 0, blkRead: 0, blkWrite: 0, pids: null },
    ];
    expect(latestPids(samples)).toBe(4);
    expect(latestPids([{ at: 1, cpu: 1, memUsed: 1, memLimit: 1, netRx: 0, netTx: 0, blkRead: 0, blkWrite: 0, pids: null }])).toBeUndefined();
  });
});

describe('coverage and sources', () => {
  it("maps the four windows onto the endpoint's resolution table", () => {
    // The endpoint serves the 10s host ring up to an hour and 1-minute
    // rollups beyond; the coverage caption is computed from the same numbers.
    expect(Object.keys(METRIC_WINDOWS)).toEqual(['15m', '1h', '6h', '24h']);
    expect(METRIC_WINDOWS['15m']).toEqual({ intervalSeconds: 10, windowSeconds: 900 });
    expect(METRIC_WINDOWS['1h']).toEqual({ intervalSeconds: 10, windowSeconds: 3_600 });
    expect(METRIC_WINDOWS['6h']).toEqual({ intervalSeconds: 60, windowSeconds: 21_600 });
    expect(METRIC_WINDOWS['24h']).toEqual({ intervalSeconds: 60, windowSeconds: 86_400 });
    expect(Object.keys(SERVICE_METRIC_WINDOWS)).toEqual(['15m', '1h', '6h', '24h']);
    expect(SERVICE_METRIC_WINDOWS['15m'].intervalSeconds).toBe(30);
    expect(SERVICE_METRIC_WINDOWS['1h'].intervalSeconds).toBe(30);
    expect(SERVICE_METRIC_WINDOWS['6h'].intervalSeconds).toBe(60);
    expect(SERVICE_METRIC_WINDOWS['24h'].intervalSeconds).toBe(60);
  });

  it('counts collected minutes from the sample count and resolution', () => {
    expect(collectedMinutes(360, 10, 3_600)).toEqual({ haveMinutes: 60, ofMinutes: 60, partial: false });
    expect(collectedMinutes(90, 10, 900)).toEqual({ haveMinutes: 15, ofMinutes: 15, partial: false });
    expect(collectedMinutes(2, 10, 900)).toEqual({ haveMinutes: 0, ofMinutes: 15, partial: true });
    expect(collectedMinutes(40, 60, 21_600)).toEqual({ haveMinutes: 40, ofMinutes: 360, partial: true });
  });

  it('names an unreadable source, and a source missing from the table', () => {
    expect(sourceProblem({ network: { available: true } }, 'network')).toBeUndefined();
    expect(sourceProblem({ network: { available: false, reason: 'not mounted' } }, 'network')).toBe('not mounted');
    expect(sourceProblem({ network: { available: false } }, 'network')).toBe('The panel could not read this metric from the host.');
    expect(sourceProblem({}, 'network')).toBe('The panel has not sampled this source yet.');
  });
});
