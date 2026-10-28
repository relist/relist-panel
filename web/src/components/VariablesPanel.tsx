import { DeleteOutlined, EditOutlined, EyeInvisibleOutlined, EyeOutlined, PlusOutlined, SearchOutlined, SnippetsOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, Popconfirm, Space, Spin, Switch, Table, Tag, Tooltip, Typography, theme } from 'antd';
import { useMemo, useRef, useState, type ReactNode } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel, type NewVariable } from '../data/store';
import type { Environment } from '../data/types';
import { EmptyState, RelativeTime } from './primitives';
import {
  duplicateKeys,
  invalidKeys,
  isSecret,
  isSecretKey,
  maskedValue,
  parseDotEnv,
  pasteSecretFlags,
  replacePlan,
  VARIABLE_KEY_PATTERN,
  visibleVariables,
} from './variables-view';

const { Text, Paragraph } = Typography;

/** One row of any variable list the editor shows: an Environment Variable, a Compose Variable, later a Variable Set entry. */
export type VariableRow = {
  id: number;
  key: string;
  secret: boolean;
  value: string;
  /** Absent for lists whose rows carry no timestamp; the Updated column is then left out. */
  updatedAt?: string;
};

/** One value cell: masked until revealed, copy always at hand (GUIDELINES §4.5). */
export function ValueCell({ variable }: { variable: Pick<VariableRow, 'secret' | 'value'> }) {
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
 * The key-value editor behind every variable list (GUIDELINES §4.5): masked
 * values with reveal and copy, a key filter, and an add/edit modal with the
 * secret toggle. The list and every operation come in as props, so the
 * Environment's Variables, a Compose's Variables and (later) a Variable Set's
 * entries share one editor. A paste-in bulk editor appears when `onReplace` is
 * given, because for an Environment that is where the values already live.
 *
 * The load itself is the page's, so a failure joins the screen's error Alert;
 * this editor only renders the wait.
 */
export type VariablesEditorProps = {
  title: string;
  /** The standing note under the title. */
  description: ReactNode;
  /** Plural noun for messages and the bulk editor, e.g. "Environment Variables". */
  noun: string;
  variables: VariableRow[];
  loading: boolean;
  saving: boolean;
  isDeleting: (variable: VariableRow) => boolean;
  /** Rejects with the message the modal shows next to the form. */
  onSave: (values: NewVariable, id?: number) => Promise<void>;
  onDelete: (variable: VariableRow) => Promise<void>;
  onError?: (reason: unknown) => void;
  /** Present: the Paste .env editor is offered, and confirming replaces the whole list. */
  onReplace?: (rows: NewVariable[]) => Promise<void>;
  replacing?: boolean;
  /** Present: the stored-in-SQLite notice above the table. */
  notice?: string;
  emptyTitle: string;
  emptyDescription: string;
  /** Shown in the delete confirmation. */
  deleteNote: string;
  /** Extra content under a row's value, e.g. the override hint. */
  annotate?: (variable: VariableRow) => ReactNode;
  footer?: ReactNode;
  /** True: while the list is empty the card header drops its add button, leaving the empty state's as the only one. */
  singleAddWhenEmpty?: boolean;
  /**
   * True: the table alone, for a list that sits in someone else's card or tab
   * (the Compose page). No card, title, description, notice, footer or type
   * column; row actions show on hover; add and paste are links under the
   * table; the filter waits for more than {@link BARE_FILTER_THRESHOLD} rows.
   */
  bare?: boolean;
  /** The bare rendering's add link, e.g. "Add entry". */
  addLabel?: string;
};

/** How many rows a bare list holds before it offers the key filter. */
export const BARE_FILTER_THRESHOLD = 10;

export function VariablesEditor({
  addLabel = 'Add variable',
  annotate,
  bare = false,
  deleteNote,
  description,
  emptyDescription,
  emptyTitle,
  footer,
  isDeleting,
  loading,
  noun,
  notice,
  onDelete,
  onError,
  onReplace,
  onSave,
  replacing = false,
  saving,
  singleAddWhenEmpty = false,
  title,
  variables,
}: VariablesEditorProps) {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const [form] = Form.useForm<{ key: string; value: string; secret: boolean }>();
  const [query, setQuery] = useState('');
  const [editing, setEditing] = useState<VariableRow>();
  const [modalOpen, setModalOpen] = useState(false);
  // Modal submissions keep their failure next to the form (GUIDELINES §8) —
  // the operator edits and resubmits without losing what they typed.
  const [formError, setFormError] = useState<string>();
  const [bulkOpen, setBulkOpen] = useState(false);
  const [bulkText, setBulkText] = useState('');
  const [bulkError, setBulkError] = useState<string>();
  // Whether the user has answered the secret toggle themselves this modal
  // session — until then, while creating, the toggle follows the heuristic's
  // answer for the typed key.
  const secretTouched = useRef(false);

  const visible = useMemo(() => visibleVariables(variables, query), [variables, query]);

  const parsed = useMemo(() => parseDotEnv(bulkText), [bulkText]);
  const duplicated = useMemo(() => duplicateKeys(parsed), [parsed]);
  const invalid = useMemo(() => invalidKeys(parsed), [parsed]);
  const plan = useMemo(() => replacePlan(variables, parsed), [variables, parsed]);
  const bulkBlocked = parsed.length === 0 || duplicated.length > 0 || invalid.length > 0;

  const openCreate = () => {
    setEditing(undefined);
    setFormError(undefined);
    secretTouched.current = false;
    form.resetFields();
    setModalOpen(true);
  };

  const openEdit = (variable: VariableRow) => {
    setEditing(variable);
    setFormError(undefined);
    form.setFieldsValue({ key: variable.key, value: variable.value, secret: variable.secret });
    setModalOpen(true);
  };

  const submit = async (values: { key: string; value: string; secret: boolean }) => {
    setFormError(undefined);
    try {
      await onSave(values, editing?.id);
      setModalOpen(false);
      message.success(editing ? `Saved ${values.key}` : `Added ${values.key}`);
    } catch (reason: unknown) {
      setFormError(reason instanceof Error ? reason.message : 'Something went wrong');
    }
  };

  const remove = async (variable: VariableRow) => {
    try {
      await onDelete(variable);
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
    if (!onReplace) return;
    setBulkError(undefined);
    // A keyed notification (GUIDELINES §8): the waiting line is replaced by the
    // outcome rather than stacked on top of it.
    message.open({ content: `Replacing ${noun}…`, key: 'bulk-variables', type: 'loading' });
    onReplace(pasteSecretFlags(variables, parsed))
      .then(() => {
        setBulkOpen(false);
        message.open({
          content: `Replaced ${noun} with ${parsed.length} key${parsed.length === 1 ? '' : 's'}`,
          key: 'bulk-variables',
          type: 'success',
        });
      })
      .catch((reason: unknown) => {
        message.destroy('bulk-variables');
        setBulkError(reason instanceof Error ? reason.message : 'Something went wrong');
      });
  };

  const filter = (bare ? variables.length > BARE_FILTER_THRESHOLD : variables.length > 0) && (
    <Input
      allowClear
      onChange={(event) => setQuery(event.target.value)}
      placeholder="Filter by key"
      prefix={<SearchOutlined />}
      style={{ marginBottom: bare ? 12 : 16, maxWidth: 280 }}
      value={query}
    />
  );

  const table = (
    <Table<VariableRow>
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
          render: (_value, variable) => (
            <>
              <ValueCell variable={variable} />
              {annotate?.(variable)}
            </>
          ),
          title: 'Value',
        },
        // The bare list drops the tag: the mask and its eye already say which values are secret.
        ...(bare
          ? []
          : [
              {
                key: 'type',
                render: (_value: unknown, variable: VariableRow) =>
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
            ]),
        ...(variables.some((variable) => variable.updatedAt)
          ? [
              {
                dataIndex: 'updatedAt',
                key: 'updatedAt',
                render: (value?: string) =>
                  value ? (
                    <Text type="secondary">
                      <RelativeTime value={value} />
                    </Text>
                  ) : null,
                title: 'Updated',
                width: 140,
              },
            ]
          : []),
        {
          key: 'actions',
          render: (_value, variable) => (
            <Space className={bare ? 'rp-row-actions' : undefined} size={4}>
              <Button aria-label={`Edit ${variable.key}`} icon={<EditOutlined />} onClick={() => openEdit(variable)} size="small" type="text" />
              <Popconfirm
                cancelText="Keep"
                description={deleteNote}
                okButtonProps={{ danger: true }}
                okText="Delete"
                onConfirm={() => void remove(variable)}
                title={`Delete ${variable.key}?`}
              >
                <Button aria-label={`Delete ${variable.key}`} danger icon={<DeleteOutlined />} loading={isDeleting(variable)} size="small" type="text" />
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
      // At phone width the table scrolls inside the card rather than crushing the value column.
      scroll={{ x: 'max-content' }}
      size={bare ? 'small' : 'middle'}
    />
  );

  const modals = (
    <>
  <Modal
    confirmLoading={saving}
    destroyOnHidden
    okText={editing ? 'Save changes' : 'Add Variable'}
    onCancel={() => setModalOpen(false)}
    onOk={() => form.submit()}
    open={modalOpen}
    title={editing ? `Edit ${editing.key}` : 'New Variable'}
  >
    {formError && (
      <Alert title={formError} showIcon style={{ marginBottom: 16 }} type="error" />
    )}
    <Form
      form={form}
      initialValues={{ secret: false }}
      layout="vertical"
      onFinish={(values) => void submit(values)}
      onValuesChange={(changed) => {
        if (changed.secret !== undefined) {
          secretTouched.current = true;
        }
        // While creating, the toggle defaults to the heuristic's answer
        // for the typed key — the guess's only job — until the user
        // answers it themselves. Editing starts from the stored flag and
        // the key cannot re-suggest.
        if (!editing && changed.key !== undefined && !secretTouched.current) {
          form.setFieldsValue({ secret: isSecretKey(changed.key) });
        }
      }}
      requiredMark={false}
    >
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
      <Form.Item
        extra="Masks the value on screen — a display tier, not encryption. The value is stored and served as any other."
        label="Secret"
        name="secret"
        valuePropName="checked"
      >
        <Switch checkedChildren="secret" unCheckedChildren="plain" />
      </Form.Item>
    </Form>
  </Modal>

  <Modal
    confirmLoading={replacing}
    destroyOnHidden
    okButtonProps={{ disabled: bulkBlocked }}
    okText={`Replace ${variables.length} variable${variables.length === 1 ? '' : 's'} with ${parsed.length}`}
    onCancel={() => setBulkOpen(false)}
    onOk={replaceAll}
    open={bulkOpen}
    title="Paste a .env file"
    width={720}
  >
    <Paragraph type="secondary">
      One <Text code>KEY=value</Text> per line. Comments and blank lines are ignored, quotes are stripped. This
      <strong> replaces</strong> the existing variables — keys missing from the paste are removed.
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
        title={`Duplicate keys: ${duplicated.join(', ')}`}
        showIcon
        style={{ marginTop: 12 }}
        type="error"
      />
    )}
    {invalid.length > 0 && (
      <Alert
        title={`Keys a Compose file cannot reference: ${invalid.join(', ')}`}
        description="Use letters, digits and underscores, not starting with a digit — otherwise ${KEY} cannot pick the value up."
        showIcon
        style={{ marginTop: 12 }}
        type="error"
      />
    )}
    {bulkError && (
      <Alert title={bulkError} showIcon style={{ marginTop: 12 }} type="error" />
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
    </>
  );

  if (bare) {
    return (
      <>
        {filter}
        {loading && variables.length === 0 ? (
          <Flex justify="center" style={{ padding: '24px 0' }}>
            <Spin />
          </Flex>
        ) : variables.length === 0 ? (
          <Text type="secondary">{emptyDescription}</Text>
        ) : (
          table
        )}
        <Flex gap={16} style={{ marginTop: 8 }} wrap>
          <Button icon={<PlusOutlined />} onClick={openCreate} size="small" style={{ paddingInline: 0 }} type="link">
            {addLabel}
          </Button>
          {onReplace && (
            <Button icon={<SnippetsOutlined />} onClick={openBulk} size="small" style={{ paddingInline: 0 }} type="link">
              Paste .env
            </Button>
          )}
        </Flex>
        {modals}
      </>
    );
  }

  return (
    <Card
      extra={
        <Space>
          {onReplace && (
            <Button icon={<SnippetsOutlined />} onClick={openBulk}>
              Paste .env
            </Button>
          )}
          {!(singleAddWhenEmpty && variables.length === 0 && !loading) && (
            <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
              New Variable
            </Button>
          )}
        </Space>
      }
      title={title}
      variant="outlined"
    >
      <Paragraph type="secondary">{description}</Paragraph>

      {notice && <Alert title={notice} showIcon style={{ marginBottom: 16 }} type="info" />}

      {filter}

      {loading && variables.length === 0 ? (
        <Flex justify="center" style={{ padding: '48px 0' }}>
          <Spin />
        </Flex>
      ) : variables.length === 0 ? (
        <EmptyState
          action={
            <Space>
              {onReplace && (
                <Button icon={<SnippetsOutlined />} onClick={openBulk}>
                  Paste a .env file
                </Button>
              )}
              <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
                Add a Variable
              </Button>
            </Space>
          }
          description={emptyDescription}
          title={emptyTitle}
        />
      ) : (
        table
      )}

      {modals}
      <div style={{ borderTop: `1px solid ${token.colorBorderSecondary}`, marginTop: 16, paddingTop: 12 }}>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {variables.length} variable{variables.length === 1 ? '' : 's'}
          {footer}
        </Text>
      </div>
    </Card>
  );
}

/**
 * Environment Variables (GUIDELINES §4.5): shared config for every Compose in
 * one Environment, injected at deploy time. The variable load itself is the
 * page's (ProjectPage), so a failure joins the screen's error Alert.
 */
export function VariablesPanel({ environment, onError }: { environment: Environment; onError?: (reason: unknown) => void }) {
  const { deleteVariable, isPending, replaceVariables, saveVariable } = usePanel();

  return (
    <VariablesEditor
      deleteNote="Composes pick this up on their next deploy."
      description={
        <>
          Injected into every Compose in this Environment at deploy time. Changing a value takes effect on the next deploy.
          <br />
          Used by every Compose · usable as <Text code>{'${VAR}'}</Text>
        </>
      }
      emptyDescription="Anything shared by every Compose here — database URLs, secrets, log level, timezone."
      emptyTitle="No Environment Variables yet"
      footer={
        <>
          {' '}
          · applied to {environment.composes.length} Compose{environment.composes.length === 1 ? '' : 's'} in this Environment
        </>
      }
      isDeleting={(variable) => isPending(`delete-variable:${variable.id}`)}
      loading={isPending(`variables:${environment.id}`)}
      noun="Environment Variables"
      notice="Values are stored in the panel's SQLite database and written to a mode-600 env file on deploy."
      onDelete={(variable) => deleteVariable(environment.id, variable.id)}
      onError={onError}
      onReplace={(rows) => replaceVariables(environment.id, rows)}
      onSave={(values, id) => saveVariable(environment.id, values, id)}
      replacing={isPending(`bulk-variables:${environment.id}`)}
      saving={isPending(`save-variable:${environment.id}`)}
      title={`Environment Variables · ${environment.name}`}
      variables={environment.variables}
    />
  );
}
