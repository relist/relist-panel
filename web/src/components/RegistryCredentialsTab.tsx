import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Form, Input, Modal, Popconfirm, Table, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { api } from '../data/api';
import type { PushCredential, RegistryState } from '../data/types';
import { loginActionSnippet, loginCommand } from '../pages/registry-view';
import { CODE_FONT_STACK } from '../app/theme';
import { EmptyState, RelativeTime } from './primitives';
import { REGISTRY_KEY, RegistryOffState } from './RegistryOffState';

const { Paragraph, Text } = Typography;

export const PUSH_CREDENTIALS_KEY = '/api/registry/credentials';

/**
 * Push credentials: named htpasswd logins CI pushes with. Only the bcrypt hash
 * is stored, so a token is shown once, on create.
 */
export function RegistryCredentialsTab() {
  const registry = useSWR(REGISTRY_KEY, () => api.getRegistry());
  const enabled = registry.data?.settings.enabled === true;
  const { data, error, isLoading, mutate } = useSWR(enabled ? PUSH_CREDENTIALS_KEY : null, () => api.listPushCredentials());
  const [creating, setCreating] = useState(false);
  const [failure, setFailure] = useState<string>();

  if (registry.data && !enabled) return <RegistryOffState />;

  const revoke = async (credential: PushCredential) => {
    setFailure(undefined);
    try {
      await api.deletePushCredential(credential.id);
      void mutate();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not revoke the Push credential');
    }
  };

  const columns: TableColumnsType<PushCredential> = [
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text strong>{name}</Text>, title: 'Name' },
    { dataIndex: 'createdBy', key: 'createdBy', title: 'Created by' },
    { dataIndex: 'createdAt', key: 'createdAt', render: (at: string) => <RelativeTime value={at} />, title: 'Created' },
    {
      align: 'right',
      key: 'actions',
      render: (_, credential) => (
        <Popconfirm
          description="Anything logging in with it can no longer push or pull."
          okButtonProps={{ danger: true }}
          okText="Revoke"
          onConfirm={() => revoke(credential)}
          title={`Revoke ${credential.name}?`}
        >
          <Button aria-label={`Revoke ${credential.name}`} danger icon={<DeleteOutlined />} type="text" />
        </Popconfirm>
      ),
      title: '',
    },
  ];

  const createButton = (
    <Button icon={<PlusOutlined />} onClick={() => setCreating(true)} type="primary">
      Create credential
    </Button>
  );
  const loadError = error ?? registry.error;

  return (
    <Flex gap={16} vertical>
      {(loadError || failure) && (
        <Alert
          showIcon
          title={failure ?? (loadError instanceof Error ? loadError.message : 'Could not load Push credentials')}
          type="error"
        />
      )}
      <Card extra={createButton} title="Push credentials" variant="outlined">
        <Text style={{ display: 'block', marginBottom: 16 }} type="secondary">
          Logins CI uses to push to this Registry. Each can push and pull every repository.
        </Text>
        <Table<PushCredential>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading || registry.isLoading}
          locale={{
            emptyText:
              isLoading || loadError ? (
                ' '
              ) : (
                <EmptyState action={createButton} description="Create one for each CI pipeline that pushes images." title="No Push credentials yet" />
              ),
          }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>
      <CreateCredentialModal
        hostname={registry.data?.settings.hostname ?? ''}
        onClose={() => {
          setCreating(false);
          void mutate();
        }}
        open={creating}
      />
    </Flex>
  );
}

function CreateCredentialModal({ hostname, onClose, open }: { hostname: string; onClose: () => void; open: boolean }) {
  const [form] = Form.useForm<{ name: string }>();
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ name: string; token: string }>();

  const submit = async ({ name }: { name: string }) => {
    setBusy(true);
    try {
      const result = await api.createPushCredential(name.trim());
      setCreated({ name: result.credential.name, token: result.token });
    } catch (reason: unknown) {
      form.setFields([{ errors: [reason instanceof Error ? reason.message : 'Could not create the Push credential'], name: 'name' }]);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      afterClose={() => setCreated(undefined)}
      cancelButtonProps={created ? { style: { display: 'none' } } : undefined}
      confirmLoading={busy}
      destroyOnHidden
      okText={created ? 'Done' : 'Create'}
      onCancel={onClose}
      onOk={() => (created ? onClose() : form.submit())}
      open={open}
      title={created ? `Push credential ${created.name}` : 'Create Push credential'}
      width={600}
    >
      {created ? (
        <Flex gap={12} vertical>
          <Alert showIcon title="This token is shown only once. Store it as a CI secret now." type="warning" />
          <div>
            <Text strong>Token</Text>
            <Paragraph copyable={{ text: created.token }} style={{ marginBottom: 0 }}>
              <Text code>{created.token}</Text>
            </Paragraph>
          </div>
          <div>
            <Text strong>Log in from a shell</Text>
            <Paragraph copyable={{ text: loginCommand(hostname, created.name) }} style={{ marginBottom: 0 }}>
              <Text code>{loginCommand(hostname, created.name)}</Text>
            </Paragraph>
          </div>
          <div>
            <Text strong>GitHub Actions</Text>
            <Text style={{ display: 'block' }} type="secondary">
              Save the token as the RELIST_REGISTRY_TOKEN secret.
            </Text>
            <Paragraph copyable={{ text: loginActionSnippet(hostname, created.name) }} style={{ marginBottom: 0 }}>
              <pre style={{ fontFamily: CODE_FONT_STACK, fontSize: 12, margin: 0 }}>{loginActionSnippet(hostname, created.name)}</pre>
            </Paragraph>
          </div>
        </Flex>
      ) : (
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} preserve={false}>
          <Form.Item
            extra="For example github-ci. Lowercase letters, digits, - and _."
            label="Name"
            name="name"
            rules={[{ message: 'Enter a name', required: true, whitespace: true }]}
          >
            <Input autoComplete="off" autoFocus />
          </Form.Item>
        </Form>
      )}
    </Modal>
  );
}
