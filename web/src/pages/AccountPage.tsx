import { LockOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Modal, QRCode, Skeleton, Steps, Tag, Typography } from 'antd';
import { useCallback, useEffect, useState } from 'react';

import { PageHeader } from '../components/primitives';
import { api } from '../data/api';
import type { Account, TwoFactorSetup } from '../data/api';
import { enableStepIndex, RESET_COMMAND } from './account-view';
import type { EnableStep } from './account-view';

const { Text } = Typography;

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : 'Something went wrong';
}

/**
 * The Account page (GUIDELINES §4.10): the admin's email, and the one control
 * that changes the account — turning opt-in TOTP two-factor on or off. An
 * account with no second factor is not broken, so the page states the `Off`
 * state plainly and keeps the host recovery command in view; a lost
 * authenticator is the failure the panel cannot fix from here.
 *
 * The turn-on flow is a three-step modal (Password → Scan → Verify), and the
 * pending secret only becomes the account's secret at the last step, where a
 * complete code submits on its own. The turn-off flow is one dangerous modal
 * asking for the password and a current code, because a stolen session cookie
 * must not be able to remove the factor. Both mutations end by deleting the
 * account's other sessions, and both say so.
 */
export function AccountPage() {
  const { message } = App.useApp();
  const [account, setAccount] = useState<Account>();
  const [loadError, setLoadError] = useState<string>();

  const [enableForm] = Form.useForm<{ password: string }>();
  const [disableForm] = Form.useForm<{ password: string; code: string }>();

  const [enableOpen, setEnableOpen] = useState(false);
  const [enableStep, setEnableStep] = useState<EnableStep>('password');
  const [setup, setSetup] = useState<TwoFactorSetup>();
  const [enableCode, setEnableCode] = useState('');
  const [enableError, setEnableError] = useState<string>();
  const [enablePending, setEnablePending] = useState(false);

  const [disableOpen, setDisableOpen] = useState(false);
  const [disableError, setDisableError] = useState<string>();
  const [disablePending, setDisablePending] = useState(false);

  const loadAccount = useCallback(async () => {
    setLoadError(undefined);
    try {
      setAccount(await api.account());
    } catch (reason: unknown) {
      setLoadError(errorMessage(reason));
    }
  }, []);

  useEffect(() => {
    void loadAccount();
  }, [loadAccount]);

  const openEnable = () => {
    enableForm.resetFields();
    setEnableStep('password');
    setSetup(undefined);
    setEnableCode('');
    setEnableError(undefined);
    setEnableOpen(true);
  };

  const closeEnable = () => {
    setEnableOpen(false);
    setEnableError(undefined);
  };

  const submitEnablePassword = async (values: { password: string }) => {
    setEnableError(undefined);
    setEnablePending(true);
    try {
      setSetup(await api.twoFactorSetup(values.password));
      setEnableStep('scan');
    } catch (reason: unknown) {
      setEnableError(errorMessage(reason));
    } finally {
      setEnablePending(false);
    }
  };

  const submitEnableCode = async (code: string) => {
    setEnableError(undefined);
    setEnablePending(true);
    try {
      await api.twoFactorEnable(code);
      setEnableOpen(false);
      message.success('Two-factor turned on. Other sessions were signed out.');
      await loadAccount();
    } catch (reason: unknown) {
      // A refused code is spent: clear the boxes and show the API's words, the
      // lock's 429 included, inside the modal.
      setEnableCode('');
      setEnableError(errorMessage(reason));
    } finally {
      setEnablePending(false);
    }
  };

  const openDisable = () => {
    disableForm.resetFields();
    setDisableError(undefined);
    setDisableOpen(true);
  };

  const submitDisable = async (values: { password: string; code: string }) => {
    setDisableError(undefined);
    setDisablePending(true);
    try {
      await api.twoFactorDisable(values.password, values.code);
      setDisableOpen(false);
      message.success('Two-factor turned off. Other sessions were signed out.');
      await loadAccount();
    } catch (reason: unknown) {
      disableForm.setFieldValue('code', '');
      setDisableError(errorMessage(reason));
    } finally {
      setDisablePending(false);
    }
  };

  return (
    <>
      <PageHeader description="The admin account for this host." title="Account" />

      {loadError && (
        <Alert
          action={<Button onClick={() => void loadAccount()}>Retry</Button>}
          showIcon
          style={{ marginBottom: 16 }}
          title={loadError}
          type="error"
        />
      )}

      {account ? (
        <Flex gap={16} vertical>
          <Card title="Sign-in" variant="outlined">
            <Flex gap={4} vertical>
              <Text type="secondary">Email</Text>
              <Text>{account.email}</Text>
            </Flex>
          </Card>

          <Card title="Two-factor authentication" variant="outlined">
            <Flex gap={16} vertical>
              <Flex align="center" gap={12}>
                <Text>Two-factor authentication is</Text>
                <Tag
                  color={account.twoFactorEnabled ? 'success' : undefined}
                  variant={account.twoFactorEnabled ? 'filled' : 'outlined'}
                >
                  {account.twoFactorEnabled ? 'On' : 'Off'}
                </Tag>
              </Flex>
              <Text type="secondary">
                {account.twoFactorEnabled
                  ? 'Sign-in asks for a code from your authenticator app after the password. Turning it on signed every other session out.'
                  : 'When it is on, sign-in asks for a code from your authenticator app after the password.'}
              </Text>
              {account.twoFactorEnabled ? (
                <div>
                  <Button danger onClick={openDisable}>
                    Turn off two-factor
                  </Button>
                </div>
              ) : (
                <div>
                  <Button onClick={openEnable} type="primary">
                    Turn on two-factor
                  </Button>
                </div>
              )}
              <div>
                <Text style={{ display: 'block' }} type="secondary">
                  Lost your authenticator? Run this on the host to turn two-factor off and clear the sign-in lock:
                </Text>
                <Text code copyable>
                  {RESET_COMMAND}
                </Text>
              </div>
            </Flex>
          </Card>
        </Flex>
      ) : (
        !loadError && (
          <Card variant="outlined">
            <Skeleton active />
          </Card>
        )
      )}

      <Modal
        destroyOnHidden
        footer={null}
        onCancel={closeEnable}
        open={enableOpen}
        title="Turn on two-factor authentication"
        width={480}
      >
        <Steps
          current={enableStepIndex(enableStep)}
          items={[{ title: 'Password' }, { title: 'Scan' }, { title: 'Verify' }]}
          size="small"
          style={{ margin: '8px 0 24px' }}
        />
        {enableError && <Alert showIcon style={{ marginBottom: 16 }} title={enableError} type="error" />}
        {enableStep === 'password' && (
          <Form
            form={enableForm}
            layout="vertical"
            onFinish={(values) => void submitEnablePassword(values)}
            requiredMark={false}
          >
            <Form.Item label="Password" name="password" rules={[{ message: 'Enter your password', required: true }]}>
              <Input.Password autoComplete="current-password" autoFocus prefix={<LockOutlined />} />
            </Form.Item>
            <Flex gap={8} justify="flex-end">
              <Button onClick={closeEnable}>Cancel</Button>
              <Button htmlType="submit" loading={enablePending} type="primary">
                Continue
              </Button>
            </Flex>
          </Form>
        )}
        {enableStep === 'scan' && setup && (
          <Flex align="center" gap={16} vertical>
            <QRCode value={setup.otpauthUri} />
            <Text style={{ textAlign: 'center' }} type="secondary">
              Scan this with your authenticator app. If you cannot scan it, enter the secret by hand.
            </Text>
            <Text code copyable>
              {setup.secret}
            </Text>
            <Flex gap={8} justify="flex-end" style={{ width: '100%' }}>
              <Button onClick={closeEnable}>Cancel</Button>
              <Button
                onClick={() => {
                  setEnableCode('');
                  setEnableStep('verify');
                }}
                type="primary"
              >
                Continue
              </Button>
            </Flex>
          </Flex>
        )}
        {enableStep === 'verify' && (
          <Flex align="center" gap={16} vertical>
            <Text type="secondary">Enter the six-digit code from your authenticator app.</Text>
            <Input.OTP
              autoComplete="one-time-code"
              autoFocus
              disabled={enablePending}
              length={6}
              onChange={(value) => {
                setEnableCode(value);
                if (value.length === 6) void submitEnableCode(value);
              }}
              value={enableCode}
            />
            <Flex gap={8} justify="flex-end" style={{ width: '100%' }}>
              <Button onClick={closeEnable}>Cancel</Button>
            </Flex>
          </Flex>
        )}
      </Modal>

      <Modal
        confirmLoading={disablePending}
        destroyOnHidden
        okButtonProps={{ danger: true }}
        okText="Turn off"
        onCancel={() => setDisableOpen(false)}
        onOk={() => disableForm.submit()}
        open={disableOpen}
        title="Turn off two-factor authentication"
      >
        {disableError && <Alert showIcon style={{ marginBottom: 16 }} title={disableError} type="error" />}
        <Text type="secondary">Confirm with your password and a current code from your authenticator app.</Text>
        <Form
          form={disableForm}
          layout="vertical"
          onFinish={(values) => void submitDisable(values)}
          requiredMark={false}
          style={{ marginTop: 16 }}
        >
          <Form.Item label="Password" name="password" rules={[{ message: 'Enter your password', required: true }]}>
            <Input.Password autoComplete="current-password" autoFocus prefix={<LockOutlined />} />
          </Form.Item>
          <Form.Item label="Code" name="code" rules={[{ message: 'Enter the six-digit code', required: true }]}>
            <Input.OTP autoComplete="one-time-code" length={6} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
