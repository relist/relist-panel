import type {
  MetricAxis,
  MetricChartState,
  MetricMarker,
  MetricPoint,
  MetricSeries,
  MetricSummary,
  MetricUnit,
} from '../components/MetricChart';
import { summarizeMetric } from '../components/MetricChart';
import type { HostMetrics, MetricSample, MetricWindow, ServiceMetrics, ServiceSample, SourceStatus } from '../data/types';
import { METRIC_WINDOWS, SERVICE_METRIC_WINDOWS } from '../data/types';

/**
 * Screen decisions for the two monitoring screens, pure so they test in the
 * node env: how the endpoints' envelopes become the charts' props. The screen
 * rules that live here (spec, "Screen rules the implementation must keep"):
 * a source that is not readable renders `unavailable` with its reason and
 * never a zeroed line; an absent reading (JSON null) is never plotted as a
 * zero; a window longer than what the panel has collected says so.
 */

export type ChartSpec = {
  title: string;
  unit: MetricUnit;
  series: MetricSeries[];
  summary: MetricSummary;
  state: MetricChartState;
  /** `auto` ends the axis above the data — for a percent metric that can pass 100. */
  axis?: MetricAxis;
  /** Vertical references (a deploy) the chart draws when they fall inside the window. */
  markers?: MetricMarker[];
  /** The absolute reading behind a percent metric. `only` hides avg/peak/low. */
  absolute?: { only?: boolean; total: number; used: number };
  collecting?: { haveMinutes: number; ofMinutes: number };
  /** A standing line under the chart (the Docker disk cache note). */
  note?: string;
  /** Render the series as the step it is: a cached reading carried forward between refreshes. */
  step?: boolean;
  unavailableReason?: string;
  warn?: boolean;
};

/** Design default: below 85% there is room, above it the reading is worth the warning tone. */
const PRESSURE_THRESHOLD = 85;

/** Whether a number is a real reading: present on the wire and finite. */
function isReading(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function percent(used: number | null, total: number | null): number | null {
  return isReading(used) && isReading(total) && total > 0 ? (used / total) * 100 : null;
}

/**
 * Whether a source can be read at all. The reason is the endpoint's own —
 * `/host/proc not mounted`, `docker system df failed: …` — so the screen never
 * has to guess why. A source missing from the table has not been sampled yet.
 */
export function sourceProblem(sources: Record<string, SourceStatus>, key: string): string | undefined {
  const status = sources[key];
  if (!status) {
    return 'The panel has not sampled this source yet.';
  }
  return status.available ? undefined : status.reason || 'The panel could not read this metric from the host.';
}

/** How much of the window the samples actually cover, in whole minutes. */
export function collectedMinutes(
  sampleCount: number,
  intervalSeconds: number,
  windowSeconds: number,
): { haveMinutes: number; ofMinutes: number; partial: boolean } {
  const ofMinutes = Math.round(windowSeconds / 60);
  const haveMinutes = Math.min(ofMinutes, Math.round((sampleCount * intervalSeconds) / 60));
  return { haveMinutes, ofMinutes, partial: haveMinutes < ofMinutes };
}

type WindowState = {
  state: MetricChartState;
  collecting?: { haveMinutes: number; ofMinutes: number };
};

function coverageState(
  payload: { partial: boolean },
  sampleCount: number,
  intervalSeconds: number,
  windowSeconds: number,
  problem?: string,
): WindowState {
  if (problem !== undefined) {
    return { state: 'unavailable' };
  }
  const coverage = collectedMinutes(sampleCount, intervalSeconds, windowSeconds);
  if (coverage.partial || payload.partial) {
    return { state: 'partial', collecting: { haveMinutes: coverage.haveMinutes, ofMinutes: coverage.ofMinutes } };
  }
  return { state: 'ok' };
}

/** The last honest reading of one field, scanning backwards. */
function latestReading<T>(samples: T[], pick: (sample: T) => number | null): number | undefined {
  for (let index = samples.length - 1; index >= 0; index -= 1) {
    const value = pick(samples[index]);
    if (isReading(value)) return value;
  }
  return undefined;
}

/** Points for one field: absent readings simply do not appear. */
function seriesOf<T extends { at: number }>(samples: T[], pick: (sample: T) => number | null): MetricPoint[] {
  const out: MetricPoint[] = [];
  for (const sample of samples) {
    const value = pick(sample);
    if (isReading(value)) {
      out.push({ at: sample.at, value });
    }
  }
  return out;
}

/**
 * Total throughput per sample, for the one headline number under a two-line
 * chart. A sample needs both readings: a total with a missing part is not a
 * total, and halving it to zero would flatten the summary.
 */
function totalOf<T extends { at: number }>(samples: T[], a: (sample: T) => number | null, b: (sample: T) => number | null): MetricPoint[] {
  const out: MetricPoint[] = [];
  for (const sample of samples) {
    const left = a(sample);
    const right = b(sample);
    if (isReading(left) && isReading(right)) {
      out.push({ at: sample.at, value: left + right });
    }
  }
  return out;
}

/** The host screen's six charts out of one envelope. */
export function hostChartSpecs(payload: HostMetrics, metricWindow: MetricWindow): ChartSpec[] {
  const { samples, intervalSeconds } = payload;
  const { windowSeconds } = METRIC_WINDOWS[metricWindow];

  const cpu = seriesOf(samples, (sample: MetricSample) => sample.cpu);
  const memory = seriesOf(samples, (sample: MetricSample) => percent(sample.memUsed, sample.memTotal));
  const disk = seriesOf(samples, (sample: MetricSample) => percent(sample.diskUsed, sample.diskTotal));
  const docker = seriesOf(samples, (sample: MetricSample) => percent(sample.dockerUsed, sample.dockerTotal));
  const blkRead = seriesOf(samples, (sample: MetricSample) => sample.blkRead);
  const blkWrite = seriesOf(samples, (sample: MetricSample) => sample.blkWrite);
  const netRx = seriesOf(samples, (sample: MetricSample) => sample.netRx);
  const netTx = seriesOf(samples, (sample: MetricSample) => sample.netTx);
  const io = totalOf(samples, (sample: MetricSample) => sample.blkRead, (sample: MetricSample) => sample.blkWrite);
  const net = totalOf(samples, (sample: MetricSample) => sample.netRx, (sample: MetricSample) => sample.netTx);

  const memUsed = latestReading(samples, (sample: MetricSample) => sample.memUsed);
  const memTotal = latestReading(samples, (sample: MetricSample) => sample.memTotal);
  const diskUsed = latestReading(samples, (sample: MetricSample) => sample.diskUsed);
  const diskTotal = latestReading(samples, (sample: MetricSample) => sample.diskTotal);
  const dockerUsed = latestReading(samples, (sample: MetricSample) => sample.dockerUsed);
  const dockerTotal = latestReading(samples, (sample: MetricSample) => sample.dockerTotal);
  const cpuNow = latestReading(samples, (sample: MetricSample) => sample.cpu);

  // The wire names the sources in camelCase (`wireSources` in internal/metrics);
  // each chart names the source it is allowed to draw from, so a container-
  // scoped counter can never pass for a host number.
  const problems = {
    blockIo: sourceProblem(payload.sources, 'blockIo'),
    cpu: sourceProblem(payload.sources, 'cpu'),
    dockerDisk: sourceProblem(payload.sources, 'dockerDisk'),
    diskSpace: sourceProblem(payload.sources, 'diskSpace'),
    memory: sourceProblem(payload.sources, 'memory'),
    network: sourceProblem(payload.sources, 'network'),
  };

  const spec = (
    problem: string | undefined,
    base: Omit<ChartSpec, 'collecting' | 'state' | 'unavailableReason'>,
  ): ChartSpec => {
    const state = coverageState(payload, samples.length, intervalSeconds, windowSeconds, problem);
    return {
      ...base,
      collecting: state.collecting,
      state: state.state,
      unavailableReason: state.state === 'unavailable' ? problem : undefined,
    };
  };

  return [
    spec(problems.cpu, {
      series: [{ key: 'cpu', label: 'CPU', points: cpu }],
      summary: summarizeMetric(cpu),
      title: 'CPU Usage',
      unit: 'percent',
      warn: cpuNow !== undefined && cpuNow >= PRESSURE_THRESHOLD,
    }),
    spec(problems.memory, {
      absolute: memUsed !== undefined && memTotal !== undefined ? { total: memTotal, used: memUsed } : undefined,
      series: [{ key: 'memory', label: 'Memory', points: memory }],
      summary: summarizeMetric(memory),
      title: 'Memory Usage',
      unit: 'percent',
      warn: (() => {
        const reading = percent(memUsed ?? null, memTotal ?? null);
        return reading !== null && reading >= PRESSURE_THRESHOLD;
      })(),
    }),
    spec(problems.diskSpace, {
      absolute: diskUsed !== undefined && diskTotal !== undefined ? { only: true, total: diskTotal, used: diskUsed } : undefined,
      series: [{ key: 'disk', label: 'Disk', points: disk }],
      summary: summarizeMetric(disk),
      title: 'Disk Space',
      unit: 'percent',
      warn: (() => {
        const reading = percent(diskUsed ?? null, diskTotal ?? null);
        return reading !== null && reading >= PRESSURE_THRESHOLD;
      })(),
    }),
    spec(problems.dockerDisk, {
      absolute: dockerUsed !== undefined && dockerTotal !== undefined ? { only: true, total: dockerTotal, used: dockerUsed } : undefined,
      note: 'Images, volumes and build cache — read with docker system df and cached, refreshed every few minutes rather than on every sample.',
      series: [{ key: 'docker', label: 'Docker', points: docker }],
      step: true,
      summary: summarizeMetric(docker),
      title: 'Docker Disk Usage',
      unit: 'percent',
    }),
    spec(problems.blockIo, {
      series: [
        { key: 'blk-read', label: 'Read', points: blkRead },
        { key: 'blk-write', label: 'Write', points: blkWrite },
      ],
      summary: summarizeMetric(io),
      title: 'Block I/O',
      unit: 'bytesPerSecond',
    }),
    spec(problems.network, {
      series: [
        { key: 'net-rx', label: 'Received', points: netRx },
        { key: 'net-tx', label: 'Sent', points: netTx },
      ],
      summary: summarizeMetric(net),
      title: 'Network I/O',
      unit: 'bytesPerSecond',
    }),
  ];
}

/** The Monitor tab's four charts out of one Service envelope. */
export function serviceChartSpecs(
  payload: ServiceMetrics,
  metricWindow: MetricWindow,
  markers: MetricMarker[],
): ChartSpec[] {
  const { samples, intervalSeconds } = payload;
  const { windowSeconds } = SERVICE_METRIC_WINDOWS[metricWindow];

  const cpu = seriesOf(samples, (sample: ServiceSample) => sample.cpu);
  const memory = seriesOf(samples, (sample: ServiceSample) => percent(sample.memUsed, sample.memLimit));
  const netRx = seriesOf(samples, (sample: ServiceSample) => sample.netRx);
  const netTx = seriesOf(samples, (sample: ServiceSample) => sample.netTx);
  const blkRead = seriesOf(samples, (sample: ServiceSample) => sample.blkRead);
  const blkWrite = seriesOf(samples, (sample: ServiceSample) => sample.blkWrite);
  const net = totalOf(samples, (sample: ServiceSample) => sample.netRx, (sample: ServiceSample) => sample.netTx);
  const io = totalOf(samples, (sample: ServiceSample) => sample.blkRead, (sample: ServiceSample) => sample.blkWrite);

  const memUsed = latestReading(samples, (sample: ServiceSample) => sample.memUsed);
  const memLimit = latestReading(samples, (sample: ServiceSample) => sample.memLimit);
  const cpuNow = latestReading(samples, (sample: ServiceSample) => sample.cpu);

  // The Service side has exactly one source: the Docker stats API sweep. When
  // it cannot be read, all four charts say so — a zeroed quad would present an
  // unread service as an idle one.
  const problem = sourceProblem(payload.sources, 'dockerStats');
  const state = coverageState(payload, samples.length, intervalSeconds, windowSeconds, problem);

  const base: Omit<ChartSpec, 'state' | 'collecting' | 'unavailableReason'>[] = [
    {
      // A container is not a host: it may use more than one core, so the axis
      // comes from the data instead of the host chart's fixed 0–100.
      axis: 'auto',
      markers,
      series: [{ key: 'cpu', label: 'CPU', points: cpu }],
      summary: summarizeMetric(cpu),
      title: 'CPU Usage',
      unit: 'percent',
      warn: cpuNow !== undefined && cpuNow >= PRESSURE_THRESHOLD,
    },
    {
      // The limit is the container's own, and `memUsed` is usage minus page
      // cache — cache is the host's memory, not this service's footprint.
      absolute: memUsed !== undefined && memLimit !== undefined ? { total: memLimit, used: memUsed } : undefined,
      markers,
      series: [{ key: 'memory', label: 'Memory', points: memory }],
      summary: summarizeMetric(memory),
      title: 'Memory Usage',
      unit: 'percent',
      warn: (() => {
        const reading = percent(memUsed ?? null, memLimit ?? null);
        return reading !== null && reading >= PRESSURE_THRESHOLD;
      })(),
    },
    {
      markers,
      series: [
        { key: 'net-rx', label: 'Received', points: netRx },
        { key: 'net-tx', label: 'Sent', points: netTx },
      ],
      summary: summarizeMetric(net),
      title: 'Network I/O',
      unit: 'bytesPerSecond',
    },
    {
      markers,
      series: [
        { key: 'blk-read', label: 'Read', points: blkRead },
        { key: 'blk-write', label: 'Write', points: blkWrite },
      ],
      summary: summarizeMetric(io),
      title: 'Block I/O',
      unit: 'bytesPerSecond',
    },
  ];

  return base.map((chart) => ({
    ...chart,
    collecting: state.collecting,
    state: state.state,
    unavailableReason: state.state === 'unavailable' ? problem : undefined,
  }));
}

/** The last sample's PIDs reading, for the facts strip. */
export function latestPids(samples: ServiceSample[]): number | undefined {
  return latestReading(samples, (sample) => sample.pids);
}
