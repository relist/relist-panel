import { ReloadOutlined, RocketOutlined } from '@ant-design/icons';
import { Alert, Badge, Button, Card, Col, Descriptions, Flex, Row, Segmented, Select, Skeleton, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { CODE_FONT_STACK, SERVICE_STATE_META } from '../app/theme';
import { usePanel } from '../data/store';
import type { Compose, MetricWindow, ServiceMetrics } from '../data/types';
import { shortContainerId } from '../data/types';
import { MetricChart } from './MetricChart';
import { latestPids, serviceChartSpecs } from '../pages/monitor-view';
import { CodeValue, EmptyState, HealthBadge, RelativeTime, ServiceStateTag } from './primitives';

const { Text } = Typography;

const WINDOWS: MetricWindow[] = ['15m', '1h', '6h', '24h'];

/**
 * The Compose screen's Monitor tab (GUIDELINES §4.6), the port of the
 * prototype's ServiceMonitor onto the Services endpoint. The history belongs
 * to the service name, not to the container: a deploy mints a new container
 * ID, and metrics keyed by container would restart from zero on every deploy —
 * exactly when the operator wants to see what the deploy did. The deploy
 * marker reads the API's `lastDeployedAt` (written on deploy success), so it
 * survives a reload and appears for deploys made before this session.
 */
export function ServiceMonitor({ compose, onOpenEditor }: { compose: Compose; onOpenEditor: () => void }) {
  const { isPending, loadServiceMetrics, loadServices, services: servicesByCompose } = usePanel();
  const [pickedService, setPickedService] = useState<string>();
  const [metricWindow, setMetricWindow] = useState<MetricWindow>('1h');
  const [error, setError] = useState<string>();
  const [servicesReady, setServicesReady] = useState(false);
  const [result, setResult] = useState<{ samples: ServiceMetrics; service: string; window: MetricWindow }>();

  // The rail reads the same rows; whoever loads first wins, and a refetch is
  // one cache-fresh GET for each.
  const services = servicesByCompose[compose.id] ?? [];

  const fetchServices = useCallback(
    (composeId: number) =>
      loadServices(composeId)
        .catch((reason: unknown) => {
          setError(reason instanceof Error ? reason.message : 'Could not load services');
        })
        .finally(() => setServicesReady(true)),
    [loadServices],
  );

  useEffect(() => {
    setPickedService(undefined);
    setServicesReady(false);
    void fetchServices(compose.id);
  }, [compose.id, fetchServices]);

  // The selection is a name, not an index: after a deploy the services array is
  // rebuilt, and an index would point at whichever service happened to move.
  const service = services.find((candidate) => candidate.name === pickedService) ?? services[0];

  const load = useCallback(
    async (name: string, next: MetricWindow) => {
      setError(undefined);
      try {
        setResult({ samples: await loadServiceMetrics(compose.id, name, next), service: name, window: next });
      } catch (reason: unknown) {
        setError(reason instanceof Error ? reason.message : 'Could not load metrics');
      }
    },
    [compose.id, loadServiceMetrics],
  );

  useEffect(() => {
    if (service) void load(service.name, metricWindow);
  }, [load, metricWindow, service?.name]);

  if (!servicesReady && services.length === 0) {
    return (
      <Card variant="outlined">
        <Skeleton active paragraph={{ rows: 6 }} title={{ width: '30%' }} />
      </Card>
    );
  }

  // "Never deployed" and "fully stopped" both arrive as an empty Services list
  // — the collector only caches containers it can see. Either way, deploying
  // is what starts collection, so both get the same empty state.
  if (!service) {
    return (
      <Card variant="outlined">
        <EmptyState
          action={
            <Button icon={<RocketOutlined />} onClick={onOpenEditor}>
              Open the Editor tab
            </Button>
          }
          description="Per-service metrics begin with the first deploy — the panel reads the containers a Compose created, and a Compose with no containers on the host has none. Deploying starts collection."
          title="Nothing to monitor yet"
        />
      </Card>
    );
  }

  // A different window or service is a different query: until it answers, the
  // previous samples would be drawn against the new caption.
  const ready = result?.service === service.name && result?.window === metricWindow;
  const samples = ready ? result.samples : undefined;
  // The marker is the API's own deploy time — null only for a Compose that
  // was never deployed (or was, before the panel began recording), in which
  // case there is nothing honest to mark.
  const deployedStamp = compose.lastDeployedAt;
  const markers = deployedStamp ? [{ at: Date.parse(deployedStamp), label: 'Deployed' }] : [];
  const charts = samples ? serviceChartSpecs(samples, metricWindow, markers) : undefined;
  const pids = samples ? latestPids(samples.samples) : undefined;
  const loading = isPending(`service-metrics:${compose.id}:${service.name}:${metricWindow}`);

  return (
    <>
      <Flex align="center" gap={16} style={{ marginBottom: 16 }} wrap>
        <Flex align="center" gap={12}>
          <Text type="secondary">Service</Text>
          <Select
            aria-label="Service"
            onChange={setPickedService}
            options={services.map((entry) => ({
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
        <Button
          icon={<ReloadOutlined />}
          loading={loading}
          onClick={() => {
            void load(service.name, metricWindow);
            void fetchServices(compose.id);
          }}
        >
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
            {deployedStamp ? (
              <>
                Deployed <RelativeTime value={deployedStamp} />
              </>
            ) : (
              'Last deploy not recorded'
            )}
          </Text>
        </Flex>
        <Descriptions
          column={{ lg: 3, sm: 2, xs: 1 }}
          items={[
            { children: <CodeValue copyable value={service.image} />, key: 'image', label: 'Image' },
            {
              children: (
                <CodeValue copyable={Boolean(service.containerId)} value={service.containerId ? shortContainerId(service.containerId) : '—'} />
              ),
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

      {error && (
        <Alert
          action={
            <Button onClick={() => {
              void load(service.name, metricWindow);
            }}>Retry</Button>
          }
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
