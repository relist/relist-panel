import { DeleteOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Form, Input, Modal, Popconfirm, Table, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { EmptyState, PageHeader } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { AuthGroup, AuthGroupDomain } from '../data/types';
import { inUseDomains, usedByLabel } from './basic-auth-view';

const { Text } = Typography;

const GROUPS_KEY = '/api/auth-groups';
const DESCRIPTION =
  'Traefik asks for a username and password of the group before serving a domain that uses it. Changes to users apply immediately.';

function asApiError(reason: unknown): ApiError {
  if (reason instanceof ApiError) return reason;
  return new ApiError(0, reason instanceof Error ? reason.message : 'Something went wrong');
}

type Refusal = { group: AuthGroup; message: string; domains: AuthGroupDomain[] };

/**
 * Basic auth: the host-wide groups of usernames and passwords a Domain can
 * require (ADR-0036). Admin only; the Domain form only picks one.
 */
export function BasicAuthPage() {
  const navigate = useNavigate();
  const { data, error, isLoading, mutate } = useSWR(GROUPS_KEY, () => api.listAuthGroups());
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<ApiError>();
  const [refusal, setRefusal] = useState<Refusal>();
  const [form] = Form.useForm<{ name: string }>();

  const reload = () => void mutate();
  const openCreate = () => {
    setSaveError(undefined);
    setCreating(true);
  };
  const close = () => {
    setSaveError(undefined);
    setCreating(false);
  };

  const create = async (name: string) => {
    setSaving(true);
    setSaveError(undefined);
    try {
      const group = await api.createAuthGroup({ name: name.trim() });
      close();
      reload();
      void navigate(paths.basicAuthGroup(group.id));
    } catch (reason: unknown) {
      setSaveError(asApiError(reason));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (group: AuthGroup) => {
    setRefusal(undefined);
    try {
      await api.deleteAuthGroup(group.id);
      reload();
    } catch (reason: unknown) {
      const failure = asApiError(reason);
      setRefusal({ domains: inUseDomains(failure), group, message: failure.message });
    }
  };

  const columns: TableColumnsType<AuthGroup> = [
    {
      dataIndex: 'name',
      key: 'name',
      render: (name: string, group) => (
        <Link onClick={(event) => event.stopPropagation()} to={paths.basicAuthGroup(group.id)}>
          <Text strong>{name}</Text>
        </Link>
      ),
      title: 'Name',
    },
    { align: 'right', dataIndex: 'userCount', key: 'userCount', title: 'Users', width: 120 },
    { align: 'right', dataIndex: 'domainCount', key: 'domainCount', title: 'Domains', width: 120 },
    {
      align: 'right',
      key: 'actions',
      render: (_, group) => (
        <span onClick={(event) => event.stopPropagation()}>
          <Popconfirm
            description="Its users are removed with it."
            okButtonProps={{ danger: true }}
            okText="Delete"
            onConfirm={() => remove(group)}
            title={`Delete group ${group.name}?`}
          >
            <Button aria-label={`Delete ${group.name}`} danger icon={<DeleteOutlined />} type="text" />
          </Popconfirm>
        </span>
      ),
      title: '',
      width: 64,
    },
  ];

  return (
    <>
      <PageHeader
        description={DESCRIPTION}
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={reload}>
              Refresh
            </Button>
            <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
              New group
            </Button>
          </Flex>
        }
        title="Basic auth"
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load groups'}
          type="error"
        />
      )}

      {refusal && (
        <Alert
          closable={{ onClose: () => setRefusal(undefined) }}
          description={
            refusal.domains.length > 0 && (
              <ul style={{ margin: 0, paddingInlineStart: 20 }}>
                {refusal.domains.map((domain) => (
                  <li key={domain.id}>{usedByLabel(domain)}</li>
                ))}
              </ul>
            )
          }
          showIcon
          style={{ marginBottom: 16 }}
          title={refusal.message}
          type="error"
        />
      )}

      <Card variant="outlined">
        <Table<AuthGroup>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading}
          locale={{
            emptyText:
              isLoading || error ? (
                ' '
              ) : (
                <EmptyState
                  action={
                    <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                      New group
                    </Button>
                  }
                  description="Make a group, add its users, then pick it on a domain."
                  title="No groups yet"
                />
              ),
          }}
          onRow={(group) => ({
            onClick: () => void navigate(paths.basicAuthGroup(group.id)),
            style: { cursor: 'pointer' },
          })}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>

      <Modal
        afterClose={() => form.resetFields()}
        cancelText="Cancel"
        confirmLoading={saving}
        destroyOnHidden
        okText="Create"
        onCancel={close}
        onOk={() => form.submit()}
        open={creating}
        title="New group"
      >
        {saveError && <Alert showIcon style={{ marginBottom: 16 }} title={saveError.message} type="error" />}
        <Form<{ name: string }> autoComplete="off" form={form} layout="vertical" onFinish={(values) => void create(values.name)} preserve={false}>
          <Form.Item
            label="Group name"
            name="name"
            rules={[
              { message: 'Enter a name', required: true, whitespace: true },
              { max: 64, message: 'At most 64 characters' },
            ]}
          >
            <Input placeholder="Staging" />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
