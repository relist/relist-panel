import { PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Checkbox, Flex, Form, Input, Modal, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { DangerModal } from '../components/DangerModal';
import { api } from '../data/api';
import type { Database, LogicalDatabase } from '../data/types';
import { formatBytes, validTargetName } from './databases-view';

const { Text } = Typography;

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/** The databases inside a SQL Database: create one with its own user, restrict, drop. */
export function DatabaseDatabasesTab({ db }: { db: Database }) {
  const { message } = App.useApp();
  const key = `/api/databases/${db.id}/logical-databases/details`;
  const list = useSWR(key, () => api.databaseLogicalDetails(db.id), { refreshInterval: db.job ? 2000 : 15000 });
  const schedule = useSWR(`/api/databases/${db.id}/schedule`, () => api.getDatabaseSchedule(db.id));
  const postgres = db.engine === 'postgres';

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [createOwner, setCreateOwner] = useState(true);
  const [createBusy, setCreateBusy] = useState(false);
  const [createError, setCreateError] = useState<string>();
  const [dropping, setDropping] = useState<LogicalDatabase>();
  const [backupFirst, setBackupFirst] = useState(true);
  const [dropBusy, setDropBusy] = useState(false);
  const [error, setError] = useState<string>();

  const canBackup = db.status === 'running' && !db.job;

  const create = async () => {
    setCreateBusy(true);
    setCreateError(undefined);
    try {
      await api.createLogicalDatabase(db.id, name.trim(), createOwner);
      void message.success(createOwner ? `Created ${name.trim()} and its user` : `Created ${name.trim()}`);
      setCreating(false);
      await list.mutate();
    } catch (reason: unknown) {
      setCreateError(failure(reason, 'Could not create the database'));
    } finally {
      setCreateBusy(false);
    }
  };

  const restrict = async (row: LogicalDatabase) => {
    setError(undefined);
    try {
      await api.restrictLogicalDatabase(db.id, row.name);
      void message.success(`Restricted ${row.name}`);
      await list.mutate();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not restrict the database'));
    }
  };

  const drop = async () => {
    if (!dropping) return;
    setDropBusy(true);
    setError(undefined);
    const withBackup = canBackup && backupFirst;
    try {
      await api.dropLogicalDatabase(db.id, dropping.name, dropping.name, withBackup);
      void message.success(withBackup ? `Backing up ${dropping.name}, then dropping it` : `Dropped ${dropping.name}`);
      setDropping(undefined);
      await list.mutate();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not drop the database'));
      setDropping(undefined);
    } finally {
      setDropBusy(false);
    }
  };

  const sched = schedule.data;
  const inSchedule = Boolean(dropping && sched && !sched.all && sched.databases.includes(dropping.name));
  const rows = list.data?.databases ?? [];

  return (
    <Flex gap={16} vertical>
      {list.error && <Alert showIcon title={failure(list.error, 'Could not list the databases')} type="error" />}
      {error && <Alert closable={{ onClose: () => setError(undefined) }} showIcon title={error} type="error" />}
      <Flex justify="flex-end">
        <Button
          icon={<PlusOutlined />}
          onClick={() => {
            setName('');
            setCreateOwner(true);
            setCreateError(undefined);
            setCreating(true);
          }}
          type="primary"
        >
          Create database
        </Button>
      </Flex>
      <Table<LogicalDatabase>
        columns={[
          {
            dataIndex: 'name',
            render: (value: string, row) => (
              <Flex align="center" gap={8} wrap>
                <Text>{value}</Text>
                {postgres && row.anyUserCanConnect && <Tag color="orange">Any user can connect</Tag>}
              </Flex>
            ),
            title: 'Name',
          },
          { dataIndex: 'sizeBytes', render: (value: number) => formatBytes(value), title: 'Size' },
          postgres
            ? { dataIndex: 'owner', title: 'Owner' }
            : { dataIndex: 'charset', title: 'Character set' },
          {
            align: 'right',
            key: 'actions',
            render: (_: unknown, row) => (
              <Flex gap={8} justify="flex-end">
                {postgres && row.anyUserCanConnect && (
                  <Button aria-label={`Restrict ${row.name}`} onClick={() => void restrict(row)} size="small">
                    Restrict
                  </Button>
                )}
                {!row.protected && (
                  <Button
                    aria-label={`Drop ${row.name}`}
                    danger
                    disabled={Boolean(db.job)}
                    onClick={() => {
                      setBackupFirst(true);
                      setDropping(row);
                    }}
                    size="small"
                  >
                    Drop
                  </Button>
                )}
              </Flex>
            ),
            title: '',
          },
        ]}
        dataSource={rows}
        loading={list.isLoading}
        pagination={false}
        rowKey="name"
        size="small"
      />
      {postgres && rows.some((row) => row.anyUserCanConnect) && (
        <Text type="secondary">
          Any user can connect: Postgres's default. A user still sees no tables without grants. Restrict revokes CONNECT
          from everyone but the owner and the users given access.
        </Text>
      )}
      <Modal
        confirmLoading={createBusy}
        destroyOnHidden
        okButtonProps={{ disabled: !validTargetName(name.trim()) }}
        okText="Create"
        onCancel={() => setCreating(false)}
        onOk={() => void create()}
        open={creating}
        title="Create database"
      >
        <Form layout="vertical">
          <Form.Item extra="Letters, digits, _ and -, at most 63, not starting with -." htmlFor="logical-name" label="Name">
            <Input id="logical-name" onChange={(event) => setName(event.target.value)} value={name} />
          </Form.Item>
          <Checkbox checked={createOwner} onChange={(event) => setCreateOwner(event.target.checked)}>
            Create a user that owns it
          </Checkbox>
          {createOwner && (
            <Text style={{ display: 'block', marginTop: 4 }} type="secondary">
              A user of the same name is made with a generated password and Full access. Reveal it on the Connection tab.
            </Text>
          )}
          {createError && <Alert showIcon style={{ marginTop: 16 }} title={createError} type="error" />}
        </Form>
      </Modal>
      <DangerModal
        confirmWord={dropping?.name ?? ''}
        consequences={
          <Flex gap={8} vertical>
            <div>
              <Checkbox checked={canBackup && backupFirst} disabled={!canBackup} onChange={(event) => setBackupFirst(event.target.checked)}>
                Back up first
              </Checkbox>
              {!canBackup && (
                <Text style={{ display: 'block', marginTop: 4 }} type="secondary">
                  Start the Database to back up first
                </Text>
              )}
            </div>
            {inSchedule && <Text>It is also removed from the Backup schedule.</Text>}
          </Flex>
        }
        description="Drops the database and everything in it. Apps connected to it are disconnected."
        loading={dropBusy}
        okText="Drop database"
        onCancel={() => setDropping(undefined)}
        onConfirm={() => void drop()}
        open={dropping !== undefined}
        title={`Drop ${dropping?.name ?? ''}?`}
      />
    </Flex>
  );
}
