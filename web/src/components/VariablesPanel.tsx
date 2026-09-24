import { DeleteOutlined, EditOutlined, EyeInvisibleOutlined, EyeOutlined, PlusOutlined, SearchOutlined, SnippetsOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, Popconfirm, Space, Spin, Table, Tag, Tooltip, Typography, theme } from 'antd';
import { useMemo, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel } from '../data/store';
import type { Environment, EnvironmentVariable } from '../data/types';
import { EmptyState, RelativeTime } from './primitives';
import { duplicateKeys, invalidKeys, isSecret, maskedValue, parseDotEnv, replacePlan, VARIABLE_KEY_PATTERN, visibleVariables } from './variables-view';

const { Text, Paragraph } = Typography;

/** One value cell: masked until revealed, copy always at hand (GUIDELINES §4.5). */
function ValueCell({ variable }: { variable: EnvironmentVariable }) {
  const [revealed, setRevealed] = useState(false);
  const secret = isSecret(variable);

  return (
    <Flex align="center" gap={6}>
      <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{secret && !revealed ? maskedValue(variable.value) : variable.value}</Text>
      {secret && (
        <Tooltip title={revealed ? 'Hide value' : 'Reveal value'}>
          <Button
            aria-label={revealed ? 'Hide value' : 'Reveal value'}
            icon={revealed ? <EyeInvisibleOutlined /> : <EyeOutlined />}
            onClick={() => setRevealed((current) => !current)}
            size="small"
            type="text"
          />
        </Tooltip>
      )}
      <Typography.Text copyable={{ text: variable.value, tooltips: ['Copy value', 'Copied'] }} style={{ fontSize: 12 }} />
    </Flex>
  );
}

/**
 * Environment Variables (GUIDELINES §4.5): shared config for every Compose in
 * one Environment, injected at deploy time. Values are masked until revealed,
 * and a paste-in bulk editor takes a whole `.env` file at once, because that is
 * where these values already live.
 *
 * The variable load itself is the page's (ProjectPage), so a failure joins the
 * screen's error Alert; this panel only renders the wait.
 */
export function VariablesPanel({ environment, onError }: { environment: Environment; onError?: (reason: unknown) => void }) {
  const { deleteVariable, isPending, replaceVariables, saveVariable } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [form] = Form.useForm<{ key: string; value: string }>();
  const [query, setQuery] = useState('');
  const [editing, setEditing] = useState<EnvironmentVariable>();
  const [modalOpen, setModalOpen] = useState(false);
  // Modal submissions keep their failure next to the form (GUIDELINES §8) —
  // the operator edits and resubmits without losing what they typed.
  const [formError, setFormError] = useState<string>();
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkText, setBulkText] = useState('');
  const [bulkError, setBulkError] = useState<string>();

  const loading = isPending(`variables:${environment.id}`);
  const visible = useMemo(() => visibleVariables(environment.variables, query), [environment.variables, query]);

  const parsed = useMemo(() => parseDotEnv(bulkText), [bulkText]);
  const duplicated = useMemo(() => duplicateKeys(parsed), [parsed]);
  const invalid = useMemo(() => invalidKeys(parsed), [parsed]);
  const plan = useMemo(() => replacePlan(environment.variables, parsed), [environment.variables, parsed]);
  const bulkBlocked = parsed.length === 0 || duplicated.length > 0 || invalid.length > 0;

  const openCreate = () => {
    setEditing(undefined);
    setFormError(undefined);
    form.resetFields();
    setModalOpen(true);
  };

  const openEdit = (variable: EnvironmentVariable) => {
    setEditing(variable);
    setFormError(undefined);
    form.setFieldsValue({ key: variable.key, value: variable.value });
    setModalOpen(true);
  };

  const submit = async (values: { key: string; value: string }) => {
    setFormError(undefined);
    try {
      await saveVariable(environment.id, values, editing?.id);
      setModalOpen(false);
      message.success(editing ? `Saved ${values.key}` : `Added ${values.key}`);
    } catch (reason: unknown) {
      setFormError(reason instanceof Error ? reason.message : 'Something went wrong');
    }
  };

  const remove = async (variable: EnvironmentVariable) => {
    try {
      await deleteVariable(environment.id, variable.id);
      message.success(`Deleted ${variable.key}`);
    } catch (reason: unknown) {
      onError?.(reason);
    }
  };

  const openBulk = () => {
    setBulkText('');
    setBulkError(undefined);
    setBulkOpen(true);
  };

  const replaceAll = () => {
    setBulkError(undefined);
    // A keyed notification (GUIDELINES §8): the waiting line is replaced by the
    // outcome rather than stacked on top of it.
    message.open({ content: 'Replacing Environment Variables…', key: 'bulk-variables', type: 'loading' });
    replaceVariables(environment.id, parsed)
      .then(() => {
        setBulkOpen(false);
        message.open({
          content: `Replaced Environment Variables with ${parsed.length} key${parsed.length === 1 ? '' : 's'}`,
          key: 'bulk-variables',
          type: 'success',
        });
      })
      .catch((reason: unknown) => {
        message.destroy('bulk-variables');
        setBulkError(reason instanceof Error ? reason.message : 'Something went wrong');
      });
  };

  return (
    <Card
      extra={
        <Space>
          <Button icon={<SnippetsOutlined />} onClick={openBulk}>
            Paste .env
          </Button>
          <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
            New Variable
          </Button>
        </Space>
      }
      title={`Environment Variables · ${environment.name}`}
      variant="outlined"
    >
      <Paragraph type="secondary">
        Injected into every Compose in this Environment at deploy time. Changing a value takes effect on the next deploy.
      </Paragraph>

      <Alert
        message="Values are stored in the panel's SQLite database and written to a mode-600 env file on deploy."
        showIcon
        style={{ marginBottom: 16 }}
        type="info"
      />

      {environment.variables.length > 0 && (
        <Input
          allowClear
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Filter by key"
          prefix={<SearchOutlined />}
          style={{ marginBottom: 16, maxWidth: 280 }}
          value={query}
        />
      )}

      {loading && environment.variables.length === 0 ? (
        <Flex justify="center" style={{ padding: '48px 0' }}>
          <Spin />
        </Flex>
      ) : environment.variables.length === 0 ? (
        <EmptyState
          action={
            <Space>
              <Button icon={<SnippetsOutlined />} onClick={openBulk}>
                Paste a .env file
              </Button>
              <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                Add a Variable
              </Button>
            </Space>
          }
          description="Anything shared by every Compose here — database URLs, secrets, log level, timezone."
          title="No Environment Variables yet"
        />
      ) : (
        <Table<EnvironmentVariable>
          columns={[
            {
              dataIndex: 'key',
              key: 'key',
              render: (key: string) => (
                <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{key}</Text>
              ),
              title: 'Key',
              width: 260,
            },
            {
              dataIndex: 'value',
              key: 'value',
              render: (_value, variable) => <ValueCell variable={variable} />,
              title: 'Value',
            },
            {
              key: 'type',
              render: (_value, variable) =>
                isSecret(variable) ? (
                  <Tag color="gold" variant="filled">
                    secret
                  </Tag>
                ) : (
                  <Tag variant="outlined">plain</Tag>
                ),
              title: '',
              width: 90,
            },
            {
              dataIndex: 'updatedAt',
              key: 'updatedAt',
              render: (value: string) => (
                <Text type="secondary">
                  <RelativeTime value={value} />
                </Text>
              ),
              title: 'Updated',
              width: 140,
            },
            {
              key: 'actions',
              render: (_value, variable) => (
                <Space size={4}>
                  <Button icon={<EditOutlined />} onClick={() => openEdit(variable)} size="small" type="text" />
                  <Popconfirm
                    cancelText="Keep"
                    description="Composes pick this up on their next deploy."
                    okButtonProps={{ danger: true }}
                    okText="Delete"
                    onConfirm={() => void remove(variable)}
                    title={`Delete ${variable.key}?`}
                  >
                    <Button danger icon={<DeleteOutlined />} loading={isPending(`delete-variable:${variable.id}`)} size="small" type="text" />
                  </Popconfirm>
                </Space>
              ),
              title: '',
              width: 90,
            },
          ]}
          dataSource={visible}
          locale={{ emptyText: <Text type="secondary">No keys match “{query}”.</Text> }}
          pagination={false}
          rowKey="id"
          size="middle"
        />
      )}

      <Modal
        confirmLoading={isPending(`save-variable:${environment.id}`)}
        destroyOnHidden
        okText={editing ? 'Save changes' : 'Add Variable'}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        open={modalOpen}
        title={editing ? `Edit ${editing.key}` : 'New Environment Variable'}
      >
        {formError && (
          <Alert message={formError} showIcon style={{ marginBottom: 16 }} type="error" />
        )}
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
          <Form.Item
            extra="Uppercase with underscores, as your Compose files reference it: ${KEY}"
            label="Key"
            name="key"
            rules={[
              { message: 'Enter a key', required: true },
              { max: 255, message: 'Use 255 characters or fewer' },
              { message: 'Use letters, digits and underscores only', pattern: VARIABLE_KEY_PATTERN },
            ]}
          >
            <Input autoFocus placeholder="DATABASE_URL" style={{ fontFamily: CODE_FONT_STACK }} />
          </Form.Item>
          <Form.Item label="Value" name="value" rules={[{ message: 'Enter a value', required: true }]}>
            <Input placeholder="postgres://user:pass@db:5432/app" style={{ fontFamily: CODE_FONT_STACK }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        confirmLoading={isPending(`bulk-variables:${environment.id}`)}
        destroyOnHidden
        okButtonProps={{ disabled: bulkBlocked }}
        okText={`Replace ${environment.variables.length} variable${environment.variables.length === 1 ? '' : 's'} with ${parsed.length}`}
        onCancel={() => setBulkOpen(false)}
        onOk={replaceAll}
        open={bulkOpen}
        title="Paste a .env file"
        width={720}
      >
        <Paragraph type="secondary">
          One <Text code>KEY=value</Text> per line. Comments and blank lines are ignored, quotes are stripped. This
          <strong> replaces</strong> the Environment's variables — keys missing from the paste are removed.
        </Paragraph>
        <Input.TextArea
          onChange={(event) => setBulkText(event.target.value)}
          placeholder={'DATABASE_URL=postgres://app:pw@db:5432/app\nLOG_LEVEL=info\n# comments are ignored\nTZ="Asia/Bangkok"'}
          rows={8}
          style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}
          value={bulkText}
        />
        {duplicated.length > 0 && (
          <Alert
            message={`Duplicate keys: ${duplicated.join(', ')}`}
            showIcon
            style={{ marginTop: 12 }}
            type="error"
          />
        )}
        {invalid.length > 0 && (
          <Alert
            message={`Keys a Compose file cannot reference: ${invalid.join(', ')}`}
            description="Use letters, digits and underscores, not starting with a digit — otherwise ${KEY} cannot pick the value up."
            showIcon
            style={{ marginTop: 12 }}
            type="error"
          />
        )}
        {bulkError && (
          <Alert message={bulkError} showIcon style={{ marginTop: 12 }} type="error" />
        )}
        {parsed.length > 0 && !bulkBlocked && (
          <div style={{ marginTop: 12 }}>
            <Text strong>Will write {parsed.length} variables</Text>
            {(plan.added.length > 0 || plan.removed.length > 0) && (
              <Text style={{ display: 'block', fontSize: 12 }} type="secondary">
                {plan.added.length > 0 && `${plan.added.length} added`}
                {plan.added.length > 0 && plan.removed.length > 0 && ' · '}
                {plan.removed.length > 0 && (
                  <>
                    removes {plan.removed.join(', ')}
                  </>
                )}
              </Text>
            )}
            <Table
              columns={[
                { dataIndex: 'key', key: 'key', render: (key: string) => <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{key}</Text>, title: 'Key', width: 240 },
                {
                  dataIndex: 'value',
                  key: 'value',
                  render: (value: string) => (
                    <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }} type="secondary">
                      {value.length > 40 ? `${value.slice(0, 40)}…` : value || '—'}
                    </Text>
                  ),
                  title: 'Value',
                },
              ]}
              dataSource={parsed}
              pagination={false}
              rowKey={(row) => row.key}
              scroll={{ y: 220 }}
              size="small"
              style={{ marginTop: 8 }}
            />
          </div>
        )}
      </Modal>
      <div style={{ borderTop: `1px solid ${token.colorBorderSecondary}`, marginTop: 16, paddingTop: 12 }}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {environment.variables.length} variable{environment.variables.length === 1 ? '' : 's'} · applied to{' '}
          {environment.composes.length} Compose{environment.composes.length === 1 ? '' : 's'} in this Environment
        </Text>
      </div>
    </Card>
  );
}
