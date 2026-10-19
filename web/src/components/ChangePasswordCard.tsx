import { LockOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, Typography } from 'antd';
import { useState } from 'react';

import { api } from '../data/api';

const { Text } = Typography;

type Values = { currentPassword: string; newPassword: string; confirm: string; code?: string };

/**
 * The Account page's password card. Changing it asks for the current
 * password, and a code when two-factor is on, because a stolen session cookie
 * must not be able to take the account; every other session then ends.
 */
export function ChangePasswordCard({
  onChanged,
  passwordFromEnv,
  twoFactorEnabled,
}: {
  onChanged: () => void;
  passwordFromEnv: boolean;
  twoFactorEnabled: boolean;
}) {
  const { message } = App.useApp();
  const [form] = Form.useForm<Values>();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string>();

  const close = () => {
    setOpen(false);
    setError(undefined);
  };

  const submit = async (values: Values) => {
    setPending(true);
    setError(undefined);
    try {
      await api.changePassword(values.currentPassword, values.newPassword, twoFactorEnabled ? values.code?.trim() : undefined);
      close();
      message.success('Password changed. Every other session was signed out.');
      onChanged();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Could not change the password');
    } finally {
      setPending(false);
    }
  };

  return (
    <Card title="Password" variant="outlined">
      <Flex gap={16} vertical>
        {passwordFromEnv && (
          <Alert
            description="It is readable in the install's .env and in docker inspect. Change it, then remove RELIST_ADMIN_EMAIL and RELIST_ADMIN_PASSWORD from the .env."
            showIcon
            title="The password is still the one in RELIST_ADMIN_PASSWORD"
            type="warning"
          />
        )}
        <Text type="secondary">Changing the password signs every other session out.</Text>
        <div>
          <Button onClick={() => setOpen(true)} type={passwordFromEnv ? 'primary' : 'default'}>
            Change password
          </Button>
        </div>
      </Flex>
      <Modal destroyOnHidden footer={null} onCancel={close} open={open} title="Change password" width={480}>
        {error && <Alert showIcon style={{ marginBottom: 16 }} title={error} type="error" />}
        <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} preserve={false} requiredMark={false}>
          <Form.Item label="Current password" name="currentPassword" rules={[{ message: 'Enter your current password', required: true }]}>
            <Input.Password autoComplete="current-password" autoFocus prefix={<LockOutlined />} />
          </Form.Item>
          <Form.Item
            label="New password"
            name="newPassword"
            rules={[
              { message: 'Enter a new password', required: true },
              { message: 'At least 8 characters', min: 8 },
            ]}
          >
            <Input.Password autoComplete="new-password" prefix={<LockOutlined />} />
          </Form.Item>
          <Form.Item
            dependencies={['newPassword']}
            label="Confirm new password"
            name="confirm"
            rules={[
              { message: 'Enter the new password again', required: true },
              ({ getFieldValue }) => ({
                validator: (_, value) =>
                  !value || value === getFieldValue('newPassword') ? Promise.resolve() : Promise.reject(new Error('The two passwords differ')),
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" prefix={<LockOutlined />} />
          </Form.Item>
          {twoFactorEnabled && (
            <Form.Item label="Authenticator code" name="code" rules={[{ message: 'Enter the code from your authenticator app', required: true }]}>
              <Input autoComplete="one-time-code" inputMode="numeric" />
            </Form.Item>
          )}
          <Flex gap={8} justify="flex-end">
            <Button onClick={close}>Cancel</Button>
            <Button htmlType="submit" loading={pending} type="primary">
              Change password
            </Button>
          </Flex>
        </Form>
      </Modal>
    </Card>
  );
}
