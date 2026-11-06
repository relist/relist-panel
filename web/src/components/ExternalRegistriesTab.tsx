import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Form, Input, Modal, Popconfirm, Table, Tag, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { ApiError, api } from '../data/api';
import type { ExternalRegistry, ExternalRegistryInput } from '../data/types';
import { TOKEN_HINT, testState } from '../pages/registry-view';
import { EmptyState, RelativeTime } from './primitives';

const { Text } = Typography;

export const EXTERNAL_REGISTRIES_KEY = '/api/registry/external';

const STATE_TAG = {
  failed: { color: 'error', label: 'Failed' },
  passed: { color: 'success', label: 'Passed' },
  untested: { color: 'default', label: 'Not tested' },
} as const;

type Dialog = { kind: 'add' } | { kind: 'edit'; registry: ExternalRegistry } | undefined;

/**
 * External registries: logins for registries the panel does not run. Every
 * pull and build gets them (ADR-0034); the secret is never shown again.
 */
export function ExternalRegistriesTab() {
  const { data, error, isLoading, mutate } = useSWR(EXTERNAL_REGISTRIES_KEY, () => api.listExternalRegistries());
  const [dialog, setDialog] = useState<Dialog>();
  const [testing, setTesting] = useState<number>();
  const [failure, setFailure] = useState<string>();

  const reload = () => void mutate();

  const test = async (registry: ExternalRegistry) => {
    setTesting(registry.id);
    setFailure(undefined);
    try {
      const result = await api.testExternalRegistry(registry.id);
      await mutate(
        (list) =>
          list?.map((item) =>
            item.id === registry.id
              ? { ...item, lastTestAt: new Date().toISOString(), lastTestError: result.error, lastTestOk: result.ok }
              : item,
          ),
        { revalidate: true },
      );
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not test the login');
    } finally {
      setTesting(undefined);
    }
  };

  const remove = async (registry: ExternalRegistry) => {
    setFailure(undefined);
    try {
      await api.deleteExternalRegistry(registry.id);
      reload();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not delete the External registry');
    }
  };

  const columns: TableColumnsType<ExternalRegistry> = [
    { dataIndex: 'host', key: 'host', render: (host: string) => <Text strong>{host}</Text>, title: 'Host' },
    { dataIndex: 'username', key: 'username', title: 'Username' },
    {
      key: 'test',
      render: (_, registry) => {
        const state = STATE_TAG[testState(registry)];
        return (
          <Flex gap={2} vertical>
            <Flex align="center" gap={6}>
              <Tag color={state.color}>{state.label}</Tag>
              {registry.lastTestAt && (
                <Text type="secondary">
                  <RelativeTime value={registry.lastTestAt} />
                </Text>
              )}
            </Flex>
            {registry.lastTestError && (
              <Text style={{ fontSize: 12 }} type="danger">
                {registry.lastTestError}
              </Text>
            )}
          </Flex>
        );
      },
      title: 'Last test',
    },
    {
      align: 'right',
      key: 'actions',
      render: (_, registry) => (
        <Flex gap={4} justify="flex-end">
          <Button aria-label={`Test ${registry.host}`} loading={testing === registry.id} onClick={() => void test(registry)}>
            Test
          </Button>
          <Button
            aria-label={`Edit ${registry.host}`}
            icon={<EditOutlined />}
            onClick={() => setDialog({ kind: 'edit', registry })}
            type="text"
          />
          <Popconfirm
            description="Pulls of private images from this host will fail until it is added again."
            okButtonProps={{ danger: true }}
            okText="Delete"
            onConfirm={() => remove(registry)}
            title={`Delete ${registry.host}?`}
          >
            <Button aria-label={`Delete ${registry.host}`} danger icon={<DeleteOutlined />} type="text" />
          </Popconfirm>
        </Flex>
      ),
      title: '',
    },
  ];

  const addButton = (
    <Button icon={<PlusOutlined />} onClick={() => setDialog({ kind: 'add' })} type="primary">
      Add registry
    </Button>
  );

  return (
    <Flex gap={16} vertical>
      {(error || failure) && (
        <Alert
          action={error ? <Button onClick={reload}>Retry</Button> : undefined}
          showIcon
          title={failure ?? (error instanceof Error ? error.message : 'Could not load External registries')}
          type="error"
        />
      )}
      <Card
        extra={addButton}
        title="External registries"
        variant="outlined"
      >
        <Text style={{ display: 'block', marginBottom: 16 }} type="secondary">
          Logins for registries this panel does not run, such as GHCR or Docker Hub. Every pull and build on this host uses them.
        </Text>
        <Table<ExternalRegistry>
          columns={columns}
          dataSource={data ?? []}
          loading={isLoading}
          locale={{
            emptyText:
              isLoading || error ? (
                ' '
              ) : (
                <EmptyState
                  action={addButton}
                  description="Add a registry's login so Composes can pull its private images."
                  title="No External registries yet"
                />
              ),
          }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>
      <RegistryModal
        onClose={() => setDialog(undefined)}
        onSaved={() => {
          setDialog(undefined);
          reload();
        }}
        registry={dialog?.kind === 'edit' ? dialog.registry : undefined}
        open={dialog !== undefined}
      />
    </Flex>
  );
}

function RegistryModal({
  onClose,
  onSaved,
  open,
  registry,
}: {
  onClose: () => void;
  onSaved: () => void;
  open: boolean;
  registry: ExternalRegistry | undefined;
}) {
  const [form] = Form.useForm<ExternalRegistryInput>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();
  const editing = registry !== undefined;

  const submit = async (values: ExternalRegistryInput) => {
    setBusy(true);
    setFailure(undefined);
    const input = { host: values.host.trim(), secret: values.secret ?? '', username: values.username.trim() };
    try {
      if (registry) await api.updateExternalRegistry(registry.id, input);
      else await api.createExternalRegistry(input);
      onSaved();
    } catch (reason: unknown) {
      const message = reason instanceof Error ? reason.message : 'Could not save the External registry';
      if (reason instanceof ApiError && (reason.status === 409 || reason.status === 422) && !/username|token/.test(message)) {
        form.setFields([{ errors: [message], name: 'host' }]);
      } else {
        setFailure(message);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      afterClose={() => setFailure(undefined)}
      confirmLoading={busy}
      destroyOnHidden
      okText="Save"
      onCancel={onClose}
      onOk={() => form.submit()}
      open={open}
      title={editing ? `Edit ${registry.host}` : 'Add External registry'}
    >
      <Form
        form={form}
        initialValues={{ host: registry?.host ?? '', secret: '', username: registry?.username ?? '' }}
        layout="vertical"
        onFinish={(values) => void submit(values)}
        preserve={false}
      >
        {failure && <Alert showIcon style={{ marginBottom: 16 }} title={failure} type="error" />}
        <Form.Item
          extra="For example ghcr.io, docker.io or registry.example.com:5000."
          label="Host"
          name="host"
          rules={[{ message: 'Enter the registry host', required: true, whitespace: true }]}
        >
          <Input autoComplete="off" autoFocus />
        </Form.Item>
        <Form.Item label="Username" name="username" rules={[{ message: 'Enter the username', required: true, whitespace: true }]}>
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item
          extra={TOKEN_HINT}
          label="Token or password"
          name="secret"
          rules={editing ? [] : [{ message: 'Enter a token or password', required: true }]}
        >
          <Input.Password autoComplete="new-password" placeholder={editing ? 'Unchanged' : undefined} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
