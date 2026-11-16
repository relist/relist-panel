import { PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Flex, Form, Input, Modal, Popconfirm, Segmented, Table, Tag, Typography } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api, ApiError } from '../data/api';
import type { Database, DatabaseAccess, DatabaseUser, LogicalDatabase } from '../data/types';
import { validTargetName } from './databases-view';

const { Text } = Typography;

function failure(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

/** The login users of a SQL Database, and what each may do in each database. */
export function DatabaseUsersTab({ db }: { db: Database }) {
  const { message } = App.useApp();
  const users = useSWR(`/api/databases/${db.id}/users`, () => api.listDatabaseUsers(db.id));
  const dbs = useSWR(`/api/databases/${db.id}/logical-databases/details`, () => api.databaseLogicalDetails(db.id));
  const postgres = db.engine === 'postgres';

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [createBusy, setCreateBusy] = useState(false);
  const [createError, setCreateError] = useState<string>();
  const [error, setError] = useState<string>();
  // A delete refused because panel users use this database user.
  const [grantsConfirm, setGrantsConfirm] = useState<{ emails: string[]; user: DatabaseUser }>();
  const [editing, setEditing] = useState<DatabaseUser>();
  const [levels, setLevels] = useState<Record<string, DatabaseAccess>>({});
  const [saveBusy, setSaveBusy] = useState(false);
  const [saveError, setSaveError] = useState<string>();

  const create = async () => {
    setCreateBusy(true);
    setCreateError(undefined);
    try {
      await api.createDatabaseUser(db.id, name.trim());
      void message.success(`Created ${name.trim()}. Reveal its password on the Connection tab.`);
      setCreating(false);
      await users.mutate();
    } catch (reason: unknown) {
      setCreateError(failure(reason, 'Could not create the user'));
    } finally {
      setCreateBusy(false);
    }
  };

  const reset = async (user: DatabaseUser) => {
    setError(undefined);
    try {
      await api.resetDatabaseUserPassword(db.id, user.name);
      void message.success(`Reset the password of ${user.name}. Reveal it on the Connection tab.`);
      await users.mutate();
    } catch (reason: unknown) {
      setError(failure(reason, 'Could not reset the password'));
    }
  };

  const remove = async (user: DatabaseUser, confirmGrants = false) => {
    setError(undefined);
    try {
      await api.deleteDatabaseUser(db.id, user.name, confirmGrants);
      void message.success(`Deleted ${user.name}`);
      setGrantsConfirm(undefined);
      await users.mutate();
    } catch (reason: unknown) {
      // A 409 naming panel users: deleting takes their access with it.
      if (reason instanceof ApiError && reason.status === 409 && reason.body && typeof reason.body === 'object' && 'users' in reason.body) {
        setGrantsConfirm({ emails: (reason.body as { users: string[] }).users, user });
        return;
      }
      setError(failure(reason, 'Could not delete the user'));
    }
  };

  const databases: LogicalDatabase[] = dbs.data?.databases ?? [];
  // What the dialog shows: the Admin's change, else the live level.
  const levelOf = (row: LogicalDatabase): DatabaseAccess => levels[row.name] ?? editing?.access[row.name] ?? 'none';
  const open = (user: DatabaseUser) => {
    setLevels({});
    setSaveError(undefined);
    setEditing(user);
  };

  const save = async () => {
    if (!editing) return;
    setSaveBusy(true);
    setSaveError(undefined);
    try {
      const all: Record<string, DatabaseAccess> = {};
      for (const row of databases) all[row.name] = postgres && row.owner === editing.name ? 'full' : levelOf(row);
      await api.setDatabaseUserAccess(db.id, editing.name, all);
      void message.success(`Saved the access of ${editing.name}`);
      setEditing(undefined);
      await users.mutate();
    } catch (reason: unknown) {
      setSaveError(failure(reason, 'Could not save the access'));
    } finally {
      setSaveBusy(false);
    }
  };

  const fullRows = databases.filter((row) => levelOf(row) === 'full' && !(postgres && row.owner === editing?.name));
  const picksFull = fullRows.length > 0;

  return (
    <Flex gap={16} vertical>
      {users.error && <Alert showIcon title={failure(users.error, 'Could not list the users')} type="error" />}
      {error && <Alert closable={{ onClose: () => setError(undefined) }} showIcon title={error} type="error" />}
      <Flex align="center" gap={8} justify="space-between" wrap>
        {postgres ? <span /> : <Text type="secondary">Only accounts on host % are shown.</Text>}
        <Button
          icon={<PlusOutlined />}
          onClick={() => {
            setName('');
            setCreateError(undefined);
            setCreating(true);
          }}
          type="primary"
        >
          Create user
        </Button>
      </Flex>
      <Table<DatabaseUser>
        columns={[
          { dataIndex: 'name', title: 'Name' },
          {
            key: 'password',
            render: (_: unknown, row) => (row.managed ? <Tag color="blue">Managed</Tag> : <Text type="secondary">password unknown</Text>),
            title: 'Password',
          },
          ...(postgres
            ? [
                {
                  dataIndex: 'owns',
                  render: (owns: string[]) => (owns.length > 0 ? owns.join(', ') : <Text type="secondary">None</Text>),
                  title: 'Owns',
                },
              ]
            : []),
          {
            align: 'right',
            key: 'actions',
            render: (_: unknown, row) => (
              <Flex gap={8} justify="flex-end" wrap>
                <Button aria-label={`Access ${row.name}`} onClick={() => open(row)} size="small">
                  Access
                </Button>
                <Popconfirm
                  description="Apps using the old password stop connecting until given the new one."
                  okText="Reset"
                  onConfirm={() => void reset(row)}
                  title={`Reset the password of ${row.name}?`}
                >
                  <Button aria-label={`Reset password ${row.name}`} size="small">
                    Reset password
                  </Button>
                </Popconfirm>
                <Popconfirm
                  description="Its grants go with it."
                  okButtonProps={{ danger: true }}
                  okText="Delete"
                  onConfirm={() => void remove(row)}
                  title={`Delete ${row.name}?`}
                >
                  <Button aria-label={`Delete ${row.name}`} danger size="small">
                    Delete
                  </Button>
                </Popconfirm>
              </Flex>
            ),
            title: '',
          },
        ]}
        dataSource={users.data?.users ?? []}
        loading={users.isLoading}
        pagination={false}
        rowKey="name"
        size="small"
      />
      <Modal
        confirmLoading={createBusy}
        destroyOnHidden
        okButtonProps={{ disabled: !validTargetName(name.trim()) }}
        okText="Create"
        onCancel={() => setCreating(false)}
        onOk={() => void create()}
        open={creating}
        title="Create user"
      >
        <Form layout="vertical">
          <Form.Item
            extra={`Letters, digits, _ and -, not starting with -, at most ${postgres ? 63 : 32}. The password is generated.`}
            htmlFor="user-name"
            label="Name"
          >
            <Input id="user-name" onChange={(event) => setName(event.target.value)} value={name} />
          </Form.Item>
          {createError && <Alert showIcon title={createError} type="error" />}
        </Form>
      </Modal>
      <Modal
        confirmLoading={saveBusy}
        destroyOnHidden
        okText="Save"
        onCancel={() => setEditing(undefined)}
        onOk={() => void save()}
        open={editing !== undefined}
        title={`Access of ${editing?.name ?? ''}`}
      >
        <Flex gap={12} vertical>
          <Text type="secondary">
            Saving applies the grants again, which also picks up schemas and tables added since.
          </Text>
          {databases.length === 0 && <Text type="secondary">There are no databases yet.</Text>}
          {databases.map((row) => {
            const owner = postgres && editing !== undefined && row.owner === editing.name;
            const superOwned = postgres && row.owner === db.user;
            return (
              <Flex align="center" gap={12} justify="space-between" key={row.name} wrap>
                <Text strong>{row.name}</Text>
                <div aria-label={`Access to ${row.name}`} role="group">
                  <Segmented<DatabaseAccess>
                    disabled={owner}
                    onChange={(value) => setLevels((current) => ({ ...current, [row.name]: value }))}
                    options={[
                      { label: 'None', value: 'none' },
                      { label: 'Read-only', value: 'read' },
                      {
                        disabled: superOwned,
                        label: 'Full',
                        tooltip: superOwned
                          ? `${row.name} is owned by a superuser, so Full would make the user one. Give it Read-only, or create the database with an owning user.`
                          : undefined,
                        value: 'full',
                      },
                    ]}
                    value={owner ? 'full' : levelOf(row)}
                  />
                </div>
              </Flex>
            );
          })}
          {postgres && picksFull && (
            <Alert
              showIcon
              description={fullRows.map((row) => (
                <div key={row.name}>
                  Inside {row.name} the user acts as {row.owner}, so what it creates belongs to {row.owner}.
                </div>
              ))}
              title="A member gets everything the owner role has, in every database it owns."
              type="warning"
            />
          )}
          {saveError && <Alert showIcon title={saveError} type="error" />}
        </Flex>
      </Modal>
      <Modal
        destroyOnHidden
        okButtonProps={{ danger: true }}
        okText="Delete"
        onCancel={() => setGrantsConfirm(undefined)}
        onOk={() => void (grantsConfirm && remove(grantsConfirm.user, true))}
        open={Boolean(grantsConfirm)}
        title={grantsConfirm ? `Delete ${grantsConfirm.user.name}?` : ''}
      >
        <Text>
          {grantsConfirm?.user.name} backs the panel access of {grantsConfirm?.emails.join(', ')}. Deleting it deletes
          their access to this Database too.
        </Text>
      </Modal>
    </Flex>
  );
}
