import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Col, Flex, Row, Segmented, Skeleton, Statistic } from 'antd';
import type { ReactNode } from 'react';
import { useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import type { RequestMetrics, RequestWindow } from '../data/types';
import {
  DEFAULT_REQUEST_WINDOW,
  latencyHeader,
  statusHeader,
  latencySeries,
  statusSeries,
  summaryItems,
  windowsFor,
} from '../pages/request-metrics-view';
import { MetricChart } from './MetricChart';

/**
 * Request metrics for one scope (ADR-0035): a window picker, the summary row,
 * and the Status and Latency charts. The three views (Monitoring › Requests,
 * Monitoring › Panel, a Compose's Monitor tab) differ only in `load`, so they
 * share this. `extra` sits next to the picker: a Settings button, a host
 * selector.
 */
export function RequestMetricsPanel({
  extra,
  load,
  swrKey,
}: {
  swrKey: string;
  load: (window: RequestWindow) => Promise<RequestMetrics>;
  extra?: ReactNode;
}) {
  const [picked, setPicked] = useState<RequestWindow>(DEFAULT_REQUEST_WINDOW);
  // The retention comes with each answer, so remember the last one: a window
  // the retention no longer covers falls back to the default instead of 400ing.
  const [retentionDays, setRetentionDays] = useState(1);
  const offered = windowsFor(retentionDays);
  const window = offered.includes(picked) ? picked : DEFAULT_REQUEST_WINDOW;

  const { data, error, isValidating, mutate } = useSWR(
    [swrKey, window],
    async () => {
      const result = await load(window);
      setRetentionDays(result.retentionDays);
      return result;
    },
    { keepPreviousData: false, refreshInterval: 60_000 },
  );

  // A different window is a different query: never draw the old one's buckets
  // under the new caption.
  const ready = data?.window === window ? data : undefined;

  return (
    <>
      <Flex align="center" gap={12} style={{ marginBottom: 16 }} wrap>
        <Segmented
          onChange={(value) => setPicked(value as RequestWindow)}
          options={offered.map((value) => ({ label: value, value }))}
          value={window}
        />
        <Button icon={<ReloadOutlined />} loading={isValidating} onClick={() => void mutate()}>
          Refresh
        </Button>
        {extra}
      </Flex>

      {error && (
        <Alert
          action={<Button onClick={() => void mutate()}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load request metrics'}
          type="error"
        />
      )}

      {ready && !ready.accessLogEnabled ? (
        <Alert
          description={
            <>
              Turn it on in <Link to={paths.traefik}>Traefik</Link> › Settings to collect request metrics.
            </>
          }
          showIcon
          title="The Access log is off."
          type="info"
        />
      ) : (
        <>
          {ready?.accessLogFiltered && (
            <Alert
              showIcon
              style={{ marginBottom: 16 }}
              title="The Access log is filtered, so these numbers cover only the requests it logs."
              type="warning"
            />
          )}
          {ready ? <Charts metrics={ready} /> : !error && <Loading />}
        </>
      )}
    </>
  );
}

function Charts({ metrics }: { metrics: RequestMetrics }) {
  const status = statusSeries(metrics);
  const latency = latencySeries(metrics);
  return (
    <>
      <Card size="small" style={{ marginBottom: 16 }} variant="outlined">
        <Row gutter={[16, 16]}>
          {summaryItems(metrics).map((item) => (
            <Col key={item.label} md={{ flex: '1 1 0' }} xs={12}>
              <Statistic title={item.label} value={item.value} />
            </Col>
          ))}
        </Row>
      </Card>
      <Row gutter={[16, 16]}>
        <Col lg={12} xs={24}>
          <MetricChart
            axis="auto"
            series={status}
            summary={statusHeader(status)}
            title="Status"
            unit="requestsPerMinute"
          />
        </Col>
        <Col lg={12} xs={24}>
          <MetricChart
            axis="auto"
            series={latency}
            summary={latencyHeader(latency)}
            title="Latency"
            unit="milliseconds"
          />
        </Col>
      </Row>
    </>
  );
}

function Loading() {
  return (
    <Row gutter={[16, 16]}>
      {[0, 1].map((index) => (
        <Col key={index} lg={12} xs={24}>
          <Card variant="outlined">
            <Skeleton active paragraph={{ rows: 3 }} title={{ width: '40%' }} />
          </Card>
        </Col>
      ))}
    </Row>
  );
}
