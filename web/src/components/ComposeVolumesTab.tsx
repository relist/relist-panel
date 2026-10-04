import { DeleteOutlined, ExportOutlined, LockOutlined, RocketOutlined } from '@ant-design/icons';
import {
  Alert,
  Button,
  Card,
  Flex,
  Result,
  Skeleton,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { useState } from 'react';
import { CODE_FONT_STACK, LAYOUT } from '../app/theme';
import type { ComposeVolume, ComposeVolumes, OrphanVolume } from '../data/types';
import { DangerModal } from './DangerModal';
import { CodeValue, EmptyState, RelativeTime } from './primitives';
import { downloadRefusal, orphansView, resolvedVolumePath, volumeLabel, volumesView } from './volumes-view';

const { Text, Paragraph } = Typography;

/**
 * The Compose screen's Volumes tab (GUIDELINES §4.7): the read-only launcher
 * over this Compose's resolved Volumes, listed with kind, source, resolved
 * path and every Service's container-side target.
 *
 * The page owns the one fetch — `available: false` hides the whole tab, which
 * is a page-level decision — so this component receives the resolution rather
 * than asking for it, and a re-render never refetches. Every row opens the
 * Volume Explorer (its own route, opened in a new browser tab), where browsing,
 * downloads, writes and restores live: nothing here writes, and no archive is
 * started from this tab.
 */
export function ComposeVolumesTab({
  deploying,
  error,
  explorerHref,
  onDeleteOrphan,
  onDeploy,
  onRetry,
  onRetryOrphans,
  orphans,
  orphansError,
  payload,
}: {
  /** The page's deploy is in flight, so the state's affordance spins with it. */
  deploying: boolean;
  error?: string;
  /** Builds the Volume Explorer's URL for one Volume. */
  explorerHref: (volume: ComposeVolume) => string;
  /** Deletes one orphan; rejects with the API's message (the page shows it) so the dialog stays open. */
  onDeleteOrphan: (name: string) => Promise<void>;
  onDeploy: () => void;
  onRetry: () => void;
  onRetryOrphans: () => void;
  /** The page's one orphan fetch: the list, or its error, or neither while in flight. */
  orphans?: OrphanVolume[];
  orphansError?: string;
  payload?: ComposeVolumes;
}) {
  const view = volumesView(payload, error);

  if (view.status === 'loading') {
    return <Skeleton active paragraph={{ rows: 5 }} title={false} />;
  }

  if (view.status === 'hidden') {
    // The page hides the tab outright on this state, so this is only what a
    // stale link renders for the instant before the redirect lands.
    return (
      <Alert
        description={view.reason}
        title="Volume files are unavailable"
        showIcon
        type="warning"
      />
    );
  }

  if (view.status === 'not-deployed') {
    return (
      <Card variant="outlined">
        <Result
          extra={
            <Button icon={<RocketOutlined />} loading={deploying} onClick={onDeploy} type="primary">
              Deploy this Compose
            </Button>
          }
          status="warning"
          subTitle="The panel asks the Docker CLI which Volumes this Compose mounts, so that a path is never typed by hand. That needs the environment file a deploy writes."
          title="Not resolvable until this Compose is deployed"
        />
        <CodeValue label="docker compose:" value={view.message} />
      </Card>
    );
  }

  if (view.status === 'error') {
    return (
      <Alert
        action={<Button onClick={onRetry}>Retry</Button>}
        description={view.message}
        title="Could not resolve this Compose’s Volumes"
        showIcon
        type="error"
      />
    );
  }

  const orphanSection = (
    <OrphanVolumes
      error={orphansError}
      onDelete={onDeleteOrphan}
      onRetry={onRetryOrphans}
      orphans={orphans}
    />
  );

  if (view.status === 'empty') {
    return (
      <>
        <Card variant="outlined">
          <EmptyState
            description="This Compose declares no volumes and no bind mounts. Anything it writes lives inside its container, and goes away with it."
            title="Nothing mounted"
          />
        </Card>
        {orphanSection}
      </>
    );
  }

  return (
    <>
      {view.warnings.map((warning) => (
        <Alert
          description={warning.message}
          key={`${warning.status}:${warning.volume.source}`}
          title={
            warning.status === 'missing'
              ? `${volumeLabel(warning.volume)} — the source is missing`
              : `${volumeLabel(warning.volume)} — the panel cannot check this path`
          }
          showIcon
          style={{ marginBottom: LAYOUT.gutterSmall }}
          type={warning.status === 'missing' ? 'error' : 'warning'}
        />
      ))}

      <Table<ComposeVolume>
        columns={[
          {
            dataIndex: 'logicalName',
            key: 'volume',
            render: (_value, volume) => (
              <Flex align="center" gap={8} wrap>
                <Text strong style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>
                  {volumeLabel(volume)}
                </Text>
                <Tag color={volume.kind === 'bind' ? 'geekblue' : undefined}>{volume.kind}</Tag>
              </Flex>
            ),
            title: 'Volume',
            width: 220,
          },
          {
            dataIndex: 'source',
            key: 'source',
            render: (_value, volume) => <CodeValue copyable value={resolvedVolumePath(volume)} />,
            title: 'Source on the host',
            width: 320,
          },
          {
            dataIndex: 'uses',
            key: 'uses',
            render: (_value, volume) => (
              <Flex gap={8} vertical>
                {volume.uses.map((use) => (
                  <Flex align="center" gap={8} key={`${use.service}:${use.target}`}>
                    <Tag style={{ marginInlineEnd: 0 }}>{use.service}</Tag>
                    <Text type="secondary">→</Text>
                    <CodeValue
                      actions={
                        use.readOnly ? (
                          <Tooltip title="This Service mounts the Volume read-only.">
                            <Tag icon={<LockOutlined />}>ro</Tag>
                          </Tooltip>
                        ) : undefined
                      }
                      value={use.target}
                    />
                  </Flex>
                ))}
              </Flex>
            ),
            title: 'Mounted by',
            width: 300,
          },
          {
            key: 'actions',
            render: (_value, volume) => {
              // A missing source is refused rather than recreated: mounting it
              // is what would create it, as root:root 0755. The Explorer has
              // its own gate for the unverifiable case; this row only refuses
              // what the resolution already marked missing.
              const refusal = downloadRefusal(volume);
              return (
                <Flex gap={8} justify="flex-end" wrap>
                  <Tooltip title={refusal ?? 'Opens this Volume in its own browser tab.'}>
                    <Button
                      disabled={Boolean(refusal)}
                      href={refusal ? undefined : explorerHref(volume)}
                      icon={<ExportOutlined />}
                      rel="noreferrer"
                      size="small"
                      target="_blank"
                    >
                      Open
                    </Button>
                  </Tooltip>
                </Flex>
              );
            },
            title: '',
            width: 110,
          },
        ]}
        dataSource={view.volumes}
        pagination={false}
        rowKey={(volume) => `${volume.kind}:${volume.source}`}
        size="middle"
        tableLayout="fixed"
      />

      <Paragraph style={{ marginBottom: 0, marginTop: LAYOUT.gutterSmall }} type="secondary">
        Volume list resolved from <Text code>docker compose config</Text> — a bind source is shown at its resolved host
        path, a named Volume at <Text code>/var/lib/docker/volumes/&lt;name&gt;/_data</Text>. The panel never accepts a
        host path from a form: a Volume can only be one this Compose already declares. Downloads and writes happen in
        each Volume’s Explorer, behind its Operation user Confirm.
      </Paragraph>

      {orphanSection}
    </>
  );
}

/**
 * Volumes Docker still holds under this Compose's project that its file no
 * longer declares. Each row is deleted on its own behind a typed-name dialog —
 * there is no bulk delete, because the data in an orphan is not recoverable.
 */
function OrphanVolumes({
  error,
  onDelete,
  onRetry,
  orphans,
}: {
  error?: string;
  onDelete: (name: string) => Promise<void>;
  onRetry: () => void;
  orphans?: OrphanVolume[];
}) {
  const view = orphansView(orphans && { orphans }, error);
  const [target, setTarget] = useState<string>();
  const [deleting, setDeleting] = useState(false);

  const confirm = async () => {
    if (target === undefined) {
      return;
    }
    setDeleting(true);
    try {
      await onDelete(target);
      setTarget(undefined);
    } catch {
      // The page shows the API's message (409 while the volume is in use).
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div style={{ marginTop: LAYOUT.gutter }}>
      <Text strong style={{ display: 'block', marginBottom: 4 }}>
        Orphan volumes
      </Text>
      <Paragraph type="secondary">
        Volumes Docker holds for this Compose that its file no longer declares. Nothing mounts them any more.
      </Paragraph>

      {view.status === 'loading' && <Skeleton active paragraph={{ rows: 2 }} title={false} />}

      {view.status === 'error' && (
        <Alert
          action={<Button onClick={onRetry}>Retry</Button>}
          description={view.message}
          title="Could not list orphan volumes"
          showIcon
          type="error"
        />
      )}

      {view.status === 'empty' && (
        <Card variant="outlined">
          <EmptyState
            description="Every volume Docker holds for this Compose is still declared in its file."
            title="No orphan volumes"
          />
        </Card>
      )}

      {view.status === 'ready' && (
        <Table<OrphanVolume>
          columns={[
            {
              dataIndex: 'name',
              key: 'name',
              render: (name: string) => <CodeValue copyable value={name} />,
              title: 'Volume',
            },
            {
              dataIndex: 'createdAt',
              key: 'createdAt',
              render: (createdAt: string) => <RelativeTime value={createdAt} />,
              title: 'Created',
              width: 180,
            },
            {
              key: 'actions',
              render: (_value, orphan) => (
                <Flex justify="flex-end">
                  <Button danger icon={<DeleteOutlined />} onClick={() => setTarget(orphan.name)} size="small">
                    Delete
                  </Button>
                </Flex>
              ),
              title: '',
              width: 110,
            },
          ]}
          dataSource={view.orphans}
          pagination={false}
          rowKey="name"
          size="middle"
        />
      )}

      <DangerModal
        confirmWord={target ?? ''}
        consequences={
          <Paragraph type="secondary">
            Deletes the volume <Text code>{target}</Text> from Docker. Its data is permanently lost; there is no trash.
            A volume that a container still uses is refused.
          </Paragraph>
        }
        loading={deleting}
        okText="Delete volume"
        onCancel={() => setTarget(undefined)}
        onConfirm={() => void confirm()}
        open={target !== undefined}
        title="Delete this orphan volume?"
      />
    </div>
  );
}
