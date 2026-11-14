import { Alert, Form, Input, Modal } from 'antd';
import { useEffect, useState } from 'react';

import { ApiError, api } from '../data/api';
import type { AuthGroupOption } from '../data/types';
import { PasswordFormItem } from './AuthGroupUserModal';

type Values = { name: string; username: string; password: string };

const FIELDS: readonly (keyof Values)[] = ['name', 'username', 'password'];

/**
 * Makes a Basic auth group and its first user in one call, so an Admin can
 * protect a Domain without leaving its form (ADR-0036).
 */
export function NewAuthGroupModal({
  onCancel,
  onCreated,
  open,
}: {
  open: boolean;
  onCancel: () => void;
  onCreated: (group: AuthGroupOption) => void;
}) {
  const [form] = Form.useForm<Values>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<ApiError>();

  useEffect(() => {
    if (open) {
      form.resetFields();
      setError(undefined);
    }
  }, [open, form]);

  const fieldError = FIELDS.find((name) => name === error?.field);
  useEffect(() => {
    if (error && fieldError) {
      form.setFields([{ errors: [error.message], name: fieldError }]);
    }
  }, [error, fieldError, form]);

  const submit = async (values: Values) => {
    setSaving(true);
    setError(undefined);
    try {
      const group = await api.createAuthGroup({
        name: values.name.trim(),
        users: [{ password: values.password, username: values.username.trim() }],
      });
      onCreated({ id: group.id, name: group.name });
    } catch (reason: unknown) {
      setError(reason instanceof ApiError ? reason : new ApiError(0, reason instanceof Error ? reason.message : 'Something went wrong'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      cancelText="Cancel"
      confirmLoading={saving}
      destroyOnHidden
      okText="Create"
      onCancel={onCancel}
      onOk={() => form.submit()}
      open={open}
      title="New Basic auth group"
    >
      {error && !fieldError && <Alert showIcon style={{ marginBottom: 16 }} title={error.message} type="error" />}
      <Form<Values> autoComplete="off" form={form} layout="vertical" onFinish={(values) => void submit(values)} preserve={false}>
        <Form.Item
          label="Group name"
          name="name"
          rules={[
            { message: 'Enter a name', required: true, whitespace: true },
            { max: 64, message: 'At most 64 characters' },
          ]}
        >
          <Input placeholder="Staging" />
        </Form.Item>
        <Form.Item
          label="Username"
          name="username"
          rules={[
            { message: 'Enter a username', required: true, whitespace: true },
            { max: 64, message: 'At most 64 characters' },
            { message: "No spaces or ':'", pattern: /^[\x21-\x39\x3b-\x7e]*$/ },
          ]}
        >
          <Input autoComplete="off" />
        </Form.Item>
        <PasswordFormItem form={form} required />
      </Form>
    </Modal>
  );
}
