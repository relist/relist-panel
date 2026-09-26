import { DeleteOutlined, EditOutlined, EyeInvisibleOutlined, EyeOutlined, PlusOutlined, SearchOutlined, SnippetsOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, Popconfirm, Space, Table, Tag, Tooltip, Typography, theme } from 'antd';
import { useLayoutEffect, useMemo } from 'react';
import { CODE_FONT_STACK } from '../app/theme';
import { publishSceneHandle } from '../app/sceneAdapters';
import { useUiState } from '../app/uiState';
import { EmptyState, RelativeTime } from './primitives';
import { usePanel } from '../data/store';
import type { Environment, EnvironmentVariable } from '../data/types';
import { useSceneContainer } from '../scenes/scope';

const { Text, Paragraph } = Typography;

/**
 * The fixture `.env` the paste scenes seed (ticket 02, audit §3b: the paste is
 * the state; §5.10: a document, so it arrives through the
 * `fixture:variables.bulk*` adapters, not a URL literal). It exercises the
 * parser's stated shape: comments, quoted values, secrets.
 */
const BULK_FIXTURE = `DATABASE_URL=postgres://app:pw@db:5432/app
LOG_LEVEL=info
STRIPE_WEBHOOK_SECRET=whsec_9f27kad81m2v
TZ="Asia/Bangkok"
# comments are ignored
FEATURE_FLAGS=variant-a`;

/** The duplicate-key refusal: one repeated key is enough to disable the OK. */
const BULK_FIXTURE_DUPLICATE = `${BULK_FIXTURE}
LOG_LEVEL=debug`;

/**
 * One variable row's delete confirm (audit §5.5's per-row `Popconfirm`,
 * addressed as `?ui=variables.deletePopconfirm.DATABASE_URL=1`). Confirming
 * stays the caller's real `onConfirm`; only the open state is addressable.
 */
function DeleteVariableButton({
  loading,
  onDelete,
  variable,
}: {
  loading: boolean;
  onDelete: () => void;
  variable: EnvironmentVariable;
}) {
  const [open, setOpen] = useUiState(`variables.deletePopconfirm.${variable.key}`, false);
  return (
    <Popconfirm
      cancelText="Keep"
      description="Composes pick this up on their next deploy."
      okButtonProps={{ danger: true }}
      okText="Delete"
      onConfirm={onDelete}
      onOpenChange={setOpen}
      open={open}
      title={`Delete ${variable.key}?`}
    >
      <Button danger icon={<DeleteOutlined />} loading={loading} size="small" type="text" />
    </Popconfirm>
  );
}

/** `.env` parsing for the bulk editor. Mirrors what `docker compose` accepts. */
export function parseDotEnv(text: string): { key: string; value: string }[] {
  const rows: { key: string; value: string }[] = [];
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const withoutExport = line.startsWith('export ') ? line.slice(7).trim() : line;
    const separator = withoutExport.indexOf('=');
    if (separator <= 0) continue;
    const key = withoutExport.slice(0, separator).trim();
    let value = withoutExport.slice(separator + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (key) rows.push({ key, value });
  }
  return rows;
}

function ValueCell({ variable }: { variable: EnvironmentVariable }) {
  // Ticket 02, decision D2: reveal is per row, addressed by naming the key —
  // `?ui=variables.revealed.DATABASE_URL=1` reveals exactly that row.
  const [revealed, setRevealed] = useUiState(`variables.revealed.${variable.key}`, false);
  const masked = '•'.repeat(Math.min(16, Math.max(8, variable.value.length)));

  return (
    <Flex align="center" gap={6}>
      <Text style={{ fontFamily: CODE_FONT_STACK, fontSize: 12 }}>{variable.secret && !revealed ? masked : variable.value}</Text>
      {variable.secret && (
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
 * Environment Variables: shared config for every Compose in one Environment.
 * Values are masked by default and there is a paste-in bulk editor, because
 * these are typed in from `.env` files an operator already has.
 */
export function VariablesPanel({ environment }: { environment: Environment }) {
  const { deleteVariable, isPending, replaceVariables, saveVariable } = usePanel();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [form] = Form.useForm<{ key: string; value: string }>();
  const [query, setQuery] = useUiState('variables.query', '');
  // `editing` holds an `EnvironmentVariable` and `bulkText` a whole `.env`
  // document (audit §1, CONVERT* / §5.10) — never forced by a URL literal
  // (ticket 02, decision D1): the `object:`/`fixture:` adapters seed them
  // through this panel's own setters, via the handles published below.
  const [editing, setEditing] = useUiState<EnvironmentVariable | undefined>('variables.editing', undefined);
  const [modalOpen, setModalOpen] = useUiState('variables.modalOpen', false);
  const [bulkOpen, setBulkOpen] = useUiState('variables.bulkOpen', false);
  const [bulkText, setBulkText] = useUiState('variables.bulkText', '');
  const sceneContainer = useSceneContainer();

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const sorted = [...environment.variables].sort((a, b) => a.key.localeCompare(b.key));
    return needle ? sorted.filter((variable) => variable.key.toLowerCase().includes(needle)) : sorted;
  }, [environment.variables, query]);

  const parsed = useMemo(() => parseDotEnv(bulkText), [bulkText]);
  const duplicateKeys = parsed.filter((row, index) => parsed.findIndex((other) => other.key === row.key) !== index);

  const openCreate = () => {
    setEditing(undefined);
    form.resetFields();
    setModalOpen(true);
  };

  const openEdit = (variable: EnvironmentVariable) => {
    setEditing(variable);
    form.setFieldsValue({ key: variable.key, value: variable.value });
    setModalOpen(true);
  };

  // Ticket 02: the scene adapters reach this panel's own transitions through
  // these handles. Each is idempotent — a seed finds its target already set.
  useLayoutEffect(
    () =>
      publishSceneHandle('variables.editing.seed', () => {
        const target =
          environment.variables.find((variable) => variable.key === 'DATABASE_URL') ?? environment.variables[0];
        if (target) openEdit(target);
      }),
    [environment.variables, openEdit],
  );

  useLayoutEffect(
    () => publishSceneHandle('variables.bulkText.fixture', () => setBulkText(BULK_FIXTURE)),
    [setBulkText],
  );

  useLayoutEffect(
    () => publishSceneHandle('variables.bulkDuplicate.fixture', () => setBulkText(BULK_FIXTURE_DUPLICATE)),
    [setBulkText],
  );

  const submit = async (values: { key: string; value: string }) => {
    await saveVariable(environment.id, values, editing?.id);
    setModalOpen(false);
    message.success(editing ? `Saved ${values.key}` : `Added ${values.key}`);
  };

  return (
    <Card
      extra={
        <Space>
          <Button icon={<SnippetsOutlined />} onClick={() => { setBulkText(''); setBulkOpen(true); }}>
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

      {environment.variables.length === 0 ? (
        <EmptyState
          action={
            <Space>
              <Button icon={<SnippetsOutlined />} onClick={() => { setBulkText(''); setBulkOpen(true); }}>
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
                variable.secret ? (
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
                  <DeleteVariableButton
                    loading={isPending(`delete-variable:${variable.id}`)}
                    onDelete={() => void deleteVariable(environment.id, variable.id)}
                    variable={variable}
                  />
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
        getContainer={sceneContainer}
        okText={editing ? 'Save changes' : 'Add Variable'}
        onCancel={() => setModalOpen(false)}
        onOk={() => form.submit()}
        open={modalOpen}
        title={editing ? `Edit ${editing.key}` : 'New Environment Variable'}
      >
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
          <Form.Item
            extra="Uppercase with underscores, as your Compose files reference it: ${KEY}"
            label="Key"
            name="key"
            rules={[
              { message: 'Enter a key', required: true },
              { max: 255, message: 'Use 255 characters or fewer' },
              { message: 'Use letters, digits and underscores only', pattern: /^[A-Za-z_][A-Za-z0-9_]*$/ },
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
        getContainer={sceneContainer}
        okButtonProps={{ disabled: parsed.length === 0 || duplicateKeys.length > 0 }}
        okText={`Replace ${environment.variables.length} variable${environment.variables.length === 1 ? '' : 's'} with ${parsed.length}`}
        onCancel={() => setBulkOpen(false)}
        onOk={() => {
          void replaceVariables(environment.id, parsed).then(() => {
            setBulkOpen(false);
            message.success(`Replaced Environment Variables with ${parsed.length} keys`);
          });
        }}
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
        {duplicateKeys.length > 0 && (
          <Alert
            message={`Duplicate keys: ${[...new Set(duplicateKeys.map((row) => row.key))].join(', ')}`}
            showIcon
            style={{ marginTop: 12 }}
            type="error"
          />
        )}
        {parsed.length > 0 && duplicateKeys.length === 0 && (
          <div style={{ marginTop: 12 }}>
            <Text strong>Will write {parsed.length} variables</Text>
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
