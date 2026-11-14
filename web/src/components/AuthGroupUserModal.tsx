import { Alert, Button, Form, Input, Modal } from 'antd';
import type { FormInstance } from 'antd';
import { useEffect, useState } from 'react';

import type { ApiError } from '../data/api';
import type { AuthGroupUser } from '../data/types';
import { generatePassword } from '../pages/basic-auth-view';

export type UserFormValues = { username: string; password: string };

/**
 * A Password field with a Generate button that fills it with a random
 * password and shows it, so the Admin can copy it before saving. The form
 * must have a `password` field value to fill.
 */
export function PasswordFormItem({
  form,
  help,
  required,
}: {
  form: FormInstance;
  /** Shown under the field when it is optional. */
  help?: string;
  required: boolean;
}) {
  const [visible, setVisible] = useState(false);
  return (
    <Form.Item
      extra={
        <>
          {help && <div>{help}</div>}
          <Button
            onClick={() => {
              form.setFieldValue('password', generatePassword());
              void form.validateFields(['password']).catch(() => undefined);
              setVisible(true);
            }}
            size="small"
            style={{ paddingInline: 0 }}
            type="link"
          >
            Generate
          </Button>
        </>
      }
      label="Password"
      name="password"
      rules={[
        { message: 'Enter a password', required },
        {
          validator: (_, value: string | undefined) => {
            if (!value) return Promise.resolve();
            if (value.length < 8) return Promise.reject(new Error('At least 8 characters'));
            if (new TextEncoder().encode(value).length > 72) return Promise.reject(new Error('At most 72 bytes'));
            return Promise.resolve();
          },
        },
      ]}
    >
      <Input.Password autoComplete="new-password" visibilityToggle={{ onVisibleChange: setVisible, visible }} />
    </Form.Item>
  );
}

const FIELDS: readonly (keyof UserFormValues)[] = ['username', 'password'];

/**
 * Add or edit a user of a Basic auth group. When editing, the password is
 * optional and empty means unchanged; the submitted input then carries no
 * `password` key.
 */
export function AuthGroupUserModal({
  error,
  onCancel,
  onSubmit,
  open,
  saving,
  user,
}: {
  open: boolean;
  user?: AuthGroupUser;
  saving: boolean;
  error?: ApiError;
  onCancel: () => void;
  onSubmit: (input: { username: string; password?: string }) => void;
}) {
  const [form] = Form.useForm<UserFormValues>();
  const editing = user !== undefined;

  useEffect(() => {
    if (open) {
      form.resetFields();
      form.setFieldsValue({ password: '', username: user?.username ?? '' });
    }
  }, [open, user, form]);

  const fieldError = FIELDS.find((name) => name === error?.field);
  useEffect(() => {
    if (error && fieldError) {
      form.setFields([{ errors: [error.message], name: fieldError }]);
    }
  }, [error, fieldError, form]);

  return (
    <Modal
      cancelText="Cancel"
      confirmLoading={saving}
      destroyOnHidden
      okText={editing ? 'Save' : 'Add'}
      onCancel={onCancel}
      onOk={() => form.submit()}
      open={open}
      title={editing ? `Edit ${user.username}` : 'Add user'}
    >
      {error && !fieldError && <Alert showIcon style={{ marginBottom: 16 }} title={error.message} type="error" />}
      <Form<UserFormValues>
        autoComplete="off"
        form={form}
        layout="vertical"
        onFinish={(values) => {
          const username = values.username.trim();
          onSubmit(values.password ? { password: values.password, username } : { username });
        }}
        preserve={false}
      >
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
        <PasswordFormItem form={form} help={editing ? 'Leave empty to keep the current password.' : undefined} required={!editing} />
      </Form>
    </Modal>
  );
}
