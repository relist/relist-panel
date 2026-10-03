import { CloudDownloadOutlined, ExportOutlined, LockOutlined, RocketOutlined } from '@ant-design/icons';
import { useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Flex,
  Input,
  Modal,
  Result,
  Select,
  Skeleton,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import { CODE_FONT_STACK } from '../app/theme';
import type { ComposeVolume, ComposeVolumes, VolumeAccess } from '../data/types';
import { CodeValue, EmptyState } from './primitives';
import {
  downloadRefusal,
  resolvedVolumePath,
  volumeAccessReady,
  volumeIdentityValid,
  volumeLabel,
  volumesView,
} from './volumes-view';

const { Text, Paragraph } = Typography;

/**
 * The Compose screen's Volumes tab (GUIDELINES §4.7): the read-only launcher
 * over this Compose's resolved Volumes, listed with kind, source, resolved
 * path and every Service's container-side target.
 *
 * The page owns the one fetch — `available: false` hides the whole tab, which
 * is a page-level decision — so this component receives the resolution rather
 * than asking for it, and a re-render never refetches. Every row offers the
 * Volume Explorer (its own route, opened in a new browser tab) and the
 * whole-tree archive; nothing here writes.
 */
export function ComposeVolumesTab({
  archiveHref,
  deploying,
  error,
  explorerHref,
  onDeploy,
  onRetry,
  payload,
}: {
  /** Builds the direct download URL for one Volume's whole-tree tar.gz. */
  archiveHref: (volume: ComposeVolume, access: VolumeAccess) => string;
  /** The page's deploy is in flight, so the state's affordance spins with it. */
  deploying: boolean;
  error?: string;
  /** Builds the Volume Explorer's URL for one Volume. */
  explorerHref: (volume: ComposeVolume) => string;
  onDeploy: () => void;
  onRetry: () => void;
  payload?: ComposeVolumes;
}) {
  const [download, setDownload] = useState<ComposeVolume>();
  const [service, setService] = useState('');
  const [override, setOverride] = useState('');
  const [rootConfirmed, setRootConfirmed] = useState(false);
  const [bindConfirmed, setBindConfirmed] = useState(false);
  const users = (payload?.services ?? []).filter((candidate) =>
    download?.uses.some((use) => use.service === candidate.service),
  );
  const chosen = users.find((user) => user.service === service);
  const user = service === '__override' ? override : chosen?.user;
  const valid = volumeIdentityValid(user);
  const root = valid && Number(user?.split(':')[0]) === 0;
  const bind = download?.preflight.status === 'unverifiable';
  const allowed = volumeAccessReady(user, Boolean(bind), { rootConfirmed, bindConfirmed });
  const access = {
    ...(service === '__override' ? { user: override } : { service }),
    rootConfirmed,
    bindConfirmed,
  };
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
        message="Volume files are unavailable"
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
        message="Could not resolve this Compose’s Volumes"
        showIcon
        type="error"
      />
    );
  }

  if (view.status === 'empty') {
    return (
      <Card variant="outlined">
        <EmptyState
          description="This Compose declares no volumes and no bind mounts. Anything it writes lives inside its container, and goes away with it."
          title="Nothing mounted"
        />
      </Card>
    );
  }

  return (
    <>
      <Modal
        footer={null}
        onCancel={() => setDownload(undefined)}
        open={download !== undefined}
        title="Download Volume archive"
      >
        <Flex gap={12} vertical>
          <Select
            onChange={(value) => {
              setService(value);
              setRootConfirmed(false);
              setBindConfirmed(false);
            }}
            options={[
              ...users.map((candidate) => ({
                value: candidate.service,
                label: `${candidate.service} — ${candidate.user}`,
              })),
              { value: '__override', label: 'Numeric uid:gid override' },
            ]}
            placeholder="Choose a mounting Service or numeric uid:gid"
            value={service || undefined}
          />
          {service === '__override' && (
            <Input
              onChange={(event) => {
                setOverride(event.target.value);
                setRootConfirmed(false);
                setBindConfirmed(false);
              }}
              placeholder="1000:1000"
              value={override}
            />
          )}
          {chosen?.warning && <Alert message={chosen.warning} showIcon type="warning" />}
          {root && (
            <Checkbox checked={rootConfirmed} onChange={(event) => setRootConfirmed(event.target.checked)}>
              Use root (UID 0). Files created by these operations will be root-owned.
            </Checkbox>
          )}
          {bind && (
            <Checkbox checked={bindConfirmed} onChange={(event) => setBindConfirmed(event.target.checked)}>
              Mount this unverifiable bind. If missing, Docker creates it as root:root 0755.{' '}
              {download?.preflight.warning}
            </Checkbox>
          )}
          <Button
            disabled={!allowed}
            href={allowed && download ? archiveHref(download, access) : undefined}
            type="primary"
          >
            Download tar.gz
          </Button>
        </Flex>
      </Modal>
      {view.warnings.map((warning) => (
        <Alert
          description={warning.message}
          key={`${warning.status}:${warning.volume.source}`}
          message={
            warning.status === 'missing'
              ? `${volumeLabel(warning.volume)} — the source is missing`
              : `${volumeLabel(warning.volume)} — the panel cannot check this path`
          }
          showIcon
          style={{ marginBottom: 12 }}
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
              <Flex gap={6} vertical>
                {volume.uses.map((use) => (
                  <Flex align="center" gap={6} key={`${use.service}:${use.target}`}>
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
                <Flex gap={4} justify="flex-end" wrap>
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
                  <Tooltip title={refusal ?? 'Downloads the whole Volume as a tar.gz archive, straight to disk.'}>
                    <Button
                      disabled={Boolean(refusal)}
                      onClick={() => {
                        setDownload(volume);
                        setService('');
                        setOverride('');
                        setRootConfirmed(false);
                        setBindConfirmed(false);
                      }}
                      icon={<CloudDownloadOutlined />}
                      size="small"
                    >
                      Download tar.gz
                    </Button>
                  </Tooltip>
                </Flex>
              );
            },
            title: '',
            width: 260,
          },
        ]}
        dataSource={view.volumes}
        pagination={false}
        rowKey={(volume) => `${volume.kind}:${volume.source}`}
        size="middle"
        tableLayout="fixed"
      />

      <Paragraph style={{ marginBottom: 0, marginTop: 12 }} type="secondary">
        Volume list resolved from <Text code>docker compose config</Text> — a bind source is shown at its resolved host
        path, a named Volume at <Text code>/var/lib/docker/volumes/&lt;name&gt;/_data</Text>. The panel never accepts a
        host path from a form: a Volume can only be one this Compose already declares.
      </Paragraph>
    </>
  );
}
