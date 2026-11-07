import { DeleteOutlined, DownOutlined, RightOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Modal, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import { Link } from 'react-router';
import useSWR, { useSWRConfig } from 'swr';

import { paths } from '../app/paths';
import { api } from '../data/api';
import type { RegistryRepository, RegistryTag } from '../data/types';
import { sharedTags, shortDigest } from '../pages/registry-view';
import { DangerModal } from './DangerModal';
import { formatBytes } from './MetricChart';
import { EmptyState, RelativeTime } from './primitives';
import { REGISTRY_KEY, RegistryOffState } from './RegistryOffState';

const { Text } = Typography;

export const REGISTRY_REPOSITORIES_KEY = '/api/registry/repositories';
const tagsKey = (repo: string) => `/api/registry/tags?repo=${encodeURIComponent(repo)}`;

/**
 * Repositories: what CI pushed. Size counts shared layers in every tag, so the
 * volume's real usage sits beside it as Disk used.
 */
export function RegistryRepositoriesTab() {
  const registry = useSWR(REGISTRY_KEY, () => api.getRegistry());
  const enabled = registry.data?.settings.enabled === true;
  const { data, error, isLoading } = useSWR(enabled ? REGISTRY_REPOSITORIES_KEY : null, () => api.listRegistryRepositories());

  if (registry.data && !enabled) return <RegistryOffState />;
  const hostname = registry.data?.settings.hostname ?? '';
  const loadError = error ?? registry.error;

  const columns: TableColumnsType<RegistryRepository> = [
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text strong>{name}</Text>, title: 'Repository' },
    { dataIndex: 'tagCount', key: 'tagCount', title: 'Tags' },
    {
      dataIndex: 'newest',
      key: 'newest',
      render: (newest: string | null) => (newest ? <RelativeTime value={newest} /> : <Text type="secondary">—</Text>),
      title: 'Newest',
    },
  ];

  return (
    <Flex gap={16} vertical>
      {loadError && (
        <Alert showIcon title={loadError instanceof Error ? loadError.message : 'Could not load repositories'} type="error" />
      )}
      <Card
        extra={
          registry.data && (
            <Flex gap={6}>
              <Text type="secondary">Disk used</Text>
              <Text strong>{formatBytes(registry.data.diskBytes)}</Text>
            </Flex>
          )
        }
        title="Repositories"
        variant="outlined"
      >
        <Table<RegistryRepository>
          columns={columns}
          dataSource={data ?? []}
          expandable={{
            expandIcon: ({ expanded, onExpand, record }) => (
              <Button
                aria-label={`${expanded ? 'Hide' : 'Show'} tags of ${record.name}`}
                icon={expanded ? <DownOutlined /> : <RightOutlined />}
                onClick={(event) => onExpand(record, event)}
                size="small"
                type="text"
              />
            ),
            expandedRowRender: (record) => <RepositoryTags hostname={hostname} repo={record.name} />,
          }}
          loading={isLoading || registry.isLoading}
          locale={{
            emptyText:
              isLoading || loadError ? (
                ' '
              ) : (
                <EmptyState
                  action={<Link to={`${paths.registry}?tab=credentials`}>Create a Push credential</Link>}
                  description={`Push an image to ${hostname || 'the Registry'} from CI and it appears here.`}
                  title="No repositories yet"
                />
              ),
          }}
          pagination={false}
          rowKey="name"
          scroll={{ x: 'max-content' }}
        />
      </Card>
    </Flex>
  );
}

function RepositoryTags({ hostname, repo }: { hostname: string; repo: string }) {
  const { data, error, isLoading, mutate } = useSWR(tagsKey(repo), () => api.listRegistryTags(repo));
  const { mutate: mutateGlobal } = useSWRConfig();
  const [deleting, setDeleting] = useState<RegistryTag>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();
  const tags = data ?? [];

  // The server checks every tag on the digest, so a sibling in use needs the confirm too.
  const remove = async (tag: RegistryTag, confirm: boolean) => {
    setBusy(true);
    setFailure(undefined);
    try {
      await api.deleteRegistryTag(repo, tag.tag, confirm ? tag.tag : undefined);
      setDeleting(undefined);
      void mutate();
      void mutateGlobal(REGISTRY_REPOSITORIES_KEY);
      void mutateGlobal(REGISTRY_KEY);
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not delete the tag');
      setDeleting(undefined);
    } finally {
      setBusy(false);
    }
  };

  const columns: TableColumnsType<RegistryTag> = [
    {
      dataIndex: 'tag',
      key: 'tag',
      render: (tag: string, row) => (
        <Flex align="center" gap={6}>
          <Text copyable={{ text: `${hostname}/${repo}:${tag}`, tooltips: ['Copy image reference', 'Copied'] }} strong>
            {tag}
          </Text>
          {row.kept && <Tag color="blue">Kept</Tag>}
        </Flex>
      ),
      title: 'Tag',
    },
    { dataIndex: 'digest', key: 'digest', render: (digest: string) => <Text code>{shortDigest(digest)}</Text>, title: 'Digest' },
    { dataIndex: 'size', key: 'size', render: (size: number) => formatBytes(size), title: 'Size' },
    {
      dataIndex: 'created',
      key: 'created',
      render: (created: string | null) => (created ? <RelativeTime value={created} /> : <Text type="secondary">—</Text>),
      title: 'Created',
    },
    {
      key: 'inUseBy',
      render: (_, row) =>
        row.inUseBy.length === 0 ? (
          <Text type="secondary">—</Text>
        ) : (
          <Flex gap={8} wrap>
            {row.inUseBy.map((u) =>
              u.composeName ? (
                <Link key={u.composeId} to={paths.compose(u.projectId, u.environmentId, u.composeId)}>
                  {u.composeName}
                </Link>
              ) : (
                <Text key={u.composeId}>Compose {u.composeId}</Text>
              ),
            )}
          </Flex>
        ),
      title: 'In use by',
    },
    {
      align: 'right',
      key: 'actions',
      render: (_, row) => (
        <Button aria-label={`Delete ${repo}:${row.tag}`} danger icon={<DeleteOutlined />} onClick={() => setDeleting(row)} type="text" />
      ),
      title: '',
    },
  ];

  const others = deleting ? sharedTags(tags, deleting.tag).slice(1) : [];
  const users = deleting
    ? tags.filter((t) => t.digest === deleting.digest).flatMap((t) => t.inUseBy.map((u) => u.composeName || `Compose ${u.composeId}`))
    : [];
  const inUse = [...new Set(users)];
  const description = (
    <>
      Deleting removes the manifest, so every tag pointing at it goes too. Its layers are freed by the next cleanup.
      {others.length > 0 && <Text strong style={{ display: 'block', marginTop: 8 }}>Also deletes {others.join(', ')}.</Text>}
    </>
  );

  return (
    <Flex gap={8} vertical>
      {(error || failure) && (
        <Alert showIcon title={failure ?? (error instanceof Error ? error.message : 'Could not load tags')} type="error" />
      )}
      <Table<RegistryTag>
        columns={columns}
        dataSource={tags}
        loading={isLoading}
        pagination={false}
        rowKey="tag"
        scroll={{ x: 'max-content' }}
        size="small"
      />
      {deleting && inUse.length > 0 ? (
        <DangerModal
          confirmWord={deleting.tag}
          consequences={<Alert showIcon title={`In use by ${inUse.join(', ')}. Their next deploy will fail to pull it.`} type="warning" />}
          description={description}
          loading={busy}
          okText="Delete tag"
          onCancel={() => setDeleting(undefined)}
          onConfirm={() => void remove(deleting, true)}
          open
          title={`Delete ${repo}:${deleting.tag}?`}
        />
      ) : (
        <Modal
          confirmLoading={busy}
          okButtonProps={{ danger: true }}
          okText="Delete tag"
          onCancel={() => setDeleting(undefined)}
          onOk={() => deleting && void remove(deleting, false)}
          open={deleting !== undefined}
          title={deleting ? `Delete ${repo}:${deleting.tag}?` : ''}
        >
          {description}
        </Modal>
      )}
    </Flex>
  );
}
