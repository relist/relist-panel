import { CopyOutlined, DeleteOutlined, EditOutlined, EyeInvisibleOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Popconfirm, Table, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import { Link, useParams } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { AuthGroupUserModal } from '../components/AuthGroupUserModal';
import { PageHeader, RelativeTime } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { AuthGroupUser } from '../data/types';

const { Text } = Typography;

const MASK = '••••••••';

function asApiError(reason: unknown): ApiError {
  if (reason instanceof ApiError) return reason;
  return new ApiError(0, reason instanceof Error ? reason.message : 'Something went wrong');
}

type Dialog = { kind: 'add' } | { kind: 'edit'; user: AuthGroupUser } | undefined;

/**
 * One Basic auth group: its name, its users (passwords masked until an Admin
 * reveals one, which is audited) and the Domains that use it.
 */
export function BasicAuthGroupPage() {
  const { message } = App.useApp();
  const groupId = Number(useParams().groupId);
  const key = `/api/auth-groups/${groupId}`;
  const { data, error, isLoading, mutate } = useSWR(key, () => api.getAuthGroup(groupId));
  const [dialog, setDialog] = useState<Dialog>();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<ApiError>();
  const [revealed, setRevealed] = useState<Record<number, string>>({});

  const reload = () => void mutate();
  const close = () => {
    setSaveError(undefined);
    setDialog(undefined);
  };

  const submit = async (input: { username: string; password?: string }) => {
    setSaving(true);
    setSaveError(undefined);
    try {
      if (dialog?.kind === 'edit') {
        await api.updateAuthGroupUser(groupId, dialog.user.id, input);
        // A shown password may be stale now.
        setRevealed(({ [dialog.user.id]: _stale, ...rest }) => rest);
      } else {
        await api.addAuthGroupUser(groupId, { password: input.password ?? '', username: input.username });
      }
      close();
      reload();
    } catch (reason: unknown) {
      const failure = asApiError(reason);
      setSaveError(failure);
      // A 5xx may come after the change was stored; show it.
      if (failure.status >= 500) reload();
    } finally {
      setSaving(false);
    }
  };

  const rename = async (name: string) => {
    const next = name.trim();
    if (!next || next === data?.name) return;
    try {
      await api.renameAuthGroup(groupId, next);
      reload();
    } catch (reason: unknown) {
      void message.error(asApiError(reason).message);
    }
  };

  const toggleReveal = async (user: AuthGroupUser) => {
    if (revealed[user.id] !== undefined) {
      setRevealed(({ [user.id]: _hidden, ...rest }) => rest);
      return;
    }
    try {
      const password = await api.revealAuthGroupPassword(groupId, user.id);
      setRevealed((current) => ({ ...current, [user.id]: password }));
    } catch (reason: unknown) {
      void message.error(asApiError(reason).message);
    }
  };

  const copy = async (user: AuthGroupUser) => {
    try {
      const password = revealed[user.id] ?? (await api.revealAuthGroupPassword(groupId, user.id));
      await navigator.clipboard.writeText(password);
      void message.success('Copied');
    } catch (reason: unknown) {
      void message.error(asApiError(reason).message);
    }
  };

  const remove = async (user: AuthGroupUser) => {
    try {
      await api.deleteAuthGroupUser(groupId, user.id);
      reload();
    } catch (reason: unknown) {
      void message.error(asApiError(reason).message);
    }
  };

  const columns: TableColumnsType<AuthGroupUser> = [
    { dataIndex: 'username', key: 'username', render: (username: string) => <Text strong>{username}</Text>, title: 'Username' },
    {
      key: 'password',
      render: (_, user) => (
        <Flex align="center" gap={4}>
          <Text code>{revealed[user.id] ?? MASK}</Text>
          <Button
            aria-label={`${revealed[user.id] === undefined ? 'Show' : 'Hide'} password of ${user.username}`}
            icon={revealed[user.id] === undefined ? <EyeOutlined /> : <EyeInvisibleOutlined />}
            onClick={() => void toggleReveal(user)}
            type="text"
          />
          <Button aria-label={`Copy password of ${user.username}`} icon={<CopyOutlined />} onClick={() => void copy(user)} type="text" />
        </Flex>
      ),
      title: 'Password',
    },
    { dataIndex: 'updatedAt', key: 'updatedAt', render: (value: string) => <RelativeTime value={value} />, title: 'Updated' },
    {
      align: 'right',
      key: 'actions',
      render: (_, user) => (
        <Flex gap={4} justify="flex-end">
          <Button
            aria-label={`Edit ${user.username}`}
            icon={<EditOutlined />}
            onClick={() => {
              setSaveError(undefined);
              setDialog({ kind: 'edit', user });
            }}
            type="text"
          />
          <Popconfirm
            description="Traefik stops accepting this user at once."
            okButtonProps={{ danger: true }}
            okText="Remove"
            onConfirm={() => remove(user)}
            title={`Remove ${user.username}?`}
          >
            <Button aria-label={`Remove ${user.username}`} danger icon={<DeleteOutlined />} type="text" />
          </Popconfirm>
        </Flex>
      ),
      title: '',
    },
  ];

  const name = data?.name ?? 'Group';
  return (
    <>
      <PageHeader
        breadcrumb={[
          { title: <Link to={paths.overview}>Overview</Link> },
          { title: <Link to={paths.basicAuth}>Basic auth</Link> },
          { title: name },
        ]}
        description="Traefik asks for a username and password of this group before serving a domain that uses it. Changes to users apply immediately."
        extra={
          <Button
            disabled={!data}
            icon={<PlusOutlined />}
            onClick={() => {
              setSaveError(undefined);
              setDialog({ kind: 'add' });
            }}
            type="primary"
          >
            Add user
          </Button>
        }
        title={data ? <Typography.Text editable={{ onChange: (value) => void rename(value), text: name }}>{name}</Typography.Text> : name}
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load the group'}
          type="error"
        />
      )}

      <Card style={{ marginBottom: 16 }} title="Users" variant="outlined">
        <Table<AuthGroupUser>
          columns={columns}
          dataSource={data?.users ?? []}
          loading={isLoading}
          locale={{ emptyText: isLoading || error ? ' ' : 'No users yet. Until you add one, nobody can sign in to a domain that uses this group.' }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>

      <Card title="Used by" variant="outlined">
        {data && data.domains.length === 0 ? (
          <Text type="secondary">No domain uses this group yet.</Text>
        ) : (
          <ul style={{ margin: 0, paddingInlineStart: 20 }}>
            {data?.domains.map((domain) => (
              <li key={domain.id}>
                <Link to={paths.compose(domain.projectId, domain.environmentId, domain.composeId, 'domains')}>
                  {domain.host}
                  {domain.path === '/' ? '' : domain.path}
                </Link>{' '}
                <Text type="secondary">
                  {domain.projectName} / {domain.environmentName} / {domain.composeName}
                </Text>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <AuthGroupUserModal
        error={saveError}
        onCancel={close}
        onSubmit={(input) => void submit(input)}
        open={dialog !== undefined}
        saving={saving}
        user={dialog?.kind === 'edit' ? dialog.user : undefined}
      />
    </>
  );
}
