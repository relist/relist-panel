import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Form, Input, Modal, Popconfirm, Switch, Table, Tag, Tooltip, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { BackupDestination, BackupDestinationInput } from '../data/types';
import { bucketLabel, copiesWarning, deleteConflict, destinationTestState, isInsecureEndpoint } from './backup-destinations-view';

const { Text } = Typography;

export const BACKUP_DESTINATIONS_KEY = '/api/backup-destinations';

const STATE_TAG = {
  failed: { color: 'error', label: 'Failed' },
  passed: { color: 'success', label: 'OK' },
  untested: { color: 'default', label: 'Not tested' },
} as const;

const ENDPOINT_HINT = 'https://s3.amazonaws.com, https://<account>.r2.cloudflarestorage.com or http://minio:9000';

type Dialog = { kind: 'add' } | { kind: 'edit'; destination: BackupDestination } | undefined;

interface DestinationForm {
  name: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKeyId: string;
  secret: string;
  pathStyle: boolean;
}

/**
 * Backup destinations: the host-wide library of S3-compatible buckets that a
 * Database's schedule copies its scheduled Backups to (ADR-0040). The secret
 * access key is never shown again.
 */
export function BackupDestinationsPage() {
  const { data, error, isLoading, mutate } = useSWR(BACKUP_DESTINATIONS_KEY, () => api.listBackupDestinations());
  const [dialog, setDialog] = useState<Dialog>();
  const [testing, setTesting] = useState<ReadonlySet<number>>(new Set());
  const [failure, setFailure] = useState<string>();
  // A delete that needs the copies confirmed.
  const [confirmDelete, setConfirmDelete] = useState<{ copies: number; destination: BackupDestination }>();
  const [deleting, setDeleting] = useState(false);

  const reload = () => void mutate();

  const test = async (destination: BackupDestination) => {
    setTesting((ids) => new Set(ids).add(destination.id));
    setFailure(undefined);
    try {
      const result = await api.testBackupDestination(destination.id);
      await mutate((list) => list?.map((item) => (item.id === destination.id ? result : item)), { revalidate: true });
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not test the destination');
    } finally {
      setTesting((ids) => {
        const next = new Set(ids);
        next.delete(destination.id);
        return next;
      });
    }
  };

  const remove = async (destination: BackupDestination, copies?: number) => {
    setFailure(undefined);
    setDeleting(true);
    try {
      await api.deleteBackupDestination(destination.id, copies);
      setConfirmDelete(undefined);
      reload();
    } catch (reason: unknown) {
      const conflict = reason instanceof ApiError && reason.status === 409 ? deleteConflict(reason.body) : {};
      if (conflict.copies !== undefined) {
        setConfirmDelete({ copies: conflict.copies, destination });
      } else {
        setConfirmDelete(undefined);
        setFailure(reason instanceof Error ? reason.message : 'Could not delete the destination');
      }
    } finally {
      setDeleting(false);
    }
  };

  const columns: TableColumnsType<BackupDestination> = [
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text strong>{name}</Text>, title: 'Name' },
    { key: 'endpoint', render: (_, destination) => destination.s3.endpoint, title: 'Endpoint' },
    { key: 'bucket', render: (_, destination) => bucketLabel(destination), title: 'Bucket/Prefix' },
    {
      key: 'test',
      render: (_, destination) => {
        const state = STATE_TAG[destinationTestState(destination)];
        return (
          <Flex align="center" gap={6}>
            {destination.lastTestError ? (
              <Tooltip title={destination.lastTestError}>
                <Tag color={state.color}>{state.label}</Tag>
              </Tooltip>
            ) : (
              <Tag color={state.color}>{state.label}</Tag>
            )}
            {destination.lastTestAt && (
              <Text type="secondary">
                <RelativeTime value={destination.lastTestAt} />
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
      render: (_, destination) => (
        <Flex gap={4} justify="flex-end">
          <Button
            aria-label={`Test ${destination.name}`}
            loading={testing.has(destination.id)}
            onClick={() => void test(destination)}
          >
            Test
          </Button>
          <Button
            aria-label={`Edit ${destination.name}`}
            icon={<EditOutlined />}
            onClick={() => setDialog({ destination, kind: 'edit' })}
            type="text"
          />
          <Popconfirm
            description="Backups already copied stay in the bucket."
            okButtonProps={{ danger: true }}
            okText="Delete"
            onConfirm={() => remove(destination)}
            title={`Delete ${destination.name}?`}
          >
            <Button aria-label={`Delete ${destination.name}`} danger icon={<DeleteOutlined />} type="text" />
          </Popconfirm>
        </Flex>
      ),
      title: '',
    },
  ];

  const addButton = (
    <Button icon={<PlusOutlined />} onClick={() => setDialog({ kind: 'add' })} type="primary">
      Add destination
    </Button>
  );

  return (
    <Flex gap={16} vertical>
      <PageHeader
        description="S3-compatible buckets (AWS S3, Cloudflare R2, Backblaze B2, MinIO) that a Database's schedule copies its scheduled backups to."
        title="Backup destinations"
      />
      {(error || failure) && (
        <Alert
          action={error ? <Button onClick={reload}>Retry</Button> : undefined}
          closable={failure ? { onClose: () => setFailure(undefined) } : undefined}
          showIcon
          title={failure ?? (error instanceof Error ? error.message : 'Could not load Backup destinations')}
          type="error"
        />
      )}
      <Card extra={addButton} title="Destinations" variant="outlined">
        <Table<BackupDestination>
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
                  description="Add a bucket so a Database's scheduled backups are also kept off this server."
                  title="No Backup destinations yet"
                />
              ),
          }}
          pagination={false}
          rowKey="id"
          scroll={{ x: 'max-content' }}
        />
      </Card>
      <DestinationModal
        destination={dialog?.kind === 'edit' ? dialog.destination : undefined}
        onClose={() => setDialog(undefined)}
        onSaved={() => {
          setDialog(undefined);
          reload();
        }}
        open={dialog !== undefined}
      />
      <Modal
        confirmLoading={deleting}
        okButtonProps={{ danger: true }}
        okText="Delete destination"
        onCancel={() => setConfirmDelete(undefined)}
        onOk={() => confirmDelete && void remove(confirmDelete.destination, confirmDelete.copies)}
        open={confirmDelete !== undefined}
        title={`Delete ${confirmDelete?.destination.name ?? ''}?`}
      >
        {confirmDelete && <Text>{copiesWarning(confirmDelete.copies)}</Text>}
      </Modal>
    </Flex>
  );
}

const FIELDS = ['name', 'endpoint', 'bucket', 'accessKeyId', 'secret', 'region', 'prefix'] as const;

function DestinationModal({
  destination,
  onClose,
  onSaved,
  open,
}: {
  destination: BackupDestination | undefined;
  onClose: () => void;
  onSaved: () => void;
  open: boolean;
}) {
  const [form] = Form.useForm<DestinationForm>();
  const endpoint = Form.useWatch('endpoint', form);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();
  const editing = destination !== undefined;

  const submit = async (values: DestinationForm) => {
    setBusy(true);
    setFailure(undefined);
    const input: BackupDestinationInput = {
      name: values.name.trim(),
      s3: {
        accessKeyId: values.accessKeyId.trim(),
        bucket: values.bucket.trim(),
        endpoint: values.endpoint.trim(),
        pathStyle: values.pathStyle,
        prefix: (values.prefix ?? '').trim(),
        region: (values.region ?? '').trim(),
      },
      secret: values.secret ?? '',
      type: 's3',
    };
    try {
      if (destination) await api.updateBackupDestination(destination.id, input);
      else await api.createBackupDestination(input);
      onSaved();
    } catch (reason: unknown) {
      const message = reason instanceof Error ? reason.message : 'Could not save the Backup destination';
      const field = reason instanceof ApiError ? reason.field : undefined;
      if (field && (FIELDS as readonly string[]).includes(field)) {
        form.setFields([{ errors: [message], name: field as (typeof FIELDS)[number] }]);
      } else {
        setFailure(message);
      }
    } finally {
      setBusy(false);
    }
  };

  const required = (message: string) => [{ message, required: true, whitespace: true }];

  return (
    <Modal
      afterClose={() => setFailure(undefined)}
      confirmLoading={busy}
      destroyOnHidden
      okText="Save"
      onCancel={onClose}
      onOk={() => form.submit()}
      open={open}
      title={editing ? `Edit ${destination.name}` : 'Add Backup destination'}
    >
      <Form
        form={form}
        initialValues={{
          accessKeyId: destination?.s3.accessKeyId ?? '',
          bucket: destination?.s3.bucket ?? '',
          endpoint: destination?.s3.endpoint ?? '',
          name: destination?.name ?? '',
          pathStyle: destination?.s3.pathStyle ?? false,
          prefix: destination?.s3.prefix ?? '',
          region: destination?.s3.region ?? '',
          secret: '',
        }}
        layout="vertical"
        onFinish={(values) => void submit(values)}
        preserve={false}
      >
        {failure && <Alert showIcon style={{ marginBottom: 16 }} title={failure} type="error" />}
        <Form.Item label="Name" name="name" rules={required('Enter a name')}>
          <Input autoComplete="off" autoFocus />
        </Form.Item>
        <Form.Item extra={ENDPOINT_HINT} label="Endpoint" name="endpoint" rules={required('Enter the endpoint')}>
          <Input autoComplete="off" />
        </Form.Item>
        {isInsecureEndpoint(endpoint) && (
          <Alert showIcon style={{ marginBottom: 16 }} title="The secret and backups travel unencrypted." type="warning" />
        )}
        <Form.Item extra="Optional. Leave blank for most providers." label="Region" name="region">
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item label="Bucket" name="bucket" rules={required('Enter the bucket')}>
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item extra="Optional folder inside the bucket." label="Prefix" name="prefix">
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item label="Access key ID" name="accessKeyId" rules={required('Enter the access key ID')}>
          <Input autoComplete="off" />
        </Form.Item>
        <Form.Item
          label="Secret access key"
          name="secret"
          rules={editing ? [] : [{ message: 'Enter the secret access key', required: true }]}
        >
          <Input.Password autoComplete="new-password" placeholder={editing ? 'Leave blank to keep' : undefined} />
        </Form.Item>
        <Form.Item
          extra="MinIO needs it. AWS S3, R2 and B2 do not."
          label="Path-style addressing"
          name="pathStyle"
          valuePropName="checked"
        >
          <Switch aria-label="Path-style addressing" />
        </Form.Item>
      </Form>
    </Modal>
  );
}
