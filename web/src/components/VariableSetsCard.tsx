import { CheckOutlined, CloseOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Collapse, Flex, Form, Input, Modal, Popconfirm, Skeleton, Space, Tag, Typography } from 'antd';
import { useCallback, useEffect, useRef, useState } from 'react';

import { CODE_FONT_STACK } from '../app/theme';
import { ApiError } from '../data/api';
import { usePanel } from '../data/store';
import type { Environment, VariableSet } from '../data/types';
import { entryCountLabel, usageLabel } from './bindings-view';
import { EmptyState } from './primitives';
import { VariablesEditor } from './VariablesPanel';

const { Paragraph, Text } = Typography;

/** The server's rule for a set name, repeated so the form can say what is wrong before a round trip. */
const SET_NAME_PATTERN = /^[a-z][a-z0-9-]{0,62}$/;

const nameRules = [
  { message: 'Enter a name', required: true },
  { message: 'Lowercase letters, digits and dashes, starting with a letter, up to 63 characters', pattern: SET_NAME_PATTERN },
];

/** A delete the API refused because Services still bind the set. */
type Refusal = { setName: string; message: string; usedBy: string[] };

/**
 * The Environment's Variable Sets: collapsible rows with name, entry count and
 * who binds them; expanding one shows the shared key-value editor for its
 * entries. Sets live on the API, so the card loads its own list and reloads
 * after every write (a write also changes counts and "used by").
 */
export function VariableSetsCard({ environment, onError }: { environment: Environment; onError?: (reason: unknown) => void }) {
  const { createVariableSet, deleteSetEntry, deleteVariableSet, isPending, loadVariableSets, renameVariableSet, saveSetEntry } = usePanel();
  const { message } = App.useApp();
  const [form] = Form.useForm<{ name: string }>();
  const environmentId = environment.id;
  const [data, setData] = useState<{ environmentId: number; sets?: VariableSet[]; error?: string }>();
  const [expanded, setExpanded] = useState<string[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [createError, setCreateError] = useState<string>();
  const [renaming, setRenaming] = useState<{ id: number; name: string }>();
  const [renameError, setRenameError] = useState<string>();
  const [refusal, setRefusal] = useState<Refusal>();
  const request = useRef(0);

  const reload = useCallback(async () => {
    const mine = ++request.current;
    try {
      const sets = await loadVariableSets(environmentId);
      if (request.current === mine) setData({ environmentId, sets });
    } catch (reason: unknown) {
      if (request.current === mine) {
        const error = reason instanceof Error ? reason.message : 'Could not load the Variable Sets';
        setData((previous) => ({ environmentId, error, sets: previous?.environmentId === environmentId ? previous.sets : undefined }));
      }
    }
  }, [environmentId, loadVariableSets]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const current = data?.environmentId === environmentId ? data : undefined;
  const sets = current?.sets;

  const openCreate = () => {
    setCreateError(undefined);
    form.resetFields();
    setCreateOpen(true);
  };

  const create = async ({ name }: { name: string }) => {
    setCreateError(undefined);
    try {
      await createVariableSet(environmentId, name);
      setCreateOpen(false);
      message.success(`Created ${name}`);
      await reload();
    } catch (reason: unknown) {
      setCreateError(reason instanceof Error ? reason.message : 'Could not create the set');
    }
  };

  const rename = async () => {
    if (!renaming) return;
    setRenameError(undefined);
    try {
      await renameVariableSet(environmentId, renaming.id, renaming.name);
      setRenaming(undefined);
      message.success(`Renamed to ${renaming.name}`);
      await reload();
    } catch (reason: unknown) {
      setRenameError(reason instanceof Error ? reason.message : 'Could not rename the set');
    }
  };

  const remove = async (set: VariableSet) => {
    setRefusal(undefined);
    try {
      await deleteVariableSet(environmentId, set.id);
      message.success(`Deleted ${set.name}`);
    } catch (reason: unknown) {
      if (reason instanceof ApiError && reason.status === 409) {
        setRefusal({ message: reason.message, setName: set.name, usedBy: (reason.usedBy ?? []).map(usageLabel) });
      } else {
        onError?.(reason);
      }
    }
    await reload();
  };

  const addButton = (
    <Button icon={<PlusOutlined />} onClick={openCreate} type="primary">
      New set
    </Button>
  );

  return (
    <Card
      extra={sets && sets.length > 0 ? addButton : undefined}
      style={{ marginTop: 16 }}
      title={`Variable Sets · ${environment.name}`}
      variant="outlined"
    >
      <Paragraph type="secondary">
        Named groups of variables for particular Services. Only for Services you bind · not usable as{' '}
        <Text code>{'${VAR}'}</Text>
      </Paragraph>

      {refusal && (
        <Alert
          closable
          onClose={() => setRefusal(undefined)}
          showIcon
          style={{ marginBottom: 16 }}
          title={`Cannot delete ${refusal.setName}`}
          description={
            <>
              {refusal.message}
              {refusal.usedBy.length > 0 && (
                <Flex gap={4} style={{ marginTop: 8 }} wrap>
                  {refusal.usedBy.map((label) => (
                    <Tag key={label} style={{ fontFamily: CODE_FONT_STACK }}>
                      {label}
                    </Tag>
                  ))}
                </Flex>
              )}
            </>
          }
          type="error"
        />
      )}

      {!current || (!sets && !current.error) ? (
        <Skeleton active paragraph={{ rows: 2 }} title={false} />
      ) : !sets ? (
        <Alert action={<Button onClick={() => void reload()}>Retry</Button>} showIcon title={current.error} type="error" />
      ) : sets.length === 0 ? (
        <EmptyState
          action={addButton}
          description="Group variables for particular Services, such as database credentials only the db Service should see."
          title="No Variable Sets yet"
        />
      ) : (
        <Collapse
          activeKey={expanded}
          items={sets.map((set) => ({
            children: (
              <VariablesEditor
                deleteNote="Services bound to this set lose the key on their next deploy."
                description="Entries are written to an env file for the Services that bind this set. Changes take effect on the next deploy."
                emptyDescription="Add the keys the bound Services need."
                emptyTitle="No entries yet"
                isDeleting={(entry) => isPending(`delete-set-entry:${entry.id}`)}
                loading={false}
                noun={`${set.name} entries`}
                onDelete={async (entry) => {
                  await deleteSetEntry(environmentId, set.id, entry.id);
                  await reload();
                }}
                onError={onError}
                onSave={async (values, id) => {
                  await saveSetEntry(environmentId, set.id, values, id);
                  await reload();
                }}
                saving={isPending(`save-set-entry:${set.id}`)}
                singleAddWhenEmpty
                title={`Entries · ${set.name}`}
                variables={set.entries}
              />
            ),
            extra: (
              <Space onClick={(event) => event.stopPropagation()} size={4}>
                <Button
                  aria-label={`Rename ${set.name}`}
                  icon={<EditOutlined />}
                  onClick={() => {
                    setRenameError(undefined);
                    setRenaming({ id: set.id, name: set.name });
                  }}
                  size="small"
                  type="text"
                />
                <Popconfirm
                  cancelText="Keep"
                  description="Its entries are deleted with it. A set that Services still bind cannot be deleted."
                  okButtonProps={{ danger: true }}
                  okText="Delete"
                  onConfirm={() => void remove(set)}
                  title={`Delete ${set.name}?`}
                >
                  <Button aria-label={`Delete ${set.name}`} danger icon={<DeleteOutlined />} loading={isPending(`delete-variable-set:${set.id}`)} size="small" type="text" />
                </Popconfirm>
              </Space>
            ),
            key: String(set.id),
            label:
              renaming?.id === set.id ? (
                <div onClick={(event) => event.stopPropagation()}>
                  <Space.Compact>
                    <Input
                      aria-label={`New name for ${set.name}`}
                      autoFocus
                      onChange={(event) => setRenaming({ id: set.id, name: event.target.value })}
                      onKeyDown={(event) => {
                        // Space and Enter would otherwise toggle the panel header.
                        event.stopPropagation();
                        if (event.key === 'Escape') setRenaming(undefined);
                      }}
                      onPressEnter={() => void rename()}
                      status={renameError ? 'error' : undefined}
                      style={{ fontFamily: CODE_FONT_STACK, width: 200 }}
                      value={renaming.name}
                    />
                    <Button aria-label="Save name" icon={<CheckOutlined />} loading={isPending(`rename-variable-set:${set.id}`)} onClick={() => void rename()} />
                    <Button aria-label="Cancel rename" icon={<CloseOutlined />} onClick={() => setRenaming(undefined)} />
                  </Space.Compact>
                  {renameError && (
                    <Text style={{ display: 'block', fontSize: 12 }} type="danger">
                      {renameError}
                    </Text>
                  )}
                </div>
              ) : (
                <Flex align="center" gap={8} wrap>
                  <Text strong style={{ fontFamily: CODE_FONT_STACK }}>
                    {set.name}
                  </Text>
                  <Text type="secondary">{entryCountLabel(set.entries.length)}</Text>
                  {set.usedBy.length === 0 ? (
                    <Text type="secondary">· not bound</Text>
                  ) : (
                    set.usedBy.map((usage) => (
                      <Tag key={`${usage.composeId}/${usage.service}`} style={{ fontFamily: CODE_FONT_STACK, marginInlineEnd: 0 }}>
                        {usageLabel(usage)}
                      </Tag>
                    ))
                  )}
                </Flex>
              ),
          }))}
          onChange={(keys) => setExpanded(Array.isArray(keys) ? keys : [keys])}
        />
      )}

      <Modal
        confirmLoading={isPending(`create-variable-set:${environmentId}`)}
        destroyOnHidden
        okText="Create set"
        onCancel={() => setCreateOpen(false)}
        onOk={() => form.submit()}
        open={createOpen}
        title="New Variable Set"
      >
        {createError && <Alert showIcon style={{ marginBottom: 16 }} title={createError} type="error" />}
        <Form form={form} layout="vertical" onFinish={(values) => void create(values)} requiredMark={false}>
          <Form.Item extra="Lowercase letters, digits and dashes, e.g. db-credentials" label="Name" name="name" rules={nameRules}>
            <Input autoFocus placeholder="db-credentials" style={{ fontFamily: CODE_FONT_STACK }} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
