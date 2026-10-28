import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, Popconfirm, Skeleton, Space, Tabs, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { usePanel } from '../data/store';
import type { Compose, VariableSet } from '../data/types';
import { deleteSetNote } from './bindings-view';
import { EmptyState, InfoTitle } from './primitives';
import { VariablesEditor } from './VariablesPanel';

const { Text } = Typography;

/** The server's rule for a set name, repeated so the form can say what is wrong before a round trip. */
const SET_NAME_PATTERN = /^[a-z][a-z0-9-]{0,62}$/;

const nameRules = [
  { message: 'Enter a name', required: true },
  { message: 'Lowercase letters, digits and dashes, starting with a letter, up to 63 characters', pattern: SET_NAME_PATTERN },
];

/**
 * The Compose's Variable Sets, one tab per set (GUIDELINES §4.6): the open
 * set's bound Services as a line of text, then the bare key-value table of its
 * entries. New set sits in the card header; Rename and Delete sit in each
 * tab's pane. Binding happens only in the Service bindings card. The list belongs to
 * the Variables tab (the bindings card reads it too); every write asks the tab
 * to reload it, since a write also changes counts and "bound to".
 */
export function VariableSetsCard({
  compose,
  error,
  onError,
  onReload,
  sets,
}: {
  compose: Compose;
  /** Why the list could not be loaded; shown with a Retry while there is no list. */
  error?: string;
  onError?: (reason: unknown) => void;
  onReload: () => Promise<void> | void;
  /** `undefined` while the first load is in flight. */
  sets?: VariableSet[];
}) {
  const { createVariableSet, deleteSetEntry, deleteVariableSet, isPending, renameVariableSet, replaceSetEntries, saveSetEntry } = usePanel();
  const { message } = App.useApp();
  const [form] = Form.useForm<{ name: string }>();
  const [renameForm] = Form.useForm<{ name: string }>();
  const composeId = compose.id;
  const reload = onReload;
  const [active, setActive] = useState<string>();
  // A set just created opens once the reload lists it.
  const [created, setCreated] = useState<string>();
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState<string>();
  const [renaming, setRenaming] = useState<VariableSet>();
  const [renameError, setRenameError] = useState<string>();

  useEffect(() => {
    const set = created ? sets?.find((candidate) => candidate.name === created) : undefined;
    if (set) {
      setActive(String(set.id));
      setCreated(undefined);
    }
  }, [created, sets]);

  // The open tab, or the first one when it was deleted or never chosen.
  const activeSet = sets?.find((set) => String(set.id) === active) ?? sets?.[0];

  const openCreate = () => {
    setCreateError(undefined);
    form.resetFields();
    setCreateOpen(true);
  };

  const create = async ({ name }: { name: string }) => {
    setCreateError(undefined);
    try {
      await createVariableSet(composeId, name);
      setCreateOpen(false);
      setCreated(name);
      message.success(`Created ${name}`);
      await reload();
    } catch (reason: unknown) {
      setCreateError(reason instanceof Error ? reason.message : 'Could not create the set');
    }
  };

  const openRename = (set: VariableSet) => {
    setRenameError(undefined);
    setRenaming(set);
  };

  const rename = async ({ name }: { name: string }) => {
    if (!renaming) return;
    setRenameError(undefined);
    try {
      await renameVariableSet(composeId, renaming.id, name);
      setRenaming(undefined);
      message.success(`Renamed to ${name}`);
      await reload();
    } catch (reason: unknown) {
      setRenameError(reason instanceof Error ? reason.message : 'Could not rename the set');
    }
  };

  const remove = async (set: VariableSet) => {
    try {
      await deleteVariableSet(composeId, set.id);
      message.success(`Deleted ${set.name}`);
    } catch (reason: unknown) {
      onError?.(reason);
    }
    await reload();
  };

  // In the pane, not the tab bar: at phone width the bar needs every pixel for the tabs themselves.
  const setActions = (set: VariableSet) => (
    <Space size={4}>
      <Button aria-label={`Rename ${set.name}`} icon={<EditOutlined />} onClick={() => openRename(set)} size="small" type="text">
        Rename
      </Button>
      <Popconfirm
        cancelText="Keep"
        description={deleteSetNote(set.boundServices)}
        okButtonProps={{ danger: true }}
        okText="Delete"
        onConfirm={() => void remove(set)}
        title={`Delete ${set.name}?`}
      >
        <Button aria-label={`Delete ${set.name}`} danger icon={<DeleteOutlined />} loading={isPending(`delete-variable-set:${set.id}`)} size="small" type="text">
          Delete
        </Button>
      </Popconfirm>
    </Space>
  );

  return (
    <Card
      extra={
        sets && sets.length > 0 ? (
          <Button icon={<PlusOutlined />} onClick={openCreate} size="small">
            New set
          </Button>
        ) : undefined
      }
      size="small"
      style={{ marginTop: 16 }}
      title={
        <InfoTitle
          info={
            <>
              Named groups of variables for the Services you bind them to. Not usable as <Text code>{'${VAR}'}</Text>.
            </>
          }
          title="Variable Sets"
        />
      }
      variant="outlined"
    >
      {!sets && !error ? (
        <Skeleton active paragraph={{ rows: 2 }} title={false} />
      ) : !sets ? (
        <Alert action={<Button onClick={() => void reload()}>Retry</Button>} showIcon title={error} type="error" />
      ) : sets.length === 0 ? (
        <EmptyState
          action={
            <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
              New set
            </Button>
          }
          description="Group variables for particular Services of this Compose, such as database credentials only the db Service should see."
          title="No Variable Sets yet"
        />
      ) : (
        <Tabs
          activeKey={activeSet && String(activeSet.id)}
          items={sets.map((set) => ({
            children: (
              <>
                <Flex align="center" gap={8} justify="space-between" style={{ marginBottom: 8 }} wrap>
                  <Text type="secondary">
                    {set.boundServices.length === 0 ? (
                      'Not bound to any Service yet'
                    ) : (
                      <>
                        Bound to{' '}
                        {set.boundServices.map((service, index) => (
                          <span key={service}>
                            {index > 0 && ', '}
                            <Text style={{ fontFamily: CODE_FONT_STACK }}>{service}</Text>
                          </span>
                        ))}
                      </>
                    )}
                  </Text>
                  {setActions(set)}
                </Flex>
                <VariablesEditor
                  addLabel="Add entry"
                  bare
                  deleteNote="Services bound to this set lose the key on their next deploy."
                  description={null}
                  emptyDescription="No entries yet. Add the keys the bound Services need."
                  emptyTitle="No entries yet"
                  isDeleting={(entry) => isPending(`delete-set-entry:${entry.id}`)}
                  loading={false}
                  noun={`${set.name} entries`}
                  onDelete={async (entry) => {
                    await deleteSetEntry(composeId, set.id, entry.id);
                    await reload();
                  }}
                  onError={onError}
                  onReplace={async (rows) => {
                    try {
                      await replaceSetEntries(composeId, set.id, rows);
                    } finally {
                      // A replace that failed partway still wrote some rows: show what the server has.
                      await reload();
                    }
                  }}
                  onSave={async (values, id) => {
                    await saveSetEntry(composeId, set.id, values, id);
                    await reload();
                  }}
                  replacing={isPending(`bulk-set-entries:${set.id}`)}
                  saving={isPending(`save-set-entry:${set.id}`)}
                  title={set.name}
                  variables={set.entries}
                />
              </>
            ),
            key: String(set.id),
            label: (
              <Flex align="center" gap={6}>
                <span style={{ fontFamily: CODE_FONT_STACK }}>{set.name}</span>
                <Text style={{ fontSize: 12 }} type="secondary">
                  {set.entries.length}
                </Text>
              </Flex>
            ),
          }))}
          onChange={setActive}
        />
      )}

      <Modal
        confirmLoading={isPending(`create-variable-set:${composeId}`)}
        destroyOnHidden
        okText="Create set"
        onCancel={() => setCreateOpen(false)}
        onOk={() => form.submit()}
        open={createOpen}
        title="New Variable Set"
      >
        {createError && <Alert showIcon style={{ marginBottom: 16 }} title={createError} type="error" />}
        <Form form={form} layout="vertical" name="create-variable-set" onFinish={(values) => void create(values)} requiredMark={false}>
          <Form.Item extra="Lowercase letters, digits and dashes, e.g. db-credentials" label="Name" name="name" rules={nameRules}>
            <Input autoFocus placeholder="db-credentials" style={{ fontFamily: CODE_FONT_STACK }} />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        confirmLoading={renaming ? isPending(`rename-variable-set:${renaming.id}`) : false}
        destroyOnHidden
        okText="Rename"
        onCancel={() => setRenaming(undefined)}
        onOk={() => renameForm.submit()}
        open={renaming !== undefined}
        title={`Rename ${renaming?.name ?? ''}`}
      >
        {renameError && <Alert showIcon style={{ marginBottom: 16 }} title={renameError} type="error" />}
        <Form
          form={renameForm}
          initialValues={{ name: renaming?.name }}
          layout="vertical"
          name="rename-variable-set"
          onFinish={(values) => void rename(values)} requiredMark={false}>
          <Form.Item extra="Lowercase letters, digits and dashes" label="Name" name="name" rules={nameRules}>
            <Input autoFocus style={{ fontFamily: CODE_FONT_STACK }} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
