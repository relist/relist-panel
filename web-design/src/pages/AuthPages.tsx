import { CloudServerOutlined, LockOutlined, SafetyCertificateOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Layout, Result, Steps, Tag, Typography, theme } from 'antd';
import { useEffect } from 'react';
import { navigate, paths } from '../app/router';
import { publishSceneHandle, SceneAdapterBridge } from '../app/sceneAdapters';
import type { SceneForm } from '../app/sceneAdapters';
import { useUiState } from '../app/uiState';

const { Text, Title, Paragraph } = Typography;

/**
 * Auth screens sit outside the app shell. The split layout gives the panel a
 * place to state what it is and what it is not — the old screen was a bare card
 * on grey, which read as an error page rather than a product.
 */
function AuthLayout({ children, footer, title, subtitle }: { children: React.ReactNode; footer?: React.ReactNode; subtitle: React.ReactNode; title: string }) {
  const { token } = theme.useToken();
  // `--frame-vh` falls back to `1vh` outside a scene scope (ticket 10), so
  // standalone this is the viewport height it always was; in a frame it is the
  // frame's own height.
  const frameHeight = 'calc(100 * var(--frame-vh, 1vh))';
  return (
    <Layout style={{ minHeight: frameHeight }}>
      <Flex style={{ flex: 1, minHeight: frameHeight }}>
        <Flex
          vertical
          justify="space-between"
          style={{ background: token.colorPrimaryBg, borderInlineEnd: `1px solid ${token.colorBorderSecondary}`, flex: '0 0 44%', padding: 40 }}
        >
          <Flex align="center" gap={10}>
            <div
              style={{
                alignItems: 'center',
                background: token.colorPrimary,
                borderRadius: 6,
                color: token.colorTextLightSolid,
                display: 'flex',
                fontWeight: 600,
                height: 32,
                justifyContent: 'center',
                width: 32,
              }}
            >
              R
            </div>
            <Text strong style={{ fontSize: 16 }}>
              Relist Panel
            </Text>
          </Flex>

          <div style={{ maxWidth: 420 }}>
            <Title level={2} style={{ marginBottom: 12 }}>
              Docker Compose, on one host, without the ceremony.
            </Title>
            <Paragraph type="secondary">
              Projects group the Environments of one application. Each Environment carries the variables its Composes share.
              Each Compose is one <Text code>docker-compose.yml</Text> you can deploy, stop, restart and read logs from.
            </Paragraph>
            <Flex gap={8} vertical>
              <Flex align="center" gap={8}>
                <CloudServerOutlined style={{ color: token.colorPrimary }} />
                <Text>Shells out to the real <Text code>docker compose</Text> CLI</Text>
              </Flex>
              <Flex align="center" gap={8}>
                <SafetyCertificateOutlined style={{ color: token.colorPrimary }} />
                <Text>Single admin account — no teams, no roles</Text>
              </Flex>
              <Flex align="center" gap={8}>
                <ThunderboltOutlined style={{ color: token.colorPrimary }} />
                <Text>Traefik routing written by you, as Compose labels</Text>
              </Flex>
            </Flex>
          </div>

          <Text type="secondary" style={{ fontSize: 12 }}>
            Relist Panel v1 · self-hosted
          </Text>
        </Flex>

        <Flex align="center" justify="center" style={{ flex: 1, padding: 24 }}>
          <Card style={{ width: '100%', maxWidth: 440 }} variant="outlined">
            <Title level={3} style={{ marginBottom: 4 }}>
              {title}
            </Title>
            <Text type="secondary">{subtitle}</Text>
            <div style={{ marginTop: 24 }}>{children}</div>
            {footer && <div style={{ marginTop: 16 }}>{footer}</div>}
          </Card>
        </Flex>
      </Flex>
    </Layout>
  );
}

export function SignInPage({ onSignIn }: { onSignIn: () => void }) {
  const [form] = Form.useForm<{ email: string; password: string }>();
  const [submitting, setSubmitting] = useUiState('auth.submitting', false);

  // Ticket 02: the post-submit states are `after:` adapters (audit §3b). The
  // form is published for them; the adapters run its own validation and
  // handlers through it — valid values → `submitting`, empty → the field
  // errors. `<SceneAdapterBridge />` lets the adapters reach this app's own
  // antd handles.
  useEffect(
    () =>
      publishSceneHandle<SceneForm>('auth.signin.form', {
        setFieldsValue: (values) => form.setFieldsValue(values as { email: string; password: string }),
        submit: () => form.submit(),
      }),
    [form],
  );

  return (
    <AuthLayout
      footer={
        <Flex gap={8} wrap>
          <Text type="secondary">First run?</Text>
          <Button onClick={() => navigate(paths.setup)} size="small" type="link">
            Create the admin account
          </Button>
        </Flex>
      }
      subtitle="Use the admin account for this host."
      title="Sign in"
    >
      <Form
        form={form}
        layout="vertical"
        onFinish={() => {
          setSubmitting(true);
          setTimeout(() => {
            setSubmitting(false);
            onSignIn();
          }, 500);
        }}
        requiredMark={false}
      >
        <Form.Item label="Email" name="email" rules={[{ message: 'Enter your email address', required: true }]}>
          <Input autoComplete="email" autoFocus placeholder="admin@relist.local" size="large" />
        </Form.Item>
        <Form.Item label="Password" name="password" rules={[{ message: 'Enter your password', required: true }]}>
          <Input.Password autoComplete="current-password" placeholder="••••••••" prefix={<LockOutlined />} size="large" />
        </Form.Item>
        <Button block htmlType="submit" loading={submitting} size="large" type="primary">
          Sign in
        </Button>
      </Form>
      <Alert
        description="Sessions are cookie-based and survive a reload; signing out clears the session server-side."
        message="Staying signed in"
        showIcon
        style={{ marginTop: 16 }}
        type="info"
      />
      <SceneAdapterBridge />
    </AuthLayout>
  );
}

export function SetupPage({ onComplete }: { onComplete: () => void }) {
  const { message } = App.useApp();
  const [step, setStep] = useUiState<number>('auth.setupStep', 0);
  const [form] = Form.useForm<{ confirmPassword: string; email: string; password: string }>();

  // Ticket 02: `after:` adapters (audit §3b) — the mismatched-confirm submit
  // the form's own validator refuses, and the sticky account-created toast.
  // `<SceneAdapterBridge />` lets them reach this app's own antd handles.
  useEffect(
    () =>
      publishSceneHandle<SceneForm>('auth.setup.form', {
        setFieldsValue: (values) => form.setFieldsValue(values as { confirmPassword: string; email: string; password: string }),
        submit: () => form.submit(),
      }),
    [form],
  );

  return (
    <AuthLayout
      subtitle="This is the only account the panel will ever create."
      title="Set up your admin account"
    >
      <Steps
        current={step}
        items={[{ title: 'Account' }, { title: 'Ready' }]}
        size="small"
        style={{ marginBottom: 24 }}
      />
      {step === 0 ? (
        <Form
          form={form}
          layout="vertical"
          onFinish={() => {
            setStep(1);
            message.success('Admin account created');
          }}
          requiredMark={false}
        >
          <Form.Item
            label="Email"
            name="email"
            rules={[
              { message: 'Enter your email address', required: true },
              { message: 'Enter a valid email address', type: 'email' },
            ]}
          >
            <Input autoComplete="email" autoFocus placeholder="admin@relist.local" size="large" />
          </Form.Item>
          <Form.Item
            label="Password"
            name="password"
            rules={[
              { message: 'Enter a password', required: true },
              { message: 'Use at least 8 characters', min: 8 },
            ]}
          >
            <Input.Password autoComplete="new-password" size="large" />
          </Form.Item>
          <Form.Item
            dependencies={['password']}
            label="Confirm password"
            name="confirmPassword"
            rules={[
              { message: 'Confirm your password', required: true },
              ({ getFieldValue }) => ({
                validator(_rule, value) {
                  if (!value || getFieldValue('password') === value) return Promise.resolve();
                  return Promise.reject(new Error('Passwords do not match'));
                },
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" size="large" />
          </Form.Item>
          <Button block htmlType="submit" size="large" type="primary">
            Create admin account
          </Button>
        </Form>
      ) : (
        <Result
          extra={
            <Button onClick={onComplete} type="primary">
              Open the panel
            </Button>
          }
          status="success"
          subTitle={
            <Flex gap={4} vertical>
              <Text type="secondary">Sign in with the account you just created. Next: create a Project and add its first Environment.</Text>
              <Flex gap={8} style={{ marginTop: 8 }}>
                <Tag>1 · Project</Tag>
                <Tag>2 · Environment</Tag>
                <Tag>3 · Compose</Tag>
              </Flex>
            </Flex>
          }
          title="You're set up"
        />
      )}
      <SceneAdapterBridge />
    </AuthLayout>
  );
}

export function NotFoundPage() {
  return (
    <Result
      extra={
        <Flex gap={8}>
          <Button onClick={() => navigate(paths.overview)} type="primary">
            Back to Overview
          </Button>
          <Button onClick={() => navigate(paths.projects)}>All Projects</Button>
        </Flex>
      }
      status="404"
      subTitle="That path does not exist in the panel. It may be a stale bookmark, or a Project that has since been deleted."
      title="Page not found"
      style={{ paddingTop: 64 }}
    />
  );
}
