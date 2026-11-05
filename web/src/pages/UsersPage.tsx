import { MoreOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Dropdown, Flex, Form, Input, Modal, Switch, Table, Tag, Typography } from 'antd';
import type { MenuProps, TableColumnsType } from 'antd';
import dayjs from 'dayjs';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import useSWR from 'swr';

import { inviteUrl, paths } from '../app/paths';
import { PageHeader, RelativeTime } from '../components/primitives';
import { api } from '../data/api';
import type { IssuedLink, PanelUser, UserStatus } from '../data/api';
import { usePanel } from '../data/store';

const { Paragraph, Text } = Typography;

const USERS_KEY = '/api/users';

const statusColor: Record<UserStatus, string | undefined> = { active: 'success', disabled: 'error', invited: undefined };

/**
 * Users: who may sign in. An admin invites someone by email and hands them
 * the one-time link; there is no email delivery. The Owner (id 1) is always
 * an admin, so its row offers nothing that would take that away.
 */
export function UsersPage() {
  const { message, modal } = App.useApp();
  const { auth, loadAuthStatus } = usePanel();
  const navigate = useNavigate();
  const { data, error, isLoading, mutate } = useSWR(USERS_KEY, () => api.listUsers());
  const [inviting, setInviting] = useState(false);
  const [link, setLink] = useState<IssuedLink>();

  const reload = () => void mutate();
  const me = auth.user;

  const run = async (action: () => Promise<unknown>, after?: () => Promise<void>) => {
    try {
      await action();
      reload();
      await after?.();
    } catch (reason) {
      void message.error(reason instanceof Error ? reason.message : 'Something went wrong');
    }
  };

  const columns: TableColumnsType<PanelUser> = [
    {
      dataIndex: 'email',
      key: 'email',
      // An address broken mid-word reads as two; the table scrolls instead.
      onCell: () => ({ style: { whiteSpace: 'nowrap' } }),
      render: (email: string, user) => (
        <Link to={paths.user(user.id)}>
          <Text strong>{email}</Text>
        </Link>
      ),
      title: 'Email',
    },
    {
      dataIndex: 'status',
      key: 'status',
      render: (status: UserStatus) => <Tag color={statusColor[status]}>{status}</Tag>,
      title: 'Status',
    },
    {
      key: 'role',
      render: (_, user) => (user.isOwner ? <Tag color="gold">Owner</Tag> : user.isAdmin ? <Tag color="blue">Admin</Tag> : <Tag>Member</Tag>),
      title: 'Role',
    },
    {
      dataIndex: 'twoFactorEnabled',
      key: 'twoFactor',
      render: (on: boolean) => (on ? 'On' : 'Off'),
      title: 'Two-factor',
    },
    {
      dataIndex: 'lastLoginAt',
      key: 'lastLoginAt',
      render: (at: string | null) => (at ? <RelativeTime value={at} /> : <Text type="secondary">Never</Text>),
      title: 'Last sign-in',
    },
    {
      key: 'actions',
      render: (_, user) => {
        const self = me?.id === user.id;
        // Disabling or resetting yourself would sign you out mid-click; your
        // own password changes on the Account page. The Owner is never
        // demoted, disabled, deleted or reset from here (the API refuses it
        // too), so its row has no menu.
        if (user.isOwner) {
          return null;
        }
        const items: MenuProps['items'] = [
          // An admin sees every Project, so only a member has access to edit.
          ...(user.isAdmin ? [] : [{ key: 'access', label: 'Edit access', onClick: () => void navigate(paths.user(user.id)) }]),
          {
            key: 'admin',
            label: user.isAdmin ? 'Remove admin' : 'Make admin',
            onClick: () =>
              void run(
                () => api.updateUser(user.id, { isAdmin: !user.isAdmin }),
                self && user.isAdmin ? loadAuthStatus : undefined,
              ),
          },
          ...(self
            ? []
            : [
                {
                  key: 'disabled',
                  label: user.status === 'disabled' ? 'Enable' : 'Disable',
                  onClick: () => void run(() => api.updateUser(user.id, { disabled: user.status !== 'disabled' })),
                },
              ]),
          ...(self || user.status === 'disabled'
            ? []
            : [
                {
                  key: 'link',
                  label: user.status === 'invited' ? 'New invite link' : 'Send reset link',
                  onClick: () => void run(async () => setLink(await api.resetUser(user.id))),
                },
              ]),
          { type: 'divider' as const },
          {
            danger: true,
            key: 'delete',
            label: 'Delete',
            onClick: () =>
              void modal.confirm({
                content: 'They lose access at once. This cannot be undone.',
                okButtonProps: { danger: true },
                okText: 'Delete',
                onOk: () => run(() => api.deleteUser(user.id), self ? loadAuthStatus : undefined),
                title: `Delete ${user.email}?`,
              }),
          },
        ];
        return (
          <Dropdown menu={{ items }} placement="bottomRight" trigger={['click']}>
            <Button aria-label={`Actions for ${user.email}`} icon={<MoreOutlined />} type="text" />
          </Dropdown>
        );
      },
      title: '',
    },
  ];

  return (
    <>
      <PageHeader
        description="People who may sign in to this panel. Invite someone, then send them the link yourself."
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={reload}>
              Refresh
            </Button>
            <Button icon={<PlusOutlined />} onClick={() => setInviting(true)} type="primary">
              Invite user
            </Button>
          </Flex>
        }
        title="Users"
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load users'}
          type="error"
        />
      )}

      <Card variant="outlined">
        <Table<PanelUser>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>

      <InviteModal
        onClose={() => setInviting(false)}
        onCreated={(issued) => {
          setInviting(false);
          setLink(issued);
          reload();
        }}
        open={inviting}
      />
      <LinkModal link={link} onClose={() => setLink(undefined)} />
    </>
  );
}

function InviteModal({ onClose, onCreated, open }: { onClose: () => void; onCreated: (link: IssuedLink) => void; open: boolean }) {
  const [form] = Form.useForm<{ email: string; isAdmin: boolean }>();
  const [emailError, setEmailError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);

  const submit = async ({ email, isAdmin }: { email: string; isAdmin: boolean }) => {
    setEmailError(undefined);
    setSubmitting(true);
    try {
      const { token, expiresAt, kind } = await api.createUser(email, !!isAdmin);
      form.resetFields();
      onCreated({ token, expiresAt, kind });
    } catch (reason) {
      // A duplicate (409) or invalid (400) email, and anything else too, is
      // shown under the field: it is the only input.
      setEmailError(reason instanceof Error ? reason.message : 'Could not invite the user');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      destroyOnHidden
      okButtonProps={{ loading: submitting }}
      okText="Create invite link"
      onCancel={onClose}
      onOk={() => form.submit()}
      open={open}
      title="Invite user"
    >
      <Form form={form} initialValues={{ isAdmin: false }} layout="vertical" onFinish={submit} requiredMark={false}>
        <Form.Item
          help={emailError}
          label="Email"
          name="email"
          rules={[{ required: true, message: 'Enter an email' }]}
          validateStatus={emailError ? 'error' : undefined}
        >
          <Input autoFocus onChange={() => setEmailError(undefined)} type="email" />
        </Form.Item>
        <Form.Item extra="Admins see every Project and manage users and the host." label="Admin" name="isAdmin" valuePropName="checked">
          <Switch aria-label="Admin" />
        </Form.Item>
      </Form>
    </Modal>
  );
}

function LinkModal({ link, onClose }: { link?: IssuedLink; onClose: () => void }) {
  return (
    <Modal
      footer={<Button onClick={onClose} type="primary">Done</Button>}
      onCancel={onClose}
      open={!!link}
      title={link?.kind === 'reset' ? 'Reset link' : 'Invite link'}
    >
      {link && (
        <>
          <Paragraph copyable={{ text: inviteUrl(link.token) }} style={{ wordBreak: 'break-all' }}>
            {inviteUrl(link.token)}
          </Paragraph>
          <Paragraph type="secondary">
            This link works once and expires on {dayjs(link.expiresAt).format('D MMM YYYY, HH:mm')}. It is shown only now.
          </Paragraph>
        </>
      )}
    </Modal>
  );
}
