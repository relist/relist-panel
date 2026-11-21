import { Alert, App, Button, Card, Flex, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, VersionChange } from '../data/types';
import { DangerModal } from './DangerModal';
import { RelativeTime } from './primitives';

const { Text } = Typography;

const STATUS: Record<VersionChange['status'], { color: string; label: string }> = {
  failed: { color: 'error', label: 'Failed' },
  running: { color: 'processing', label: 'Running' },
  succeeded: { color: 'success', label: 'Succeeded' },
};

/**
 * A Database's version changes, with their step logs, and the previous
 * version data a Postgres change kept until the Admin deletes it. Nothing
 * renders before the first change.
 */
export function DatabaseVersionChangesCard({ db }: { db: Database }) {
  const { message } = App.useApp();
  const key = `/api/databases/${db.id}/version-changes`;
  const { data, mutate } = useSWR(key, () => api.listVersionChanges(db.id), {
    refreshInterval: (rows) => (rows?.some((row) => row.status === 'running') ? 2000 : 0),
  });
  // The change whose previous version data the Admin is deleting: a volume,
  // so the volume's name is typed (GUIDELINES §8).
  const [deleting, setDeleting] = useState<VersionChange>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  if (!data || data.length === 0) return null;

  const remove = async (row: VersionChange) => {
    setBusy(true);
    setError(undefined);
    try {
      await api.deletePreviousData(db.id, row.id);
      void message.success(`Deleted the ${row.fromVersion} data`);
      setDeleting(undefined);
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not delete the previous data');
    } finally {
      setBusy(false);
      await mutate();
    }
  };

  return (
    <Card title="Version changes" variant="outlined">
      <Table<VersionChange>
        columns={[
          { key: 'change', render: (_, row) => `${row.fromVersion} → ${row.toVersion}`, title: 'Change' },
          { key: 'method', render: (_, row) => (row.method === 'dump_restore' ? 'Dump and restore' : 'In place'), title: 'Method' },
          {
            key: 'status',
            render: (_, row) => <Tag color={STATUS[row.status].color}>{STATUS[row.status].label}</Tag>,
            title: 'Status',
          },
          { key: 'started', render: (_, row) => <RelativeTime value={row.startedAt} />, title: 'Started' },
          { key: 'error', render: (_, row) => row.error && <Text type="danger">{row.error}</Text>, title: 'Error' },
          {
            key: 'previous',
            render: (_, row) =>
              row.previousData && (
                <Flex align="center" gap={8} wrap>
                  <Text code>{row.previousData}</Text>
                  <Button
                    danger
                    onClick={() => {
                      setError(undefined);
                      setDeleting(row);
                    }}
                    size="small"
                  >
                    Delete
                  </Button>
                </Flex>
              ),
            title: 'Previous version data',
          },
        ]}
        dataSource={data}
        expandable={{
          expandedRowRender: (row) => <pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{row.log}</pre>,
          rowExpandable: (row) => row.log !== '',
        }}
        pagination={false}
        rowKey="id"
        scroll={{ x: 'max-content' }}
        size="small"
      />
      <DangerModal
        confirmWord={deleting?.previousData ?? ''}
        consequences={error && <Alert showIcon title={error} type="error" />}
        description={deleting && `Deletes the ${deleting.fromVersion} data kept from before the change. It cannot be undone.`}
        loading={busy}
        okText="Delete previous data"
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => deleting && void remove(deleting)}
        open={deleting !== undefined}
        title="Delete previous version data"
      />
    </Card>
  );
}
