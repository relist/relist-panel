import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Row, Segmented, Skeleton, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router';
import { MetricChart } from '../components/MetricChart';
import { PageHeader } from '../components/primitives';
import { usePanel } from '../data/store';
import type { HostMetrics, MetricWindow } from '../data/types';
import { hostChartSpecs } from './monitor-view';

const { Text } = Typography;

const WINDOWS: MetricWindow[] = ['15m', '1h', '6h', '24h'];
const DEFAULT_WINDOW: MetricWindow = '1h';

function parseWindow(value: string | null): MetricWindow {
  return WINDOWS.includes(value as MetricWindow) ? (value as MetricWindow) : DEFAULT_WINDOW;
}

/**
 * Host metrics (GUIDELINES §4.2), the port of the prototype's MonitoringPage
 * onto the real endpoint. The panel reads the host itself — no agent, no
 * sidecar — so this screen also has to be honest about what it cannot read: a
 * source that is not readable renders as such with the endpoint's own reason,
 * and a window the panel has not fully collected yet says so. The window lives
 * in the URL's `?window=` so a chart is linkable; the design's `host.name`
 * does not port (no endpoint for host info — GUIDELINES §11).
 */
export function MonitoringPage() {
  const { isPending, loadMetrics } = usePanel();
  const [searchParams, setSearchParams] = useSearchParams();
  const metricWindow = parseWindow(searchParams.get('window'));
  const [error, setError] = useState<string>();
  const [result, setResult] = useState<{ samples: HostMetrics; window: MetricWindow }>();

  const load = useCallback(
    async (next: MetricWindow) => {
      setError(undefined);
      try {
        setResult({ samples: await loadMetrics(next), window: next });
      } catch (reason: unknown) {
        // Errors stay on the screen as an Alert with a Retry (GUIDELINES §8) —
        // a vanished toast is read by nobody.
        setError(reason instanceof Error ? reason.message : 'Could not load host metrics');
      }
    },
    [loadMetrics],
  );

  useEffect(() => {
    void load(metricWindow);
  }, [load, metricWindow]);

  const setWindow = (next: MetricWindow) => {
    setSearchParams(next === DEFAULT_WINDOW ? {} : { window: next });
  };

  // A different window is a different query: until it answers, the previous
  // window's samples would be drawn against the new window's caption.
  const ready = result?.window === metricWindow;
  const charts = ready ? hostChartSpecs(result.samples, metricWindow) : undefined;
  const loading = isPending(`metrics:${metricWindow}`);

  return (
    <>
      <PageHeader
        description="Host metrics — CPU, memory, disk, Docker space and I/O, read by the panel itself."
        extra={
          <>
            <Segmented
              onChange={(value) => setWindow(value as MetricWindow)}
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

      {error && (
        <Alert
          action={<Button onClick={() => void load(metricWindow)}>Retry</Button>}
          title={error}
          showIcon
          style={{ marginBottom: 16 }}
          type="error"
        />
      )}

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
