import { ReloadOutlined, RocketOutlined } from '@ant-design/icons';
import { Badge, Button, Card, Col, Descriptions, Flex, Row, Segmented, Select, Skeleton, Typography } from 'antd';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { CODE_FONT_STACK, SERVICE_STATE_META } from '../app/theme';
import { useUiState } from '../app/uiState';
import { publishSceneHandle } from '../app/sceneAdapters';
import { usePanel } from '../data/store';
import type { Compose, MetricWindow, ServiceSample } from '../data/types';
import { SERVICE_METRIC_WINDOWS } from '../data/types';
import { MetricChart, summarizeMetric } from './MetricChart';
import type { MetricAxis, MetricChartState, MetricMarker, MetricPoint, MetricSeries, MetricSummary, MetricUnit } from './MetricChart';
import { CodeValue, EmptyState, HealthBadge, RelativeTime, ServiceStateTag } from './primitives';

const { Text } = Typography;

const WINDOWS: MetricWindow[] = ['15m', '1h', '6h', '24h'];

/**
 * The host screen's design default, applied to a container: at 85% of its
 * memory limit a service is worth the warning tone. Only the current value is
 * coloured — a red chart is alarm wallpaper.
 */
const PRESSURE_THRESHOLD = 85;

type ChartSpec = {
  axis?: MetricAxis;
  absolute?: { total: number; used: number };
  collecting?: { haveMinutes: number; ofMinutes: number };
  markers?: MetricMarker[];
  series: MetricSeries[];
  state: MetricChartState;
  summary: MetricSummary;
  title: string;
  unit: MetricUnit;
  warn?: boolean;
};

function percent(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : 0;
}

/**
 * Four charts out of one service's samples. Coverage decides the state: a
 * window longer than what the panel has read says so, rather than drawing a
 * line that pretends the rest of the window was flat.
 */
function chartsFromSamples(samples: ServiceSample[], metricWindow: MetricWindow, markers: MetricMarker[]): ChartSpec[] {
  const meta = SERVICE_METRIC_WINDOWS[metricWindow];
  const ofMinutes = meta.windowSeconds / 60;
  const haveMinutes = Math.min(ofMinutes, Math.round((samples.length * meta.intervalSeconds) / 60));
  const state: MetricChartState = haveMinutes < ofMinutes ? 'partial' : 'ok';
  const collecting = state === 'partial' ? { haveMinutes, ofMinutes } : undefined;
  const last = samples[samples.length - 1];

  const points = (pick: (sample: ServiceSample) => number): MetricPoint[] =>
    samples.map((sample) => ({ at: sample.at, value: pick(sample) }));

  const cpu = points((sample) => sample.cpu);
  const memory = points((sample) => percent(sample.memUsed, sample.memLimit));
  const netRx = points((sample) => sample.netRx);
  const netTx = points((sample) => sample.netTx);
  const blkRead = points((sample) => sample.blkRead);
  const blkWrite = points((sample) => sample.blkWrite);
  // Two lines, one headline number: the footer summarises the total throughput.
  const net = samples.map((sample) => ({ at: sample.at, value: sample.netRx + sample.netTx }));
  const io = samples.map((sample) => ({ at: sample.at, value: sample.blkRead + sample.blkWrite }));

  const charts: Omit<ChartSpec, 'collecting' | 'state'>[] = [
    {
      // A container is not a host: it may use more than one core, so the axis
      // comes from the data instead of the host chart's fixed 0–100.
      axis: 'auto',
      markers,
      series: [{ key: 'cpu', label: 'CPU', points: cpu }],
      summary: summarizeMetric(cpu),
      title: 'CPU Usage',
      unit: 'percent',
      warn: Boolean(last) && last.cpu >= PRESSURE_THRESHOLD,
    },
    {
      // The limit is the container's own, and `memUsed` is usage minus page
      // cache — cache is the host's memory, not this service's footprint.
      absolute: last ? { total: last.memLimit, used: last.memUsed } : undefined,
      markers,
      series: [{ key: 'memory', label: 'Memory', points: memory }],
      summary: summarizeMetric(memory),
      title: 'Memory Usage',
      unit: 'percent',
      warn: Boolean(last) && percent(last.memUsed, last.memLimit) >= PRESSURE_THRESHOLD,
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

  return charts.map((chart) => ({ ...chart, collecting, state }));
}

/**
 * One service of a Compose, over the same four windows as the host screen. The
 * history belongs to the service name, not to the container: a deploy mints a
 * new container ID, and metrics keyed by container would restart from zero on
 * every deploy — exactly when the operator wants to see what the deploy did.
 */
export function ServiceMonitor({ compose, onOpenEditor }: { compose: Compose; onOpenEditor: () => void }) {
  const { loadServiceMetrics } = usePanel();
  const [pickedService, setPickedService] = useUiState<string | undefined>('monitor.service', undefined);
  const [metricWindow, setMetricWindow] = useUiState<MetricWindow>('monitor.window', '1h');
  // EXCLUDED (ticket 02) — data; store latency. `loading`/`result` are the
  // fetch and its answer, not screen state a `?ui=` string can name. The
  // loading *scene* (`after: 'monitor:loading'`) holds the fetch's own loading
  // state through the recipe published below (same mechanism as
  // MonitoringPage): the pin makes the load's `finally` leave the skeletons up
  // when the fetch answers. Without an `after` name this is today's code path.
  const [loading, setLoading] = useState(true);
  const [result, setResult] = useState<{ samples: ServiceSample[]; service: string; window: MetricWindow }>();

  // A *layout* effect, deliberately: the scoped AfterRunner's passive effect
  // runs before sibling children's passive effects, so a handle published in
  // a plain effect is not in place yet when a board-driven adapter reads it.
  const loadingHold = useRef(false);
  useLayoutEffect(
    () =>
      publishSceneHandle('monitor.loading.hold', () => {
        loadingHold.current = true;
        setLoading(true);
      }),
    [],
  );

  // The selection is a name, not an index: after a deploy the services array is
  // rebuilt, and an index would point at whichever service happened to move.
  const service = compose.services.find((candidate) => candidate.name === pickedService) ?? compose.services[0];
  const deployedAt = compose.lastDeployedAt;

  const load = useCallback(
    async (name: string, next: MetricWindow) => {
      setLoading(true);
      try {
        setResult({ samples: await loadServiceMetrics(compose.id, name, next), service: name, window: next });
      } finally {
        // The scene's hold keeps the skeleton up even after the fetch answers —
        // the frame is reviewed mid-load on purpose.
        if (!loadingHold.current) setLoading(false);
      }
    },
    [compose.id, loadServiceMetrics],
  );

  useEffect(() => {
    if (service) void load(service.name, metricWindow);
  }, [load, metricWindow, service?.name]);

  if (!service || !deployedAt) {
    return (
      <Card variant="outlined">
        <EmptyState
          action={
            <Button icon={<RocketOutlined />} onClick={onOpenEditor}>
              Open the Editor tab
            </Button>
          }
          description="Per-service metrics begin with the first deploy — the panel reads the containers a Compose created, and a Compose that has never run has none."
          title="Nothing to monitor yet"
        />
      </Card>
    );
  }

  // A different window is a different query: until it answers, the previous
  // window's samples would be drawn against the new window's caption.
  const ready = result?.service === service.name && result?.window === metricWindow;
  const samples = ready ? result.samples : undefined;
  const charts = samples ? chartsFromSamples(samples, metricWindow, [{ at: Date.parse(deployedAt), label: 'Deployed' }]) : undefined;
  const pids = samples?.[samples.length - 1]?.pids;

  return (
    <>
      <Flex align="center" gap={16} style={{ marginBottom: 16 }} wrap>
        <Flex align="center" gap={12}>
          <Text type="secondary">Service</Text>
          <Select
            aria-label="Service"
            onChange={setPickedService}
            options={compose.services.map((entry) => ({
              label: (
                <Flex align="center" gap={8}>
                  <Badge status={SERVICE_STATE_META[entry.state].badge} />
                  <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                    {entry.name}
                  </Text>
                  <Text style={{ fontSize: 12 }} type="secondary">
                    {entry.image}
                  </Text>
                </Flex>
              ),
              value: entry.name,
            }))}
            style={{ maxWidth: 420, minWidth: 280 }}
            value={service.name}
          />
        </Flex>
        <Segmented
          onChange={(value) => setMetricWindow(value as MetricWindow)}
          options={WINDOWS.map((value) => ({ label: value, value }))}
          value={metricWindow}
        />
        <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load(service.name, metricWindow)}>
          Refresh
        </Button>
      </Flex>

      <Card style={{ marginBottom: 16 }} styles={{ body: { padding: 16 } }} variant="outlined">
        <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 12 }} wrap>
          <Flex align="center" gap={8}>
            <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 13 }}>
              {service.name}
            </Text>
            <HealthBadge health={service.health} />
            <ServiceStateTag service={service} />
          </Flex>
          <Text type="secondary" style={{ fontSize: 12 }}>
            Deployed <RelativeTime value={deployedAt} />
          </Text>
        </Flex>
        <Descriptions
          column={{ lg: 3, sm: 2, xs: 1 }}
          items={[
            { children: <CodeValue copyable value={service.image} />, key: 'image', label: 'Image' },
            {
              children: <CodeValue copyable={Boolean(service.containerId)} value={service.containerId ?? '—'} />,
              key: 'container',
              label: 'Container',
            },
            {
              children: <Text style={{ fontVariantNumeric: 'tabular-nums' }}>{pids ?? '—'}</Text>,
              key: 'pids',
              label: 'PIDs',
            },
          ]}
          size="small"
        />
        <Text style={{ display: 'block', fontSize: 12, marginTop: 12 }} type="secondary">
          History is keyed by service, not by container: a deploy mints a new container ID, the service name does not change.
          That is what lets these charts survive a redeploy.
        </Text>
      </Card>

      <Row gutter={[16, 16]}>
        {charts
          ? charts.map((chart) => (
              <Col key={chart.title} lg={12} xs={24}>
                <MetricChart {...chart} />
              </Col>
            ))
          : Array.from({ length: 4 }, (_, index) => (
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
