import {
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  LoadingOutlined,
  MinusCircleFilled,
} from '@ant-design/icons';
import { Alert, App, Button, Card, Drawer, Empty, Flex, Grid, Steps, Table, Tag, Typography, theme } from 'antd';
import dayjs from 'dayjs';
import { useEffect, useRef, useState } from 'react';

import { LAYOUT } from '../app/theme';
import { api } from '../data/api';
import type { ComposePath } from '../data/api';
import { deploymentHeadline, isActive, stepLabel } from '../data/deployments-view';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Compose, Deployment, DeploymentStatus } from '../data/types';
import { RelativeTime } from './primitives';

const { Text } = Typography;

/** How often the log is read while the shown Deployment is active. */
const POLL_MS = 1000;

const STATUS_ICON: Record<DeploymentStatus, React.ReactNode> = {
  canceled: <MinusCircleFilled />,
  failed: <CloseCircleFilled style={{ color: 'var(--ant-color-error)' }} />,
  queued: <ClockCircleOutlined />,
  running: <LoadingOutlined />,
  succeeded: <CheckCircleFilled style={{ color: 'var(--ant-color-success)' }} />,
};

function duration(d: Deployment): string | undefined {
  if (!d.startedAt || !d.finishedAt) {
    return undefined;
  }
  const seconds = Math.max(0, dayjs(d.finishedAt).diff(dayjs(d.startedAt), 'second'));
  return seconds >= 60 ? `${Math.floor(seconds / 60)}m ${seconds % 60}s` : `${seconds}s`;
}

/** Steps for one Deployment: which one is current, and how a finished one ended. */
function stepsView(d: Deployment): { current: number; status: 'process' | 'finish' | 'error' | 'wait' } {
  if (d.status === 'succeeded') {
    return { current: d.steps.length, status: 'finish' };
  }
  if (d.status === 'failed' || d.status === 'canceled') {
    const failedAt = d.steps.indexOf(d.failedStep || d.step);
    return { current: Math.max(failedAt, 0), status: 'error' };
  }
  if (d.status === 'queued') {
    return { current: 0, status: 'wait' };
  }
  return { current: Math.max(d.steps.indexOf(d.step), 0), status: 'process' };
}

type DeploymentsProps = {
  active?: boolean;
  compose: Compose;
  /** Newest first; the page owns history polling. */
  history?: Deployment[];
  location: ComposePath;
  running?: ActiveDeployment;
  onSelect: (number: number) => void;
  /** Open this Deployment after a Deploy action. */
  selected?: number;
};

/** History stays on the page; logs are fetched only while its Drawer is open. */
export function ComposeDeploymentsTab(props: DeploymentsProps) {
  const { location, compose } = props;
  return <DeploymentHistory key={`${location.projectId}/${location.environmentId}/${compose.id}`} {...props} />;
}

const STATUS_COLOR: Record<DeploymentStatus, string> = {
  canceled: 'default', failed: 'error', queued: 'default', running: 'processing', succeeded: 'success',
};

function sourceLabel(d: Deployment): string {
  const ref = /^[a-f\d]{40,64}$/i.test(d.sourceRef) ? d.sourceRef.slice(0, 7) : d.sourceRef;
  return [ref, d.sourceSubject].filter(Boolean).join(' · ');
}

function DeploymentHistory({ active = true, history, location, onSelect, running, selected }: DeploymentsProps) {
  const [opened, setOpened] = useState(active ? selected : undefined);
  const lastSelected = useRef<number | undefined>(undefined);
  const screens = Grid.useBreakpoint();
  useEffect(() => {
    if (!active) {
      setOpened(undefined);
    } else if (selected !== lastSelected.current) {
      setOpened(selected);
      lastSelected.current = selected;
    }
  }, [active, selected]);
  const shown = history?.find((d) => d.number === opened);
  const open = (number: number) => {
    setOpened(number);
    onSelect(number);
  };

  if (history?.length === 0) {
    return <Empty description="No deployments yet. Deploy this Compose to see its steps and log here." image={Empty.PRESENTED_IMAGE_SIMPLE} />;
  }

  return (
    <>
      <Card size="small" title="History" styles={{ body: { padding: 0, minWidth: 0 } }}>
        <Table<Deployment>
          dataSource={history}
          loading={history === undefined}
          pagination={false}
          rowKey="number"
          size="small"
          tableLayout="fixed"
          columns={[
            {
              title: 'Deployment', key: 'number', width: screens.sm ? 112 : 88,
              render: (_, d) => (
                <Flex vertical style={{ minWidth: 0 }}>
                  <Text strong>#{d.number}</Text>
                  {!screens.sm && <Text ellipsis={{ tooltip: sourceLabel(d) }} type="secondary" style={{ maxWidth: '100%', fontSize: 12 }}>{sourceLabel(d)}</Text>}
                </Flex>
              ),
            },
            {
              title: 'Status', key: 'status', width: screens.sm ? 136 : 108,
              render: (_, d) => <Tag color={STATUS_COLOR[d.status]} icon={STATUS_ICON[d.status]}>{d.status}</Tag>,
            },
            {
              title: 'Source', key: 'source', responsive: ['sm'], ellipsis: true,
              render: (_, d) => <Text ellipsis={{ tooltip: [d.sourceRef, d.sourceSubject].filter(Boolean).join(' · ') }} style={{ display: 'block' }} type="secondary">{sourceLabel(d) || '—'}</Text>,
            },
            { title: 'Started', key: 'time', width: 148, responsive: ['md'], render: (_, d) => <RelativeTime value={d.startedAt || d.queuedAt} /> },
            { title: 'Duration', key: 'duration', width: 96, responsive: ['lg'], render: (_, d) => duration(d) || '—' },
            {
              title: '', key: 'logs', width: screens.sm ? 104 : 72,
              render: (_, d) => <Button aria-label={`View logs for deployment #${d.number}`} onClick={() => open(d.number)} size="small">{screens.sm ? 'View logs' : 'Logs'}</Button>,
            },
          ]}
        />
      </Card>
      <Drawer
        destroyOnHidden
        onClose={() => setOpened(undefined)}
        open={active && opened !== undefined}
        placement="right"
        size="min(880px, 100vw)"
        title={`Deployment #${opened ?? ''}`}
        styles={{ body: { display: 'flex', flexDirection: 'column', minWidth: 0, overflow: 'hidden' } }}
      >
        {active && opened !== undefined && shown ? <DeploymentDetails key={shown.number} deployment={shown} location={location} running={running} /> : <Empty description="Loading deployment…" />}
      </Drawer>
    </>
  );
}

/** Mounting a new selection resets its log and isolates late responses. */
function DeploymentDetails({ deployment: shown, location, running }: {
  deployment: Deployment;
  location: ComposePath;
  running?: ActiveDeployment;
}) {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [log, setLog] = useState('');
  const [logError, setLogError] = useState(false);
  const [canceling, setCanceling] = useState(false);
  const logRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let offset = 0;
    const read = async () => {
      let again = true;
      try {
        const chunk = await api.deploymentLog(location, shown.number, offset);
        if (stopped) return;
        offset = chunk.offset;
        setLogError(false);
        if (chunk.text) setLog((text) => text + chunk.text);
        again = !chunk.done;
      } catch {
        if (!stopped) setLogError(true);
      }
      if (!stopped && again) timer = setTimeout(() => void read(), POLL_MS);
    };
    void read();
    return () => { stopped = true; clearTimeout(timer); };
  }, [location.composeId, location.environmentId, location.projectId, shown.number]);

  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [log]);

  const cancel = async () => {
    setCanceling(true);
    try {
      await api.cancelDeployment(location, shown.number);
    } catch (reason: unknown) {
      const text = reason instanceof Error ? reason.message : 'Could not cancel';
      if ((reason as { status?: number } | null)?.status === 409) message.info(text);
      else message.error(text);
    } finally {
      setCanceling(false);
    }
  };
  const view = stepsView(shown);

  return (
    <Flex gap={LAYOUT.gutterSmall} vertical style={{ flex: 1, minHeight: 0, minWidth: 0 }}>
      <Flex gap={12} vertical style={{ maxHeight: '45%', flexShrink: 0, overflow: 'auto', overflowWrap: 'anywhere' }}>
        <Flex align="start" gap={12} justify="space-between">
          <Text strong style={{ minWidth: 0 }}>{deploymentHeadline(shown, running)}</Text>
          {isActive(shown) && <Button danger loading={canceling} onClick={() => void cancel()} size="small">Cancel</Button>}
        </Flex>
        {(shown.sourceRef || shown.sourceSubject) && <Text type="secondary">{[shown.sourceRef, shown.sourceSubject].filter(Boolean).join(' · ')}</Text>}
        <Steps current={view.current} items={shown.steps.map((id) => ({ title: stepLabel(id) }))} orientation="vertical" size="small" status={view.status} />
        {shown.warnings.length > 0 && <Alert showIcon title={<Flex gap={4} vertical>{shown.warnings.map((warning) => <span key={warning}>{warning}</span>)}</Flex>} type="warning" />}
        {shown.status === 'failed' && shown.reason && <Alert showIcon title={shown.reason} type="error" />}
      </Flex>
      {logError && <Alert showIcon title="Could not load logs. Retrying…" type="warning" />}
      <pre
        className="rp-logs"
        data-testid="deployment-log"
        ref={logRef}
        style={{
          background: token.colorFillQuaternary,
          border: `1px solid ${token.colorBorderSecondary}`,
          borderRadius: token.borderRadius,
          flex: 1,
          margin: 0,
          minHeight: 0,
          minWidth: 0,
          overflow: 'auto',
          overflowWrap: 'anywhere',
        }}
      >{log}</pre>
    </Flex>
  );
}
