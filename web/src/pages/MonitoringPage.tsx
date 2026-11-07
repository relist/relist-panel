import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Flex, Row, Segmented, Skeleton, Tabs, Typography } from 'antd';
import { SettingOutlined } from '@ant-design/icons';
import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router';
import { MetricChart } from '../components/MetricChart';
import { PageHeader } from '../components/primitives';
import { RequestMetricsPanel } from '../components/RequestMetricsPanel';
import { RequestMetricsSettingsDrawer } from '../components/RequestMetricsSettingsDrawer';
import { api } from '../data/api';
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
function HostMonitoring() {
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
      <Flex align="center" gap={12} style={{ marginBottom: 16 }} wrap>
        <Segmented
          onChange={(value) => setWindow(value as MetricWindow)}
          options={WINDOWS.map((value) => ({ label: value, value }))}
          value={metricWindow}
        />
        <Button icon={<ReloadOutlined />} loading={loading} onClick={() => void load(metricWindow)}>
          Refresh
        </Button>
      </Flex>

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

type TabKey = 'host' | 'requests' | 'panel';
const TABS: TabKey[] = ['host', 'requests', 'panel'];

/**
 * Monitoring: the host's own charts, and the request metrics the panel reads
 * from the Access log (ADR-0035) for all traffic and for the panel alone. The
 * tab lives in `?tab=` so a view is linkable.
 */
export function MonitoringPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const requested = searchParams.get('tab');
  const tab: TabKey = TABS.includes(requested as TabKey) ? (requested as TabKey) : 'host';
  const [settingsOpen, setSettingsOpen] = useState(false);
  // Saving a retention changes which windows exist, so the Requests view
  // remounts to re-read its retention.
  const [settingsVersion, setSettingsVersion] = useState(0);

  return (
    <>
      <PageHeader
        description="Host metrics and request metrics, read by the panel itself."
        title="Monitoring"
        titleExtra={
          <Text style={{ display: 'block', fontSize: 13, maxWidth: 760 }} type="secondary">
            Host metrics are read every 10 seconds inside the panel process — no agent, no sidecar. One hour is kept at 10s
            in memory; the rest of the day is 1-minute rollups in the panel's own database. Request metrics come from the
            Traefik Access log.
          </Text>
        }
      />
      <Tabs
        activeKey={tab}
        items={[
          { key: 'host', label: 'Host', children: <HostMonitoring /> },
          {
            key: 'requests',
            label: 'Requests',
            children: (
              <RequestMetricsPanel
                extra={
                  <Button icon={<SettingOutlined />} onClick={() => setSettingsOpen(true)}>
                    Settings
                  </Button>
                }
                key={settingsVersion}
                load={(window) => api.requestMetrics(window)}
                swrKey="/api/requests/metrics"
              />
            ),
          },
          {
            key: 'panel',
            label: 'Panel',
            children: <RequestMetricsPanel load={(window) => api.panelRequestMetrics(window)} swrKey="/api/requests/panel/metrics" />,
          },
        ]}
        onChange={(key) => setSearchParams(key === 'host' ? {} : { tab: key })}
      />
      <RequestMetricsSettingsDrawer
        onClose={() => setSettingsOpen(false)}
        onSaved={() => setSettingsVersion((value) => value + 1)}
        open={settingsOpen}
      />
    </>
  );
}
