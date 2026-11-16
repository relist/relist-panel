import { PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Modal, Popconfirm, Segmented, Select, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { Database, DatabaseGrantRow } from '../data/types';
import { RelativeTime } from '../components/primitives';

const { Text } = Typography;

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/**
 * Panel access: which panel users (not Admins) may use this Database, each as
 * one managed user (SQL engines) or at a level (Redis) (ADR-0045).
 */
export function DatabaseGrantsTab({ db }: { db: Database }) {
  const { message } = App.useApp();
  const redis = db.engine === 'redis';
  const grants = useSWR(`/api/databases/${db.id}/grants`, () => api.databaseGrants(db.id));
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState<string>();

  const remove = async (grant: DatabaseGrantRow) => {
    setError(undefined);
    try {
      await api.deleteDatabaseGrant(db.id, grant.userId);
      void message.success(`Removed ${grant.email}'s access`);
      await grants.mutate();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not remove the access'));
    }
  };

  return (
    <Flex gap={16} vertical>
      <Alert
        showIcon
        title={
          redis
            ? 'A panel user with access gets this Database’s console at their level: Read, or Read-write. They never see the superuser password.'
            : 'A panel user with access works in the Query panel as the database user you pick, with its permissions, and can reveal that user’s password. They never see the superuser password.'
        }
        type="info"
      />
      {error && <Alert closable={{ onClose: () => setError(undefined) }} showIcon title={error} type="error" />}
      <Card
        extra={
          <Button icon={<PlusOutlined />} onClick={() => setAdding(true)} type="primary">
            Add
          </Button>
        }
        title="Panel users with access"
        variant="outlined"
      >
        <Table<DatabaseGrantRow>
          columns={[
            { key: 'email', render: (_, row) => row.email, title: 'Panel user' },
            {
              key: 'as',
              render: (_, row) =>
                row.dbUser ? <Text code>{row.dbUser}</Text> : <Tag>{row.level === 'read_write' ? 'Read-write' : 'Read'}</Tag>,
              title: redis ? 'Level' : 'Database user',
            },
            { key: 'since', render: (_, row) => <RelativeTime value={row.createdAt} />, title: 'Since' },
            {
              key: 'actions',
              render: (_, row) => (
                <Popconfirm
                  description="They lose this Database at once."
                  okButtonProps={{ danger: true }}
                  okText="Remove"
                  onConfirm={() => void remove(row)}
                  title={`Remove ${row.email}'s access?`}
                >
                  <Button aria-label={`Remove ${row.email}`} danger size="small">
                    Remove
                  </Button>
                </Popconfirm>
              ),
              title: '',
            },
          ]}
          dataSource={grants.data ?? []}
          loading={grants.isLoading}
          locale={{ emptyText: grants.isLoading ? ' ' : 'No panel users have access.' }}
          pagination={false}
          rowKey="userId"
          size="small"
        />
      </Card>
      {adding && (
        <AddGrantModal
          db={db}
          onClose={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            void grants.mutate();
          }}
        />
      )}
    </Flex>
  );
}

function AddGrantModal({ db, onClose, onDone }: { db: Database; onClose: () => void; onDone: () => void }) {
  const { message } = App.useApp();
  const redis = db.engine === 'redis';
  const panelUsers = useSWR('/api/users', () => api.listUsers());
  const dbUsers = useSWR(redis ? null : `/api/databases/${db.id}/users`, () => api.listDatabaseUsers(db.id), {
    shouldRetryOnError: false,
  });
  const [userId, setUserId] = useState<number>();
  const [dbUser, setDbUser] = useState<string>();
  const [level, setLevel] = useState<'read' | 'read_write'>('read');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const members = (panelUsers.data ?? []).filter((row) => !row.isAdmin);
  const managed = (dbUsers.data?.users ?? []).filter((row) => row.managed);

  const save = async () => {
    if (userId === undefined || (!redis && !dbUser)) {
      setError(redis ? 'Pick a panel user' : 'Pick a panel user and a database user');
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await api.setDatabaseGrant(db.id, userId, redis ? { level } : { dbUser: dbUser ?? '' });
      void message.success('Access given');
      onDone();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not give access'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      confirmLoading={busy}
      destroyOnHidden
      okText="Give access"
      onCancel={onClose}
      onOk={() => void save()}
      open
      title="Give a panel user access"
    >
      <Flex gap={12} style={{ marginBlock: 16 }} vertical>
        {error && <Alert showIcon title={error} type="error" />}
        <Form.Item label="Panel user" layout="vertical" style={{ marginBottom: 0 }}>
          <Select
            aria-label="Panel user"
            onChange={(value: number) => setUserId(value)}
            options={members.map((row) => ({ label: row.email, value: row.id }))}
            placeholder="Who gets access"
            showSearch
            value={userId}
          />
        </Form.Item>
        {redis ? (
          <Form.Item label="Level" layout="vertical" style={{ marginBottom: 0 }}>
            <Segmented<'read' | 'read_write'>
              onChange={setLevel}
              options={[
                { label: 'Read', value: 'read' },
                { label: 'Read-write', value: 'read_write' },
              ]}
              value={level}
            />
          </Form.Item>
        ) : (
          <Form.Item
            extra="Only managed users can back access: the panel must know the password. Reset a user's password to manage it."
            label="Database user"
            layout="vertical"
            style={{ marginBottom: 0 }}
          >
            <Select
              aria-label="Database user"
              onChange={(value: string) => setDbUser(value)}
              options={managed.map((row) => ({ label: row.name, value: row.name }))}
              placeholder="Works as this user"
              value={dbUser}
            />
          </Form.Item>
        )}
        {!redis && (
          <Text type="secondary">They can run what this user can run, and nothing more. The Database stays otherwise Admin-only.</Text>
        )}
      </Flex>
    </Modal>
  );
}
