import { FolderOpenOutlined, LockOutlined, PlayCircleOutlined, StopOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Result, Skeleton, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useLayoutEffect, useState } from 'react';
import { navigate, paths } from '../app/router';
import { publishSceneHandle } from '../app/sceneAdapters';
import { useNow } from '../app/useNow';
import { useUiState } from '../app/uiState';
import { CodeValue, EmptyState } from './primitives';
import { StartHandleDialog } from './StartHandleDialog';
import { IDLE_TIMEOUT_MINUTES, usePanel } from '../data/store';
import type { Compose, Volume } from '../data/types';
import { idleMinutesLeft } from '../data/types';

const { Text, Paragraph } = Typography;

/**
 * The Compose page's Volumes tab.
 *
 * Lists only the Volumes **this** Compose mounts — not the host's volumes.
 * Resolution goes through `docker compose config`, which is also why this tab
 * has an unavailable state at all: until the Compose has been deployed once
 * there is no environment file for the CLI to read.
 *
 * The tab is a launcher, not the file manager. Opening a Volume Handle here
 * mints the container that mounts it; the files themselves are read in the
 * Volume Explorer, which opens in its own browser tab because browsing a
 * directory is a place you go, not a panel you glance at.
 */
export function ComposeVolumesTab({
  compose,
  environmentId,
  projectId,
}: {
  compose: Compose;
  environmentId: number;
  projectId: number;
}) {
  const { handles, isPending, resolveComposeVolumes, startHandle, stopHandle } = usePanel();
  const { message } = App.useApp();
  const now = useNow();

  // EXCLUDED (ticket 02) — data (`resolveComposeVolumes`), the same verdict as
  // its `VolumeExplorerPage` twin: an array a `?ui=` string cannot carry, and
  // the resolution effect below is a data load, not an overlay reset.
  const [volumes, setVolumes] = useState<Volume[]>();
  const [error, setError] = useUiState<string | undefined>('volumes.error', undefined);
  const [starting, setStarting] = useUiState<Volume | undefined>('volumes.starting', undefined);

  useEffect(() => {
    let cancelled = false;
    setVolumes(undefined);
    void resolveComposeVolumes(compose.id).then((result) => {
      if (cancelled) return;
      if (result.ok) {
        setVolumes(result.volumes);
        setError(undefined);
      } else {
        setError(result.error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [compose.id, resolveComposeVolumes]);

  // Ticket 02, decision D1: `starting` holds a `Volume` — an object a `?ui=`
  // string cannot carry — so the `object:volumes.starting` adapter seeds it
  // through this, the tab's own setter, with the first Volume the tab itself
  // resolved. Idempotent: a second call finds `starting` already set and
  // changes nothing.
  useLayoutEffect(
    () =>
      publishSceneHandle('volumes.starting.seed', () => {
        setStarting((current) => current ?? volumes?.[0]);
      }),
    [setStarting, volumes],
  );

  const openExplorer = useCallback(
    (volume: Volume, inNewTab = true) => {
      const href = paths.volumeExplorer(projectId, environmentId, compose.id, volume.id);
      if (inNewTab) {
        window.open(href, '_blank');
      } else {
        navigate(href);
      }
    },
    [compose.id, environmentId, projectId],
  );

  if (error) {
    return (
      <Card variant="outlined">
        <Result
          extra={
            <Button disabled={compose.status.total === 0} onClick={() => message.success('Deploying…')} type="primary">
              Deploy this Compose
            </Button>
          }
          status="warning"
          subTitle={
            <>
              The panel asks the Docker CLI which Volumes this Compose mounts, so that a path is never typed by hand.
              That needs the environment file a deploy writes.
            </>
          }
          title="Not resolvable until this Compose is deployed"
        />
        <CodeValue copyable label="docker compose:" value={error} />
      </Card>
    );
  }

  if (!volumes) {
    return <Skeleton active paragraph={{ rows: 5 }} title={false} />;
  }

  if (volumes.length === 0) {
    return (
      <Card variant="outlined">
        <EmptyState
          description="This Compose declares no volumes: and no bind mounts. Anything it writes lives inside its container, and goes away with it."
          title="Nothing mounted"
        />
      </Card>
    );
  }

  const runningServices = compose.services.filter((service) => service.state === 'running').map((service) => service.name);
  const liveCount = volumes.filter((volume) => handles[volume.id]).length;

  const columns: ColumnsType<Volume> = [
    {
      dataIndex: 'declaredAs',
      render: (_: unknown, volume) => (
        <Flex align="center" gap={8} wrap>
          <Text strong>{volume.declaredAs}</Text>
          <Tag color={volume.kind === 'bind' ? 'geekblue' : undefined}>{volume.kind === 'bind' ? 'bind' : 'volume'}</Tag>
          {volume.readOnly && (
            <Tooltip title="The Compose mounts it read-only, so a handle over it can never write.">
              <Tag icon={<LockOutlined />}>ro</Tag>
            </Tooltip>
          )}
        </Flex>
      ),
      title: 'Volume',
      width: 190,
    },
    {
      dataIndex: 'source',
      render: (source: string, volume) => (
        <Flex align="center" gap={6} style={{ minWidth: 0, width: '100%' }}>
          <CodeValue copyable value={source} />
          {volume.existence === 'unverifiable' && (
            <Tooltip title="An absolute bind source outside the data directory. The panel cannot check whether it exists, and mounting it would create it — so the start dialog says so instead.">
              <Tag color="warning">unchecked</Tag>
            </Tooltip>
          )}
        </Flex>
      ),
      title: 'Source on the host',
      width: 340,
    },
    {
      dataIndex: 'target',
      render: (target: string) => <CodeValue copyable value={target} />,
      title: 'In the container',
    },
    {
      dataIndex: 'services',
      render: (services: string[]) => (
        <Flex gap={4} wrap>
          {services.map((service) => (
            <Tag key={service}>{service}</Tag>
          ))}
        </Flex>
      ),
      title: 'Mounted by',
      width: 110,
    },
    {
      key: 'handle',
      render: (_: unknown, volume) => {
        const handle = handles[volume.id];
        if (!handle) {
          return <Text type="secondary">Not open</Text>;
        }
        const remaining = idleMinutesLeft(handle, now, IDLE_TIMEOUT_MINUTES);
        return (
          <Flex align="center" gap={6} wrap>
            <Tag color={handle.mode === 'rw' ? 'processing' : 'default'}>{handle.mode === 'rw' ? 'open, writable' : 'open, read-only'}</Tag>
            {remaining !== undefined && (
              <Text type="secondary" style={{ fontSize: 12 }}>
                closes in {remaining} min
              </Text>
            )}
          </Flex>
        );
      },
      title: 'Handle',
      width: 210,
    },
    {
      key: 'actions',
      render: (_: unknown, volume) => {
        const handle = handles[volume.id];
        const stopped = volume.existence === 'absent';
        return (
          <Flex gap={8} justify="flex-end" wrap>
            {handle ? (
              <>
                <Button icon={<FolderOpenOutlined />} onClick={() => openExplorer(volume)} size="small" type="primary">
                  Browse
                </Button>
                <Button
                  icon={<StopOutlined />}
                  loading={isPending(`handle-stop:${volume.id}`)}
                  onClick={() => void stopHandle(volume.id).then(() => message.success(`Closed ${volume.declaredAs}`))}
                  size="small"
                >
                  Close
                </Button>
              </>
            ) : (
              <Button
                disabled={stopped}
                icon={<PlayCircleOutlined />}
                onClick={() => setStarting(volume)}
                size="small"
                type="primary"
              >
                Open
              </Button>
            )}
          </Flex>
        );
      },
      title: '',
      width: 190,
    },
  ];

  return (
    <>
      {liveCount > 0 && (
        <Alert
          description={
            <>
              A handle keeps a container running with this Volume mounted until it is closed. It is off every network, so
              nothing can reach it — but it does hold the Volume, and it does not close itself when you leave the page.
            </>
          }
          message={`${liveCount} ${liveCount === 1 ? 'Volume is' : 'Volumes are'} open right now`}
          showIcon
          style={{ marginBottom: 16 }}
          type="info"
        />
      )}
      <Table
        columns={columns}
        dataSource={volumes}
        pagination={false}
        rowKey={(volume) => volume.id}
        size="middle"
        tableLayout="fixed"
      />
      <Paragraph type="secondary" style={{ marginTop: 12, marginBottom: 0 }}>
        Volume list resolved from <Text code>docker compose config</Text>. The panel never accepts a host path from the
        form — a Volume can only be one this Compose already declares.
      </Paragraph>

      {starting && (
        <StartHandleDialog
          loading={isPending(`handle-start:${starting.id}`)}
          onCancel={() => setStarting(undefined)}
          onStart={(input) => {
            const volume = starting;
            void startHandle(volume.id, input).then(() => {
              setStarting(undefined);
              message.success(`Opened ${volume.declaredAs}`);
              openExplorer(volume);
            });
          }}
          open={Boolean(starting)}
          resolved={{ gid: 999, source: 'container-inspect', uid: 999 }}
          runningServices={runningServices}
          volume={starting}
        />
      )}
    </>
  );
}
