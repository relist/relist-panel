import { ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, DatePicker, Flex, Grid, Select, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import dayjs from 'dayjs';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { DeploymentLogDrawer } from '../components/DeploymentLogDrawer';
import type { DeploymentLogTarget } from '../components/DeploymentLogDrawer';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import { deploymentHeadline, isActive, stepLabel } from '../data/deployments-view';
import { usePanel } from '../data/store';
import type { DeploymentContext, DeploymentHistoryQuery, DeploymentStatus, GlobalDeployment, GlobalDeploymentPage, GlobalQueuedDeployment } from '../data/types';
import { useGlobalDeploymentQueue } from '../data/useGlobalDeploymentQueue';

const { Text } = Typography;
const DEFAULT_QUERY: DeploymentHistoryQuery = { page: 1, pageSize: 20 };
const STATUS_COLOR: Record<DeploymentStatus, string> = {
  queued: 'default', running: 'processing', succeeded: 'success', failed: 'error', canceled: 'default',
};
const STATUS_OPTIONS = (Object.keys(STATUS_COLOR) as DeploymentStatus[]).map((value) => ({ label: value, value }));

function contextLabel(row: DeploymentContext): string {
  return `${row.projectName} / ${row.environmentName} / ${row.composeName}`;
}

function SourceCell({ row }: { row: GlobalDeployment }) {
  const ref = /^[a-f\d]{40,64}$/i.test(row.sourceRef) ? row.sourceRef.slice(0, 7) : row.sourceRef;
  return <Text ellipsis={{ tooltip: [row.sourceRef, row.sourceSubject].filter(Boolean).join(' · ') }} type="secondary" style={{ display: 'block' }}>
    {[ref, row.sourceSubject].filter(Boolean).join(' · ') || '—'}
  </Text>;
}

function ContextCell({ row }: { row: DeploymentContext & { number: number } }) {
  return <Flex gap={2} vertical style={{ minWidth: 0 }}>
    <Flex align="baseline" gap={8} style={{ minWidth: 0 }}>
      <Text strong style={{ flexShrink: 0 }}>#{row.number}</Text>
      <Link
        to={paths.compose(row.projectId, row.environmentId, row.composeId, 'deployments')}
        title={contextLabel(row)}
        style={{ display: 'block', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
      >{row.composeName}</Link>
    </Flex>
    <Text ellipsis={{ tooltip: `${row.projectName} / ${row.environmentName}` }} type="secondary" style={{ display: 'block', fontSize: 12 }}>
      {row.projectName} / {row.environmentName}
    </Text>
  </Flex>;
}

/** The host's queue stays independent from the paginated, retained history. */
export function DeploymentsPage() {
  const { projects } = usePanel();
  const screens = Grid.useBreakpoint();
  const [query, setQuery] = useState<DeploymentHistoryQuery>(DEFAULT_QUERY);
  const [selected, setSelected] = useState<{ target: DeploymentLogTarget; context: string }>();
  const queue = useGlobalDeploymentQueue();
  const hasWork = Boolean(queue.data?.length);
  // Keep reading until an active history row settles even if the queue clears
  // just before the next history tick. Only one SWR timer owns these reads.
  const historyInterval = useCallback((data: GlobalDeploymentPage | undefined) =>
    hasWork || data?.items.some(isActive) ? 1000 : 5000, [hasWork]);
  const history = useSWR(['global-deployments', query], ([, filters]) => api.listGlobalDeployments(filters), {
    keepPreviousData: false,
    dedupingInterval: 500,
    refreshInterval: historyInterval,
    errorRetryInterval: 5000,
    errorRetryCount: 3,
  });
  useEffect(() => {
    const result = history.data;
    // SWR keeps each query separate; also check the response's pagination before
    // applying a shrinking count. A late effect cannot change a newer query.
    if (history.error || !result || result.page !== query.page || result.pageSize !== query.pageSize) return;
    const lastPage = Math.max(1, Math.ceil(result.total / result.pageSize));
    if ((query.page ?? 1) > lastPage) {
      setQuery((current) => current === query ? { ...current, page: lastPage } : current);
    }
  }, [history.data, history.error, query]);

  const projectOptions = projects.map((project) => ({ label: project.name, value: project.id, title: project.name }));
  const environments = useMemo(() => projects
    .filter((project) => query.projectId === undefined || project.id === query.projectId)
    .flatMap((project) => project.environments.map((environment) => ({ ...environment, project }))), [projects, query.projectId]);
  const environmentOptions = environments.map((environment) => ({
    label: query.projectId === undefined ? `${environment.project.name} / ${environment.name}` : environment.name,
    value: environment.id,
    title: `${environment.project.name} / ${environment.name}`,
  }));
  const composeOptions = environments
    .filter((environment) => query.environmentId === undefined || environment.id === query.environmentId)
    .flatMap((environment) => environment.composes.map((compose) => ({
      label: query.environmentId === undefined ? `${environment.project.name} / ${environment.name} / ${compose.name}` : compose.name,
      value: compose.id,
      title: `${environment.project.name} / ${environment.name} / ${compose.name}`,
    })));

  const setFilter = (key: 'projectId' | 'environmentId' | 'composeId' | 'status', value: number | DeploymentStatus | undefined) => {
    setQuery((previous) => {
      const next = { ...previous, page: 1 };
      delete next[key];
      if (key === 'projectId') {
        delete next.environmentId;
        delete next.composeId;
      } else if (key === 'environmentId') delete next.composeId;
      if (value !== undefined) Object.assign(next, { [key]: value });
      return next;
    });
  };
  const filtered = Object.keys(query).some((key) => key !== 'page' && key !== 'pageSize');
  const clearFilters = () => setQuery({ page: 1, pageSize: query.pageSize });
  const refresh = () => {
    void queue.mutate();
    void history.mutate();
  };
  const open = (row: GlobalDeployment | GlobalQueuedDeployment) => setSelected({
    context: contextLabel(row),
    target: {
      location: { composeId: row.composeId, environmentId: row.environmentId, projectId: row.projectId },
      number: row.number,
      ...('queuedAt' in row ? { deployment: row } : {}),
    },
  });
  const logButton = (row: GlobalDeployment | GlobalQueuedDeployment) => <Button
    aria-label={`View logs for ${contextLabel(row)} deployment #${row.number}`}
    onClick={() => open(row)}
    size="small"
  >{screens.sm ? 'View logs' : 'Logs'}</Button>;
  const running = queue.data?.find((row) => row.status === 'running');
  const queueColumns: TableColumnsType<GlobalQueuedDeployment> = [
    {
      title: 'Deployment', key: 'deployment', ellipsis: { showTitle: false },
      render: (_, row, index) => <Flex gap={6} vertical style={{ minWidth: 0 }}>
        <ContextCell row={row} />
        <Flex gap={6} align="center" wrap>
          <Tag color={STATUS_COLOR[row.status]}>{row.status}</Tag>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {row.status === 'running' ? 'Current' : `Position ${index + 1} · ${index} ahead`}
          </Text>
        </Flex>
        <Text ellipsis={{ tooltip: row.step ? stepLabel(row.step) : 'Waiting to start' }} style={{ display: 'block' }}>
          {row.step ? stepLabel(row.step) : 'Waiting to start'}
        </Text>
      </Flex>,
    },
    { title: '', key: 'logs', width: screens.sm ? 104 : 72, render: (_, row) => logButton(row) },
  ];
  const historyColumns: TableColumnsType<GlobalDeployment> = [
    {
      title: 'Deployment', key: 'deployment', ellipsis: { showTitle: false },
      render: (_, row) => <Flex vertical gap={6} style={{ minWidth: 0 }}>
        <ContextCell row={row} />
        {!screens.md && <Tag color={STATUS_COLOR[row.status]} style={{ alignSelf: 'flex-start' }}>{row.status}</Tag>}
        {!screens.md && <SourceCell row={row} />}
        {!screens.lg && <Text type="secondary" style={{ fontSize: 12 }}><RelativeTime value={row.queuedAt} /></Text>}
      </Flex>,
    },
    {
      title: 'Status', key: 'status', responsive: ['md'], width: 150,
      render: (_, row) => <Flex vertical gap={4} style={{ minWidth: 0 }}>
        <Tag color={STATUS_COLOR[row.status]} style={{ alignSelf: 'flex-start' }}>{row.status}</Tag>
        <Text ellipsis={{ tooltip: deploymentHeadline(row, running) }} type="secondary" style={{ display: 'block', fontSize: 12 }}>{deploymentHeadline(row, running)}</Text>
      </Flex>,
    },
    {
      title: 'Source', key: 'source', responsive: ['md'], ellipsis: { showTitle: false },
      render: (_, row) => <SourceCell row={row} />,
    },
    { title: 'Queued', key: 'queuedAt', responsive: ['lg'], width: 150, render: (_, row) => <RelativeTime value={row.queuedAt} /> },
    { title: '', key: 'logs', width: screens.sm ? 104 : 72, render: (_, row) => logButton(row) },
  ];

  return <>
    <div style={{ paddingTop: screens.lg ? 0 : 24 }}>
      <PageHeader
        title="Deployments"
        description="Follow the host queue and browse deployment history across all Composes."
        extra={<Button icon={<ReloadOutlined />} loading={queue.isValidating || history.isValidating} onClick={refresh}>Refresh</Button>}
      />
    </div>
    <Card title="Queue" variant="outlined" style={{ marginBottom: 24, minWidth: 0 }}>
      <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>All Composes. Running first, then queued in order. History filters apply below.</Text>
      {queue.error && <Alert
        action={<Button aria-label="Retry queue" onClick={() => void queue.mutate()}>Retry</Button>}
        description={queue.error instanceof Error ? queue.error.message : undefined}
        showIcon title="Could not load the deployment queue" type="error" style={{ marginBottom: 16 }}
      />}
      <Table<GlobalQueuedDeployment>
        columns={queueColumns} dataSource={queue.error ? [] : queue.data ?? []} loading={queue.isLoading}
        pagination={false} rowKey={(row) => `${row.projectId}/${row.environmentId}/${row.composeId}/${row.number}`} size="small" tableLayout="fixed"
        locale={{ emptyText: queue.isLoading || queue.error ? ' ' : <EmptyState title="No deployments in the queue" description="Deploy a Compose to add work to the host queue." /> }}
      />
    </Card>
    <Card title="History" variant="outlined" style={{ minWidth: 0 }}>
      <Text type="secondary" style={{ display: 'block', marginBottom: 16 }}>All retained records, newest first. The latest 20 deployments per Compose are kept.</Text>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 180px), 1fr))', gap: 12, marginBottom: 16 }}>
        <Flex vertical gap={4} style={{ minWidth: 0 }}>
          <label htmlFor="deployment-project">Project</label>
          <Select<number> id="deployment-project" allowClear placeholder="All Projects" options={projectOptions} value={query.projectId} onChange={(value) => setFilter('projectId', value)} showSearch={{ optionFilterProp: 'label' }} />
        </Flex>
        <Flex vertical gap={4} style={{ minWidth: 0 }}>
          <label htmlFor="deployment-environment">Environment</label>
          <Select<number> id="deployment-environment" allowClear placeholder="All Environments" options={environmentOptions} value={query.environmentId} onChange={(value) => setFilter('environmentId', value)} showSearch={{ optionFilterProp: 'label' }} />
        </Flex>
        <Flex vertical gap={4} style={{ minWidth: 0 }}>
          <label htmlFor="deployment-compose">Compose</label>
          <Select<number> id="deployment-compose" allowClear placeholder="All Composes" options={composeOptions} value={query.composeId} onChange={(value) => setFilter('composeId', value)} showSearch={{ optionFilterProp: 'label' }} />
        </Flex>
        <Flex vertical gap={4} style={{ minWidth: 0 }}>
          <label htmlFor="deployment-status">Status</label>
          <Select<DeploymentStatus> id="deployment-status" allowClear placeholder="All statuses" options={STATUS_OPTIONS} value={query.status} onChange={(value) => setFilter('status', value)} />
        </Flex>
        <Flex vertical gap={4} style={{ minWidth: 0 }}>
          <label htmlFor="deployment-date-from">Queued date</label>
          <DatePicker.RangePicker
            classNames={{ popup: { root: 'rp-deployments-date-popup' } }}
            id={{ start: 'deployment-date-from', end: 'deployment-date-to' }}
            placeholder={['From date', 'To date']}
            value={query.from && query.to ? [dayjs(query.from), dayjs(query.to)] : null}
            onChange={(dates) => setQuery((previous) => {
              const next = { ...previous, page: 1 };
              delete next.from;
              delete next.to;
              if (dates?.[0] && dates[1]) {
                next.from = dates[0].startOf('day').toISOString();
                next.to = dates[1].endOf('day').toISOString();
              }
              return next;
            })}
            style={{ width: '100%', minWidth: 0 }}
          />
        </Flex>
      </div>
      {filtered && <Button onClick={clearFilters} style={{ marginBottom: 16 }}>Clear filters</Button>}
      {history.error && <Alert
        action={<Button aria-label="Retry history" onClick={() => void history.mutate()}>Retry</Button>}
        description={history.error instanceof Error ? history.error.message : undefined}
        showIcon title="Could not load deployment history" type="error" style={{ marginBottom: 16 }}
      />}
      <Table<GlobalDeployment>
        columns={historyColumns} dataSource={history.error ? [] : history.data?.items ?? []} loading={history.isLoading}
        rowKey="id" size="small" tableLayout="fixed"
        pagination={{
          current: query.page, pageSize: query.pageSize, total: history.data?.total ?? 0,
          pageSizeOptions: [20, 50, 100], showSizeChanger: true, showLessItems: true,
          simple: screens.sm ? false : { readOnly: true },
          showTotal: screens.sm ? (total) => `${total} deployments` : undefined,
          onChange: (page, pageSize) => setQuery((previous) => ({ ...previous, page: pageSize !== previous.pageSize ? 1 : page, pageSize })),
        }}
        locale={{ emptyText: history.isLoading || history.error ? ' ' : <EmptyState
          title={filtered ? 'No matching deployments' : 'No deployment history'}
          description={filtered ? 'Change or clear the history filters to see other deployments.' : 'Deploy a Compose to start its history.'}
        /> }}
      />
    </Card>
    <DeploymentLogDrawer target={selected?.target} context={selected?.context} onClose={() => setSelected(undefined)} running={running} follow onCanceled={refresh} />
  </>;
}
