import { App, Button, Card, Flex, Table, Tag, Typography } from 'antd';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, VersionChange } from '../data/types';
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
  const { message, modal } = App.useApp();
  const key = `/api/databases/${db.id}/version-changes`;
  const { data, mutate } = useSWR(key, () => api.listVersionChanges(db.id), {
    refreshInterval: (rows) => (rows?.some((row) => row.status === 'running') ? 2000 : 0),
  });
  if (!data || data.length === 0) return null;

  const remove = (row: VersionChange) =>
    void modal.confirm({
      content: `Deletes the ${row.fromVersion} data kept from before the change. It cannot be undone.`,
      okText: 'Delete previous data',
      okType: 'danger',
      onOk: async () => {
        try {
          await api.deletePreviousData(db.id, row.id);
          void message.success(`Deleted the ${row.fromVersion} data`);
        } catch (reason: unknown) {
          void message.error(reason instanceof Error ? reason.message : 'Could not delete the previous data');
        }
        await mutate();
      },
      title: 'Delete previous version data',
    });

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
                  <Button danger onClick={() => remove(row)} size="small">
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
    </Card>
  );
}
