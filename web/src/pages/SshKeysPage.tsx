import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { Alert, Button, Card, Flex, Form, Input, Modal, Table, Tooltip, Typography } from 'antd';
import type { TableColumnsType } from 'antd';
import { useState } from 'react';
import { Link } from 'react-router';
import useSWR from 'swr';

import { paths } from '../app/paths';
import { CODE_FONT_STACK } from '../app/theme';
import { DangerModal } from '../components/DangerModal';
import { EmptyState, PageHeader, RelativeTime } from '../components/primitives';
import { ApiError, api } from '../data/api';
import type { SshKey } from '../data/types';
import { deleteWarning, shortPublicKey, usedByLabel } from './ssh-keys-view';

const { Paragraph, Text } = Typography;

const SSH_KEYS_KEY = '/api/ssh-keys';
const SHOWN_USES = 2;
const GENERATE_HINT =
  'Repository → Settings → Deploy keys → Add deploy key, leave write access off. To reuse it, add the same key to each repository.';

/** A name error from the server (409 duplicate, 400 invalid) belongs under the field; anything else is a banner. */
function nameError(reason: unknown): { field: boolean; message: string } {
  const message = reason instanceof Error ? reason.message : 'Something went wrong';
  return { field: reason instanceof ApiError && (reason.status === 409 || reason.status === 400), message };
}

type Dialog =
  | { kind: 'generate' }
  | { kind: 'rename'; sshKey: SshKey }
  | { kind: 'delete'; sshKey: SshKey }
  | undefined;

/**
 * SSH keys: the host-wide library of deploy keys. A Git Source picks one by
 * name; generating a key is the only way to add one, and the private half
 * never leaves the host.
 */
export function SshKeysPage() {
  const { data, error, isLoading, mutate } = useSWR(SSH_KEYS_KEY, () => api.listSshKeys());
  const [dialog, setDialog] = useState<Dialog>();

  const reload = () => void mutate();
  const openGenerate = () => setDialog({ kind: 'generate' });

  const columns: TableColumnsType<SshKey> = [
    { dataIndex: 'name', key: 'name', render: (name: string) => <Text strong>{name}</Text>, title: 'Name' },
    {
      dataIndex: 'publicKey',
      key: 'publicKey',
      render: (publicKey: string) => (
        <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{shortPublicKey(publicKey)}</Text>
      ),
      title: 'Public key',
    },
    {
      key: 'usedBy',
      render: (_, sshKey) =>
        sshKey.usedBy.length === 0 ? (
          <Text type="secondary">{usedByLabel(sshKey)}</Text>
        ) : (
          <Tooltip title={usedByLabel(sshKey)}>
            <span>
              {sshKey.usedBy.slice(0, SHOWN_USES).map((use, index) => (
                <span key={use.composeId}>
                  {index > 0 && ', '}
                  <Link to={paths.compose(use.projectId, use.environmentId, use.composeId)}>{use.composeName}</Link>
                </span>
              ))}
              {sshKey.usedBy.length > SHOWN_USES && ` +${sshKey.usedBy.length - SHOWN_USES} more`}
            </span>
          </Tooltip>
        ),
      title: 'Used by',
    },
    {
      dataIndex: 'createdAt',
      key: 'createdAt',
      render: (createdAt: string) => <RelativeTime value={createdAt} />,
      title: 'Created',
    },
    {
      align: 'right',
      key: 'actions',
      render: (_, sshKey) => (
        <Flex gap={4} justify="flex-end">
          <Button
            aria-label={`Rename ${sshKey.name}`}
            icon={<EditOutlined />}
            onClick={() => setDialog({ kind: 'rename', sshKey })}
            type="text"
          />
          <Button
            aria-label={`Delete ${sshKey.name}`}
            danger
            icon={<DeleteOutlined />}
            onClick={() => setDialog({ kind: 'delete', sshKey })}
            type="text"
          />
        </Flex>
      ),
      title: '',
    },
  ];

  return (
    <>
      <PageHeader
        description="Deploy keys for Git Sources. Generate one here, add its public key to a repository, then choose it on each Source."
        extra={
          <Flex gap={8}>
            <Button icon={<ReloadOutlined />} loading={isLoading} onClick={reload}>
              Refresh
            </Button>
            <Button icon={<PlusOutlined />} onClick={openGenerate} type="primary">
              Generate key
            </Button>
          </Flex>
        }
        title="SSH keys"
      />

      {error && (
        <Alert
          action={<Button onClick={reload}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={error instanceof Error ? error.message : 'Could not load SSH keys'}
          type="error"
        />
      )}

      <Card variant="outlined">
        <Table<SshKey>
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
                    <Button icon={<PlusOutlined />} onClick={openGenerate} type="primary">
                      Generate key
                    </Button>
                  }
                  description="Generate a key, then add its public half to your repository as a deploy key."
                  title="No SSH keys yet"
                />
              ),
          }}
          pagination={false}
          rowKey="id"
        />
      </Card>

      <GenerateModal
        onClose={() => {
          setDialog(undefined);
          reload();
        }}
        open={dialog?.kind === 'generate'}
      />
      <RenameModal
        onClose={() => setDialog(undefined)}
        onRenamed={() => {
          setDialog(undefined);
          reload();
        }}
        sshKey={dialog?.kind === 'rename' ? dialog.sshKey : undefined}
      />
      <DeleteModal
        onClose={() => setDialog(undefined)}
        onDeleted={() => {
          setDialog(undefined);
          reload();
        }}
        sshKey={dialog?.kind === 'delete' ? dialog.sshKey : undefined}
      />
    </>
  );
}

function GenerateModal({ onClose, open }: { onClose: () => void; open: boolean }) {
  const [form] = Form.useForm<{ name: string }>();
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<SshKey>();
  const [failure, setFailure] = useState<string>();

  const submit = async ({ name }: { name: string }) => {
    setBusy(true);
    setFailure(undefined);
    try {
      setCreated(await api.createSshKey(name.trim()));
    } catch (reason: unknown) {
      const { field, message } = nameError(reason);
      if (field) form.setFields([{ errors: [message], name: 'name' }]);
      else setFailure(message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      afterClose={() => {
        setCreated(undefined);
        setFailure(undefined);
        form.resetFields();
      }}
      cancelButtonProps={{ style: created ? { display: 'none' } : undefined }}
      confirmLoading={busy}
      destroyOnHidden
      okText={created ? 'Done' : 'Generate'}
      onCancel={onClose}
      onOk={created ? onClose : () => form.submit()}
      open={open}
      title={created ? `SSH key ${created.name}` : 'Generate SSH key'}
      width={560}
    >
      {created ? (
        <Flex gap={12} vertical>
          <Paragraph
            copyable
            style={{ fontFamily: CODE_FONT_STACK, fontSize: 12, margin: 0, wordBreak: 'break-all' }}
          >
            {created.publicKey}
          </Paragraph>
          <Text type="secondary">{GENERATE_HINT}</Text>
        </Flex>
      ) : (
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} preserve={false}>
          {failure && <Alert showIcon style={{ marginBottom: 16 }} title={failure} type="error" />}
          <Form.Item label="Name" name="name" rules={[{ message: 'Enter a name', required: true, whitespace: true }]}>
            <Input autoComplete="off" autoFocus />
          </Form.Item>
        </Form>
      )}
    </Modal>
  );
}

function RenameModal({
  onClose,
  onRenamed,
  sshKey,
}: {
  onClose: () => void;
  onRenamed: () => void;
  sshKey: SshKey | undefined;
}) {
  const [form] = Form.useForm<{ name: string }>();
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();

  const submit = async ({ name }: { name: string }) => {
    if (!sshKey) return;
    setBusy(true);
    setFailure(undefined);
    try {
      await api.renameSshKey(sshKey.id, name.trim());
      onRenamed();
    } catch (reason: unknown) {
      const { field, message } = nameError(reason);
      if (field) form.setFields([{ errors: [message], name: 'name' }]);
      else setFailure(message);
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
      open={sshKey !== undefined}
      title="Rename SSH key"
    >
      <Form
        form={form}
        initialValues={{ name: sshKey?.name }}
        layout="vertical"
        onFinish={(values) => void submit(values)}
        preserve={false}
      >
        {failure && <Alert showIcon style={{ marginBottom: 16 }} title={failure} type="error" />}
        <Form.Item label="Name" name="name" rules={[{ message: 'Enter a name', required: true, whitespace: true }]}>
          <Input autoComplete="off" autoFocus />
        </Form.Item>
      </Form>
    </Modal>
  );
}

function DeleteModal({
  onClose,
  onDeleted,
  sshKey,
}: {
  onClose: () => void;
  onDeleted: () => void;
  sshKey: SshKey | undefined;
}) {
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<string>();
  const warning = sshKey ? deleteWarning(sshKey) : null;

  const confirm = async () => {
    if (!sshKey) return;
    setBusy(true);
    setFailure(undefined);
    try {
      await api.deleteSshKey(sshKey.id);
      onDeleted();
    } catch (reason: unknown) {
      setFailure(reason instanceof Error ? reason.message : 'Could not delete the SSH key');
    } finally {
      setBusy(false);
    }
  };

  return (
    <DangerModal
      confirmWord={sshKey?.name ?? ''}
      consequences={
        <Flex gap={12} vertical>
          {warning && sshKey && (
            <Alert
              description={
                <ul style={{ margin: 0, paddingInlineStart: 20 }}>
                  {sshKey.usedBy.map((use) => (
                    <li key={use.composeId}>
                      <Text strong>{use.composeName}</Text>{' '}
                      <Text type="secondary">
                        {use.projectName}
                      </Text>
                    </li>
                  ))}
                </ul>
              }
              showIcon
              title={warning}
              type="warning"
            />
          )}
          {failure && <Alert showIcon title={failure} type="error" />}
        </Flex>
      }
      description={sshKey ? `Delete the SSH key ${sshKey.name}? Its private half is removed from this host.` : undefined}
      loading={busy}
      okText="Delete key"
      onCancel={onClose}
      onConfirm={() => void confirm()}
      open={sshKey !== undefined}
      title="Delete SSH key"
    />
  );
}
