import {
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  LoadingOutlined,
  MinusCircleFilled,
} from '@ant-design/icons';
import { Button, Card, Empty, Flex, Grid, Table, Tag, Typography } from 'antd';
import dayjs from 'dayjs';
import { useEffect, useRef, useState } from 'react';

import type { ComposePath } from '../data/api';
import type { ActiveDeployment } from '../data/deployments-view';
import type { Compose, Deployment, DeploymentStatus } from '../data/types';
import { ComposeBuilderSelect } from './ComposeBuilderSelect';
import { RelativeTime } from './primitives';
import { DeploymentLogDrawer } from './DeploymentLogDrawer';

const { Text } = Typography;

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
  return (
    <>
      <ComposeBuilderSelect location={location} yaml={compose.yamlContent} />
      <DeploymentHistory key={`${location.projectId}/${location.environmentId}/${compose.id}`} {...props} />
    </>
  );
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
      <DeploymentLogDrawer
        target={active && opened !== undefined && shown ? { location, number: opened, deployment: shown } : undefined}
        onClose={() => setOpened(undefined)}
        running={running}
      />
    </>
  );
}
