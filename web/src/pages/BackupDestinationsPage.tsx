import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Checkbox, Flex, Form, Input, InputNumber, Modal, Popconfirm, Radio, Segmented, Select, Switch, Table, Tag, Tooltip, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { BackupDestination, BackupDestinationInput } from '../data/types';
import { bucketLabel, copiesWarning, deleteConflict, destinationTestState, isInsecureEndpoint, sftpLocation } from './backup-destinations-view';

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
  type: 's3' | 'sftp';
  name: string;
  host: string;
  port: number | null;
  user: string;
  path: string;
  auth: 'key' | 'password';
  sshKeyId: number | undefined;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKeyId: string;
  secret: string;
  pathStyle: boolean;
  encrypt: boolean;
  passphrase: string;
}

const BASE58 = '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';

/** 24 base58 characters from crypto randomness, about 140 bits. */
function generatePassphrase(): string {
  const out: string[] = [];
  while (out.length < 24) {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    for (const byte of bytes) {
      // Rejection sampling: 232 = 4 × 58 keeps the draw uniform.
      if (byte < 232 && out.length < 24) out.push(BASE58[byte % 58]);
    }
  }
  return out.join('');
}

/**
 * Backup destinations: the host-wide library of S3-compatible buckets and SFTP
 * servers that a Database's schedule copies its scheduled Backups to
 * (ADR-0040, ADR-0042). The secret (access key or password) is never shown again.
 */
export function BackupDestinationsPage() {
  const { data, error, isLoading, mutate } = useSWR(BACKUP_DESTINATIONS_KEY, () => api.listBackupDestinations());
  const [dialog, setDialog] = useState<Dialog>();
  // Each open gets a fresh modal and form: one opened while the last is still
  // closing would otherwise keep that form's values. Closing keeps the key, so
  // the close animation still plays.
  const [dialogKey, setDialogKey] = useState(0);
  const openDialog = (next: Dialog) => {
    setDialogKey((key) => key + 1);
    setDialog(next);
  };
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
    {
      key: 'type',
      render: (_, destination) => (
        <Flex align="center" gap={6}>
          {destination.type === 'sftp' ? 'SFTP' : 'S3'}
          {destination.encrypt && <Tag>Encrypted</Tag>}
        </Flex>
      ),
      title: 'Type',
    },
    {
      key: 'location',
      render: (_, destination) =>
        destination.type === 'sftp' ? (
          <Text>{sftpLocation(destination.sftp)}</Text>
        ) : (
          <Flex gap={2} vertical>
            <Text>{destination.s3.endpoint}</Text>
            <Text type="secondary">{bucketLabel(destination)}</Text>
          </Flex>
        ),
      title: 'Location',
    },
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
            onClick={() => openDialog({ destination, kind: 'edit' })}
            type="text"
          />
          <Popconfirm
            description="Backups already copied stay on the bucket or server."
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
    <Button icon={<PlusOutlined />} onClick={() => openDialog({ kind: 'add' })} type="primary">
      Add destination
    </Button>
  );

  return (
    <Flex gap={16} vertical>
      <PageHeader
        description="S3-compatible buckets (AWS S3, Cloudflare R2, Backblaze B2, MinIO) and SFTP servers that a Database's schedule copies its scheduled backups to."
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
                  description="Add a bucket or an SFTP server so a Database's scheduled backups are also kept off this server."
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
        key={dialogKey}
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

const FIELDS = ['name', 'endpoint', 'bucket', 'accessKeyId', 'secret', 'region', 'prefix', 'host', 'port', 'user', 'path', 'auth', 'sshKeyId', 'passphrase'] as const;

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
  const type = Form.useWatch('type', form) ?? destination?.type ?? 's3';
  const endpoint = Form.useWatch('endpoint', form);
  const auth = Form.useWatch('auth', form);
  const sshKeyId = Form.useWatch('sshKeyId', form);
  const host = Form.useWatch('host', form);
  const port = Form.useWatch('port', form);
  const keys = useSWR(open && type === 'sftp' ? '/api/ssh-keys' : null, () => api.listSshKeys());
  const [busy, setBusy] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [failure, setFailure] = useState<string>();
  const [revealed, setRevealed] = useState<string>();
  const [revealing, setRevealing] = useState(false);
  const editing = destination !== undefined;
  const encrypt = Form.useWatch('encrypt', form) ?? destination?.encrypt ?? false;
  const wasEncrypting = destination?.encrypt ?? false;
  const old = destination?.type === 'sftp' ? destination.sftp : undefined;
  // A kept password stays with its host: the server asks for it again when
  // the host or port moves, or when the login switches from a key.
  const askPassword = !editing || old === undefined || old.auth !== 'password' || old.host !== (host ?? '').trim() || old.port !== (port ?? 22);
  const chosenKey = (keys.data ?? []).find((key) => key.id === sshKeyId);

  const submit = async (values: DestinationForm) => {
    setBusy(true);
    setFailure(undefined);
    const base = {
      encrypt: values.encrypt,
      name: values.name.trim(),
      passphrase: values.encrypt ? (values.passphrase ?? '').trim() : '',
      secret: values.secret ?? '',
    };
    const input: BackupDestinationInput =
      values.type === 'sftp'
        ? {
            ...base,
            secret: values.auth === 'password' ? base.secret : '',
            sftp: {
              auth: values.auth,
              host: values.host.trim(),
              path: (values.path ?? '').trim(),
              port: values.port ?? 22,
              sshKeyId: values.auth === 'key' ? (values.sshKeyId ?? null) : null,
              user: values.user.trim(),
            },
            type: 'sftp',
          }
        : {
            ...base,
            s3: {
              accessKeyId: values.accessKeyId.trim(),
              bucket: values.bucket.trim(),
              endpoint: values.endpoint.trim(),
              pathStyle: values.pathStyle,
              prefix: (values.prefix ?? '').trim(),
              region: (values.region ?? '').trim(),
            },
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

  // Turning encryption off, or entering a new passphrase, applies to new
  // copies only; the stored passphrase is forgotten, so ask first.
  const confirmThenSubmit = (values: DestinationForm) => {
    const changes = wasEncrypting && (!values.encrypt || (values.passphrase ?? '').trim() !== '');
    if (!changes) {
      void submit(values);
      return;
    }
    Modal.confirm({
      content: 'Existing copies keep the old passphrase and the panel forgets it. Make sure you have it somewhere else.',
      okButtonProps: { danger: true },
      okText: values.encrypt ? 'Change the passphrase' : 'Forget the passphrase',
      onOk: () => submit(values),
      title: values.encrypt ? 'Change the passphrase?' : 'Turn encryption off?',
    });
  };

  const reveal = async () => {
    if (!destination) return;
    setRevealing(true);
    setFailure(undefined);
    try {
      const { passphrase } = await api.revealDestinationPassphrase(destination.id);
      setRevealed(passphrase);
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not reveal the passphrase');
    } finally {
      setRevealing(false);
    }
  };

  const resetHostKey = async () => {
    if (!destination) return;
    setResetting(true);
    setFailure(undefined);
    try {
      await api.resetBackupDestinationHostKey(destination.id);
      onSaved();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not reset the host key');
    } finally {
      setResetting(false);
    }
  };

  const required = (message: string) => [{ message, required: true, whitespace: true }];
  const s3 = destination?.type === 's3' ? destination.s3 : undefined;

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
          accessKeyId: s3?.accessKeyId ?? '',
          auth: old?.auth ?? 'key',
          bucket: s3?.bucket ?? '',
          endpoint: s3?.endpoint ?? '',
          host: old?.host ?? '',
          name: destination?.name ?? '',
          path: old?.path ?? '',
          pathStyle: s3?.pathStyle ?? false,
          port: old?.port ?? 22,
          prefix: s3?.prefix ?? '',
          region: s3?.region ?? '',
          encrypt: destination?.encrypt ?? false,
          passphrase: '',
          secret: '',
          sshKeyId: old?.sshKeyId ?? undefined,
          type: destination?.type ?? 's3',
          user: old?.user ?? '',
        }}
        layout="vertical"
        onFinish={confirmThenSubmit}
        preserve={false}
      >
        {failure && <Alert showIcon style={{ marginBottom: 16 }} title={failure} type="error" />}
        <Form.Item label="Type" name="type">
          <Segmented
            disabled={editing}
            options={[
              { label: 'S3-compatible', value: 's3' },
              { label: 'SFTP', value: 'sftp' },
            ]}
          />
        </Form.Item>
        <Form.Item label="Name" name="name" rules={required('Enter a name')}>
          <Input autoComplete="off" autoFocus />
        </Form.Item>
        {type === 's3' ? (
          <>
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
          </>
        ) : (
          <>
            <Form.Item label="Host" name="host" rules={required('Enter the host')}>
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item label="Port" name="port">
              <InputNumber max={65535} min={1} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item label="User" name="user" rules={required('Enter the user')}>
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item
              extra="Optional. Starts with / for an absolute path; otherwise relative to the login directory."
              label="Base path"
              name="path"
            >
              <Input autoComplete="off" />
            </Form.Item>
            <Form.Item label="Authentication" name="auth">
              <Radio.Group
                options={[
                  { label: 'SSH key (recommended)', value: 'key' },
                  { label: 'Password', value: 'password' },
                ]}
              />
            </Form.Item>
            {auth === 'password' ? (
              <Form.Item
                label="Password"
                name="secret"
                rules={askPassword ? [{ message: 'Enter the password', required: true }] : []}
              >
                <Input.Password autoComplete="new-password" placeholder={askPassword ? undefined : 'Leave blank to keep'} />
              </Form.Item>
            ) : (
              <>
                <Form.Item label="SSH key" name="sshKeyId" rules={[{ message: 'Choose an SSH key', required: true }]}>
                  <Select
                    aria-label="SSH key"
                    loading={keys.isLoading}
                    notFoundContent="No SSH keys yet"
                    options={(keys.data ?? []).map((key) => ({ label: key.name, value: key.id }))}
                    placeholder="Choose an SSH key"
                  />
                </Form.Item>
                {keys.data && keys.data.length === 0 && (
                  <Text type="secondary">
                    <a href={paths.sshKeys} rel="noreferrer" target="_blank">
                      Create a key in SSH keys
                    </a>
                  </Text>
                )}
                {(chosenKey ?? (old && sshKeyId === old.sshKeyId ? old : undefined)) && (
                  <Flex gap={4} style={{ marginBottom: 16 }} vertical>
                    <Text code copyable>
                      {chosenKey?.publicKey ?? old?.publicKey}
                    </Text>
                    <Text type="secondary">Add this line to ~/.ssh/authorized_keys on the server.</Text>
                  </Flex>
                )}
              </>
            )}
            {old && (
              <Form.Item label="Host key">
                <Flex align="center" gap={8} wrap>
                  {old.hostKey ? (
                    <Text code>{old.hostKey}</Text>
                  ) : (
                    <Text type="secondary">Not trusted yet. The next Test trusts the key the server presents.</Text>
                  )}
                  <Popconfirm
                    onConfirm={() => resetHostKey()}
                    title="Forget this host key? The next Test trusts whatever key the server presents."
                  >
                    <Button loading={resetting}>Reset host key</Button>
                  </Popconfirm>
                </Flex>
              </Form.Item>
            )}
          </>
        )}
        <Form.Item name="encrypt" style={{ marginBottom: 8 }} valuePropName="checked">
          <Checkbox>Encrypt copies with a passphrase</Checkbox>
        </Form.Item>
        {encrypt && (
          <>
            <Form.Item
              label="Passphrase"
              name="passphrase"
              rules={wasEncrypting ? [] : [{ message: 'Enter or generate a passphrase', required: true }]}
            >
              <Input.Password autoComplete="new-password" placeholder={wasEncrypting ? 'Leave blank to keep' : undefined} />
            </Form.Item>
            <Flex align="center" gap={8} style={{ marginBottom: 12 }} wrap>
              <Button onClick={() => form.setFieldValue('passphrase', generatePassphrase())}>Generate</Button>
              {wasEncrypting &&
                (revealed === undefined ? (
                  <Button loading={revealing} onClick={() => void reveal()}>
                    Reveal passphrase
                  </Button>
                ) : (
                  <Text code copyable>
                    {revealed}
                  </Text>
                ))}
            </Flex>
            <Text strong style={{ display: 'block', marginBottom: 16 }}>
              The panel cannot help if this server is lost: keep a copy of the passphrase somewhere else, or every
              encrypted copy is unreadable.
            </Text>
          </>
        )}
      </Form>
    </Modal>
  );
}
