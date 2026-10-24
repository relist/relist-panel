import { DeleteOutlined, ReloadOutlined } from '@ant-design/icons';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Collapse,
  Descriptions,
  Flex,
  Skeleton,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd';
import type { TableColumnsType } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { DangerModal } from '../components/DangerModal';
import { formatBytes } from '../components/MetricChart';
import { PageHeader, RelativeTime } from '../components/primitives';
import { ServerMigrationCard } from '../components/ServerMigrationCard';
import { api } from '../data/api';
import type {
  DockerContainer,
  DockerNetwork,
  DockerPruneKind,
  DockerPrunePlan,
  DockerPruneResult,
  DockerUsage,
  DockerVolume,
} from '../data/types';
import { useGlobalDeploymentQueue } from '../data/useGlobalDeploymentQueue';

const { Text } = Typography;
const labels: Record<string, string> = {
  images: 'Images',
  containers: 'Containers',
  volumes: 'Volumes',
  networks: 'Networks',
  'build-cache': 'Build cache',
};
const operations: Record<
  DockerPruneKind | 'recover',
  { title: string; description: string }
> = {
  recover: {
    title: 'Resume Docker operations',
    description:
      'Verify directly with the Docker daemon that the previous prune has finished deleting. Resuming early may allow deployments to overlap deletion. This acknowledgement does not cancel or retry the previous prune.',
  },
  'build-cache': {
    title: 'Prune build cache',
    description:
      'Removes unused cache from the Docker default builder. Future builds may take longer.',
  },
  images: {
    title: 'Prune dangling images',
    description: 'Removes untagged images that no container references.',
  },
  'unused-images': {
    title: 'Prune all unused images',
    description:
      'Removes every image that no container references, including tagged images. Future deployments may need to pull or rebuild images. Skip build may fail until its image is rebuilt.',
  },
  containers: {
    title: 'Prune stopped containers',
    description:
      'Removes all stopped containers, their writable layers and their logs. Their processes cannot be restarted without recreating the containers. Volumes are kept.',
  },
  networks: {
    title: 'Prune unused networks',
    description:
      'Removes custom networks that no container uses. Docker keeps its built-in networks.',
  },
  volumes: {
    title: 'Prune anonymous volumes',
    description:
      'Permanently deletes anonymous local volumes that no container references. Named volumes are kept.',
  },
  'all-volumes': {
    title: 'Prune all unused volumes',
    description:
      'Permanently deletes all unused local volumes, including named volumes. A stopped or removed Compose may still need this data. Back up its volumes before continuing.',
  },
};
const wrapping = {
  overflowWrap: 'anywhere' as const,
  wordBreak: 'break-word' as const,
};
const size = (value: number | null) =>
  value === null ? (
    <Text type="secondary">Unavailable</Text>
  ) : (
    formatBytes(value)
  );
const listedOperation = (kind: DockerPruneKind | 'recover') =>
  kind === 'volumes' ||
  kind === 'all-volumes' ||
  kind === 'images' ||
  kind === 'unused-images';
const planCap = 50;
const volumeOperation = (kind: DockerPruneKind | 'recover') =>
  kind === 'volumes' || kind === 'all-volumes';

export function DockerPage() {
  const forceRefresh = useRef(false);
  const { data, error, isLoading, isValidating, mutate } = useSWR(
    '/api/docker',
    () => {
      const force = forceRefresh.current;
      forceRefresh.current = false;
      return api.dockerReport(force);
    },
    {
      refreshInterval: 60_000,
      revalidateOnFocus: false,
      errorRetryCount: 3,
      errorRetryInterval: 10_000,
    },
  );
  const queue = useGlobalDeploymentQueue();
  const [selected, setSelected] = useState<DockerPruneKind | 'recover'>();
  const [pruning, setPruning] = useState(false);
  const [pruneError, setPruneError] = useState<string>();
  const [resumed, setResumed] = useState(false);
  const [result, setResult] = useState<DockerPruneResult>();
  const [plan, setPlan] = useState<DockerPrunePlan>();
  const [planError, setPlanError] = useState<string>();
  const [includePanel, setIncludePanel] = useState(false);
  const [now, setNow] = useState(Date.now());
  const submitting = useRef(false);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    const clock = window.setInterval(() => setNow(Date.now()), 15_000);
    return () => {
      mounted.current = false;
      window.clearInterval(clock);
    };
  }, []);
  // The plan is a preview; the server recomputes it when the prune runs.
  useEffect(() => {
    setPlan(undefined);
    setPlanError(undefined);
    setIncludePanel(false);
    if (!selected || !listedOperation(selected)) return;
    let current = true;
    api
      .dockerPrunePlan(selected)
      .then((next) => {
        if (current) setPlan(next);
      })
      .catch((reason) => {
        if (current)
          setPlanError(
            reason instanceof Error
              ? reason.message
              : 'Could not load what this prune would delete',
          );
      });
    return () => {
      current = false;
    };
  }, [selected]);
  const planning = !!selected && listedOperation(selected) && !plan;
  const panelData = includePanel && !!plan && plan.skip.length > 0;
  const stale =
    !!data &&
    (!Number.isFinite(Date.parse(data.checkedAt)) ||
      now - Date.parse(data.checkedAt) > 120_000);
  const busyQueue = !!queue.data?.length;
  const disabled =
    !data ||
    !!error ||
    stale ||
    queue.isLoading ||
    !!queue.error ||
    busyQueue ||
    isValidating ||
    pruning ||
    data.maintenanceBlocked;
  const refresh = () => {
    forceRefresh.current = true;
    return mutate();
  };
  const start = (kind: DockerPruneKind) => {
    setPruneError(undefined);
    setSelected(kind);
  };
  const confirm = async () => {
    if (!selected || submitting.current || (selected !== 'recover' && disabled))
      return;
    const kind = selected;
    submitting.current = true;
    setPruning(true);
    setPruneError(undefined);
    setResult(undefined);
    setResumed(false);
    try {
      if (kind === 'recover') {
        await api.resumeDocker('RESUME DOCKER');
        if (mounted.current) {
          setResumed(true);
          setSelected(undefined);
        }
      } else {
        const outcome = await api.pruneDocker(
          kind,
          panelData
            ? 'DELETE PANEL DATA'
            : volumeOperation(kind)
              ? 'DELETE VOLUMES'
              : 'PRUNE',
          ...(panelData ? [true] : []),
        );
        if (mounted.current) {
          setResult(outcome);
          setSelected(undefined);
        }
      }
    } catch (reason) {
      if (mounted.current) {
        setPruneError(
          reason instanceof Error ? reason.message : 'Docker prune failed',
        );
        setSelected(undefined);
      }
    } finally {
      // Re-read after success or failure: a failed request can follow partial deletion.
      // Refresh errors belong to the report, never to the mutation's outcome.
      if (mounted.current) {
        await refresh().catch(() => undefined);
        await queue.mutate().catch(() => undefined);
        setPruning(false);
      }
      submitting.current = false;
    }
  };
  const pruneButton = (kind: DockerPruneKind) => (
    <Button
      aria-label={operations[kind].title}
      danger
      disabled={disabled}
      icon={<DeleteOutlined />}
      onClick={() => start(kind)}
    >
      {operations[kind].title}
    </Button>
  );
  const usageColumns: TableColumnsType<DockerUsage> = [
    {
      title: 'Resource',
      dataIndex: 'kind',
      render: (kind: string) => labels[kind] ?? kind,
      width: 130,
    },
    { title: 'Total', dataIndex: 'count', width: 75 },
    {
      title: 'In use',
      dataIndex: 'active',
      render: (value: number) => (value < 0 ? 'Unavailable' : value),
      width: 100,
    },
    { title: 'Reported size', dataIndex: 'size', render: size, width: 130 },
    {
      title: 'Reclaimable estimate',
      dataIndex: 'reclaimable',
      render: size,
      width: 170,
    },
    {
      title: 'Cleanup',
      key: 'cleanup',
      width: 235,
      render: (_, row) => pruneButton(row.kind as DockerPruneKind),
    },
  ];
  const containerColumns: TableColumnsType<DockerContainer> = [
    {
      title: 'Container',
      dataIndex: 'names',
      render: (names: string[], row) => (
        <Text style={wrapping}>
          {names.map((n) => n.replace(/^\//, '')).join(', ') ||
            row.id.slice(0, 12)}
        </Text>
      ),
      width: 220,
    },
    {
      title: 'Image',
      dataIndex: 'image',
      render: (image: string) => <Text style={wrapping}>{image}</Text>,
      width: 280,
    },
    {
      title: 'State',
      dataIndex: 'state',
      render: (state: string) => (
        <Tag color={state === 'running' ? 'success' : undefined}>{state}</Tag>
      ),
      width: 110,
    },
    { title: 'Writable layer', dataIndex: 'size', render: size, width: 140 },
  ];
  const volumeColumns: TableColumnsType<DockerVolume> = [
    {
      title: 'Volume',
      dataIndex: 'name',
      render: (name: string) => <Text style={wrapping}>{name}</Text>,
      width: 300,
    },
    { title: 'Driver', dataIndex: 'driver', width: 130 },
    { title: 'Size', dataIndex: 'size', render: size, width: 130 },
    {
      title: 'Container references',
      dataIndex: 'references',
      render: (value: number | null) =>
        value === null ? 'Unavailable' : value,
      width: 170,
    },
  ];
  const networkColumns: TableColumnsType<DockerNetwork> = [
    {
      title: 'Network',
      dataIndex: 'name',
      render: (name: string) => <Text style={wrapping}>{name}</Text>,
      width: 300,
    },
    { title: 'Driver', dataIndex: 'driver', width: 120 },
    { title: 'Scope', dataIndex: 'scope', width: 100 },
    {
      title: 'Attached containers',
      dataIndex: 'containers',
      render: (value: number | null) =>
        value === null ? 'Unavailable' : value,
      width: 170,
    },
  ];
  return (
    <>
      <PageHeader
        title="Docker"
        description="Reports and cleanup for the connected Docker host, including resources outside Relist."
        extra={
          <Flex gap={8} wrap>
            <Link to={paths.images}>
              <Button>Manage images</Button>
            </Link>
            <Button
              aria-label="Refresh"
              icon={<ReloadOutlined />}
              loading={isValidating}
              disabled={pruning}
              onClick={() => {
                void refresh().catch(() => undefined);
              }}
            >
              Refresh
            </Button>
          </Flex>
        }
      />
      <Flex gap={16} vertical style={{ minWidth: 0 }}>
        {error && (
          <Alert
            showIcon
            type="error"
            title={
              error instanceof Error
                ? error.message
                : 'Docker report is unavailable'
            }
            description={
              data
                ? 'Showing the last successful report. Refresh before pruning.'
                : undefined
            }
            action={
              <Button
                onClick={() => {
                  void refresh().catch(() => undefined);
                }}
              >
                Retry
              </Button>
            }
          />
        )}
        {stale && (
          <Alert
            showIcon
            type="warning"
            title="This Docker report is stale. Refresh before pruning."
          />
        )}
        {queue.error && (
          <Alert
            showIcon
            type="warning"
            title="Could not check the Deployment queue. Pruning is disabled."
            action={
              <Button
                onClick={() => {
                  void queue.mutate().catch(() => undefined);
                }}
              >
                Retry
              </Button>
            }
          />
        )}
        {busyQueue && (
          <Alert
            showIcon
            type="info"
            title="The Deployment queue has pending or running work. Wait for it to finish before pruning."
            action={<Link to={paths.deployments}>View queue</Link>}
          />
        )}
        {pruning && (
          <Alert
            showIcon
            type="info"
            title="Docker cleanup is in progress. Panel operations that use Docker wait until it finishes."
          />
        )}
        {data?.maintenanceBlocked && (
          <Alert
            showIcon
            type="warning"
            title="Previous prune completion is unknown. Docker operations are paused."
            description={
              <Flex gap={8} vertical>
                <span>
                  Check the Docker daemon and confirm that deletion has finished
                  before resuming.
                </span>
                <Button
                  disabled={pruning}
                  style={{ alignSelf: 'flex-start' }}
                  onClick={() => {
                    setPruneError(undefined);
                    setSelected('recover');
                  }}
                >
                  Resume Docker operations
                </Button>
              </Flex>
            }
          />
        )}
        {resumed && (
          <Alert
            showIcon
            type="success"
            title="Docker operations resumed after your acknowledgement."
          />
        )}
        {pruneError && <Alert showIcon type="error" title={pruneError} />}
        {!!result?.skipped?.length && (
          <Alert
            showIcon
            type="info"
            title={`Skipped ${result.skipped.length}`}
            description={
              <NameList
                names={result.skipped.map(
                  (entry) => `${entry.name} (${entry.reason})`,
                )}
              />
            }
          />
        )}
        {result && (
          <Alert
            showIcon
            type="success"
            title={`Reclaimed ${formatBytes(result.spaceReclaimed)} · ${operations[result.kind].title}`}
            description={
              result.deleted.length > 0 ? (
                <Collapse
                  ghost
                  items={[
                    {
                      key: 'deleted',
                      label: `Removed resources (${result.deleted.length} entries)`,
                      children: (
                        <div
                          style={{
                            ...wrapping,
                            maxHeight: 220,
                            overflow: 'auto',
                          }}
                        >
                          {result.deleted.slice(0, 100).map((entry, i) => (
                            <div key={i}>{entry}</div>
                          ))}
                          {result.deleted.length > 100 && (
                            <Text type="secondary">
                              Showing the first 100 entries.
                            </Text>
                          )}
                        </div>
                      ),
                    },
                  ]}
                />
              ) : result.spaceReclaimed > 0 ? (
                'Docker did not return a list of removed resources.'
              ) : (
                'Docker found no resources to remove.'
              )
            }
          />
        )}
        {isLoading && !data && (
          <Card>
            <Skeleton active />
          </Card>
        )}
        {data && (
          <>
            <Card>
              <Descriptions
                size="small"
                column={{ xs: 1, sm: 2, lg: 3 }}
                styles={{
                  content: { ...wrapping, minWidth: 0 },
                  label: { whiteSpace: 'nowrap' },
                }}
                items={[
                  {
                    key: 'host',
                    label: 'Host',
                    children: <Text style={wrapping}>{data.hostname}</Text>,
                  },
                  {
                    key: 'version',
                    label: 'Docker Engine',
                    children: `${data.version} (API ${data.apiVersion})`,
                  },
                  {
                    key: 'driver',
                    label: 'Storage driver',
                    children: data.storageDriver,
                  },
                  {
                    key: 'containers',
                    span: { xs: 1, sm: 1, lg: 2 },
                    label: 'Containers',
                    children: `${data.containersRunning} running / ${data.containersPaused} paused / ${data.containersStopped} stopped`,
                  },
                  {
                    key: 'checked',
                    label: 'Checked',
                    children: <RelativeTime value={data.checkedAt} />,
                  },
                  {
                    key: 'root',
                    label: 'Docker data',
                    span: 'filled',
                    children: (
                      <Text style={wrapping}>{data.dockerRootDir}</Text>
                    ),
                  },
                ]}
              />
            </Card>
            {data.warnings.map((warning) => (
              <Alert key={warning} showIcon type="warning" title={warning} />
            ))}
            <Card title="Docker storage">
              <Table
                columns={usageColumns}
                dataSource={data.usage}
                pagination={false}
                rowKey="kind"
                scroll={{ x: 840 }}
                size="small"
                tableLayout="fixed"
              />
              <Text
                type="secondary"
                style={{ display: 'block', marginTop: 12 }}
              >
                Category sizes can share storage. Reclaimable values are
                estimates; the volumes estimate includes named volumes. Image
                reclaimable space is unavailable because shared layers cannot be
                summed reliably. Actual reclaimed space appears after pruning.
              </Text>
            </Card>
            <Collapse
              items={[
                {
                  key: 'advanced',
                  label: 'Advanced cleanup',
                  children: (
                    <Flex gap={16} vertical>
                      <Text>
                        These options also remove tagged images or persistent
                        named volume data. Cleanup affects the entire Docker
                        host.
                      </Text>
                      <Flex gap={8} wrap>
                        {pruneButton('unused-images')}
                        {pruneButton('all-volumes')}
                      </Flex>
                    </Flex>
                  ),
                },
              ]}
            />
            <Card title="Host resources">
              <Tabs
                items={[
                  {
                    key: 'containers',
                    label: 'Containers',
                    children: (
                      <Table
                        columns={containerColumns}
                        dataSource={data.containers}
                        rowKey="id"
                        pagination={{ pageSize: 10, showSizeChanger: false }}
                        scroll={{ x: 750 }}
                        tableLayout="fixed"
                        size="small"
                      />
                    ),
                  },
                  {
                    key: 'volumes',
                    label: 'Volumes',
                    children: (
                      <Table
                        columns={volumeColumns}
                        dataSource={data.volumes}
                        rowKey="name"
                        pagination={{ pageSize: 10, showSizeChanger: false }}
                        scroll={{ x: 730 }}
                        tableLayout="fixed"
                        size="small"
                      />
                    ),
                  },
                  {
                    key: 'networks',
                    label: 'Networks',
                    children: (
                      <Table
                        columns={networkColumns}
                        dataSource={data.networks}
                        rowKey="id"
                        pagination={{ pageSize: 10, showSizeChanger: false }}
                        scroll={{ x: 690 }}
                        tableLayout="fixed"
                        size="small"
                      />
                    ),
                  },
                ]}
              />
            </Card>
          </>
        )}
        <ServerMigrationCard />
      </Flex>
      <DangerModal
        confirmWord={
          selected === 'recover'
            ? 'RESUME DOCKER'
            : panelData
              ? 'DELETE PANEL DATA'
              : selected && volumeOperation(selected)
                ? 'DELETE VOLUMES'
                : 'PRUNE'
        }
        description={selected ? operations[selected].description : ''}
        consequences={
          <Flex gap={12} vertical>
            {selected !== 'recover' && (
              <Text>
                This operation affects every eligible resource on the Docker
                host, including resources outside Relist. Running containers are
                kept.
              </Text>
            )}
            {selected && volumeOperation(selected) && (
              <Alert
                showIcon
                type="warning"
                title="Deleted volume data is not restored if a later deployment fails. Back up the data before continuing."
              />
            )}
            {selected && listedOperation(selected) && (
              <PrunePlanList
                error={planError}
                includePanel={includePanel}
                onIncludePanel={setIncludePanel}
                plan={plan}
              />
            )}
            {(busyQueue || queue.error || stale || error) && (
              <Alert
                showIcon
                type="warning"
                title="The host or queue changed. Close this confirmation and refresh before pruning."
              />
            )}
          </Flex>
        }
        disabled={
          selected === 'recover' ? pruning : disabled || planning || !!planError
        }
        loading={pruning}
        okText={selected === 'recover' ? 'Resume' : 'Prune'}
        onCancel={() => {
          if (!submitting.current) setSelected(undefined);
        }}
        onConfirm={() => {
          void confirm();
        }}
        open={selected !== undefined}
        title={selected ? operations[selected].title : 'Docker cleanup'}
      />
    </>
  );
}

function NameList({ names }: { names: string[] }) {
  return (
    <div style={{ ...wrapping, maxHeight: 160, overflow: 'auto' }}>
      {names.slice(0, planCap).map((name) => (
        <div key={name}>{name}</div>
      ))}
      {names.length > planCap && (
        <Text type="secondary">and {names.length - planCap} more</Text>
      )}
    </div>
  );
}

/** What a volume or image prune deletes, what it keeps for panel Composes, and the opt-in to delete those too. */
function PrunePlanList({
  error,
  includePanel,
  onIncludePanel,
  plan,
}: {
  error?: string;
  includePanel: boolean;
  onIncludePanel: (value: boolean) => void;
  plan?: DockerPrunePlan;
}) {
  if (error) return <Alert showIcon type="error" title={error} />;
  if (!plan) return <Skeleton active paragraph={{ rows: 2 }} />;
  const kept = includePanel ? [] : plan.skip;
  const deleted = [
    ...plan.delete.map((entry) => entry.name),
    ...(includePanel ? plan.skip.map((entry) => entry.name) : []),
  ];
  return (
    <Flex gap={8} vertical>
      <Text strong>Deleted ({deleted.length})</Text>
      <NameList names={deleted} />
      {kept.length > 0 && (
        <>
          <Text strong>Kept because a Compose uses them ({kept.length})</Text>
          <NameList
            names={kept.map((entry) =>
              entry.compose ? `${entry.name} — Compose ${entry.compose}` : entry.name,
            )}
          />
        </>
      )}
      {plan.skip.length > 0 && (
        <Checkbox
          checked={includePanel}
          onChange={(event) => onIncludePanel(event.target.checked)}
        >
          Also delete the ones panel Composes use
        </Checkbox>
      )}
    </Flex>
  );
}
