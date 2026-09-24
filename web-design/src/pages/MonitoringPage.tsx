import { ReloadOutlined } from '@ant-design/icons';
import { Button, Card, Col, Row, Segmented, Skeleton, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { MetricChart, summarizeMetric } from '../components/MetricChart';
import type { MetricChartState, MetricPoint, MetricSeries, MetricSummary, MetricUnit } from '../components/MetricChart';
import { PageHeader } from '../components/primitives';
import { usePanel } from '../data/store';
import type { MetricSample, MetricWindow } from '../data/types';
import { METRIC_WINDOWS } from '../data/types';

const { Text } = Typography;

/**
 * Design default, not a backend contract: below 85% a host has room, above it
 * the reading is worth the warning tone. Only the current value is coloured —
 * a red chart is alarm wallpaper.
 */
const PRESSURE_THRESHOLD = 85;

const WINDOWS: MetricWindow[] = ['15m', '1h', '6h', '24h'];

type ChartSpec = {
  title: string;
  unit: MetricUnit;
  series: MetricSeries[];
  summary: MetricSummary;
  state: MetricChartState;
  warn?: boolean;
  note?: string;
  absolute?: { only?: boolean; total: number; used: number };
  collecting?: { haveMinutes: number; ofMinutes: number };
};

function percent(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : 0;
}

/**
 * Six charts out of one sample array. The window decides how much history
 * exists — a panel restarted three hours ago simply has no 24-hour line, and
 * the charts say so instead of drawing a flat one.
 */
function chartsFromSamples(samples: MetricSample[], metricWindow: MetricWindow): ChartSpec[] {
  const meta = METRIC_WINDOWS[metricWindow];
  const last = samples[samples.length - 1];
  const ofMinutes = meta.windowSeconds / 60;
  const haveMinutes = Math.round((samples.length * meta.intervalSeconds) / 60);
  const state: MetricChartState = haveMinutes < ofMinutes ? 'partial' : 'ok';
  const collecting = state === 'partial' ? { haveMinutes, ofMinutes } : undefined;

  const points = (pick: (sample: MetricSample) => number): MetricPoint[] =>
    samples.map((sample) => ({ at: sample.at, value: pick(sample) }));

  const cpu = points((sample) => sample.cpu);
  const memory = points((sample) => percent(sample.memUsed, sample.memTotal));
  const disk = points((sample) => percent(sample.diskUsed, sample.diskTotal));
  const docker = points((sample) => percent(sample.dockerUsed, sample.dockerTotal));
  const blkRead = points((sample) => sample.blkRead);
  const blkWrite = points((sample) => sample.blkWrite);
  const netRx = points((sample) => sample.netRx);
  const netTx = points((sample) => sample.netTx);
  // Two lines, one headline number: the footer summarises the total throughput.
  const io = samples.map((sample) => ({ at: sample.at, value: sample.blkRead + sample.blkWrite }));
  const net = samples.map((sample) => ({ at: sample.at, value: sample.netRx + sample.netTx }));

  const charts: Omit<ChartSpec, 'collecting' | 'state'>[] = [
    {
      series: [{ key: 'cpu', label: 'CPU', points: cpu }],
      summary: summarizeMetric(cpu),
      title: 'CPU Usage',
      unit: 'percent',
      warn: last.cpu >= PRESSURE_THRESHOLD,
    },
    {
      absolute: { total: last.memTotal, used: last.memUsed },
      series: [{ key: 'memory', label: 'Memory', points: memory }],
      summary: summarizeMetric(memory),
      title: 'Memory Usage',
      unit: 'percent',
      warn: percent(last.memUsed, last.memTotal) >= PRESSURE_THRESHOLD,
    },
    {
      absolute: { only: true, total: last.diskTotal, used: last.diskUsed },
      series: [{ key: 'disk', label: 'Disk', points: disk }],
      summary: summarizeMetric(disk),
      title: 'Disk Space',
      unit: 'percent',
      warn: percent(last.diskUsed, last.diskTotal) >= PRESSURE_THRESHOLD,
    },
    {
      absolute: { only: true, total: last.dockerTotal, used: last.dockerUsed },
      note: 'Images, volumes and build cache — read with docker system df and cached, refreshed every few minutes rather than on every sample.',
      series: [{ key: 'docker', label: 'Docker', points: docker }],
      summary: summarizeMetric(docker),
      title: 'Docker Disk Usage',
      unit: 'percent',
    },
    {
      series: [
        { key: 'blk-read', label: 'Read', points: blkRead },
        { key: 'blk-write', label: 'Write', points: blkWrite },
      ],
      summary: summarizeMetric(io),
      title: 'Block I/O',
      unit: 'bytesPerSecond',
    },
    {
      series: [
        { key: 'net-rx', label: 'Received', points: netRx },
        { key: 'net-tx', label: 'Sent', points: netTx },
      ],
      summary: summarizeMetric(net),
      title: 'Network I/O',
      unit: 'bytesPerSecond',
    },
  ];

  return charts.map((chart) => ({ ...chart, collecting, state }));
}

/**
 * Host metrics. The panel reads them itself — no agent, no sidecar — so this
 * screen also has to be honest about what it cannot read: a source that is not
 * readable renders as such, and container-scoped counters never pass for host
 * numbers.
 */
export function MonitoringPage() {
  const { host, loadMetrics } = usePanel();
  const [metricWindow, setMetricWindow] = useState<MetricWindow>('1h');
  const [loading, setLoading] = useState(true);
  const [result, setResult] = useState<{ samples: MetricSample[]; window: MetricWindow }>();

  const load = useCallback(
    async (next: MetricWindow) => {
      setLoading(true);
      try {
        setResult({ samples: await loadMetrics(next), window: next });
      } finally {
        setLoading(false);
      }
    },
    [loadMetrics],
  );

  useEffect(() => {
    void load(metricWindow);
  }, [load, metricWindow]);

  // A different window is a different query: until it answers, the previous
  // window's samples would be drawn against the new window's caption.
  const ready = result?.window === metricWindow;
  const charts = ready ? chartsFromSamples(result.samples, metricWindow) : undefined;

  return (
    <>
      <PageHeader
        description={`Host metrics for ${host.name} — CPU, memory, disk, Docker space and I/O, read by the panel itself.`}
        extra={
          <>
            <Segmented
              onChange={(value) => setMetricWindow(value as MetricWindow)}
              options={WINDOWS.map((value) => ({ label: value, value }))}
              value={metricWindow}
            />
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load(metricWindow)}>
              Refresh
            </Button>
          </>
        }
        title="Monitoring"
        titleExtra={
          <Text style={{ display: 'block', fontSize: 13, maxWidth: 760 }} type="secondary">
            Read from the host every 10 seconds inside the panel process — no agent, no sidecar. One hour is kept at 10s in
            memory; the rest of the day is 1-minute rollups in the panel's own database, so a restart costs the current
            hour's detail rather than the day.
          </Text>
        }
      />

      <Row gutter={[16, 16]}>
        {charts
          ? charts.map((chart) => (
              <Col key={chart.title} lg={12} xs={24}>
                <MetricChart {...chart} />
              </Col>
            ))
          : Array.from({ length: 6 }, (_, index) => (
              <Col key={index} lg={12} xs={24}>
                <Card variant="outlined">
                  <Skeleton active paragraph={{ rows: 3 }} title={{ width: '40%' }} />
                </Card>
              </Col>
            ))}
      </Row>
    </>
  );
}
