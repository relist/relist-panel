import { CloudServerOutlined, LockOutlined, SafetyCertificateOutlined, ThunderboltOutlined } from '@ant-design/icons';
import { Alert, App, Button, Card, Flex, Form, Input, Layout, Result, Spin, Steps, Tag, Typography, theme } from 'antd';
import { useEffect, useState } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router';

import { entryScreen, signInTarget } from '../app/entry';
import { paths } from '../app/paths';
import { CenteredScreen } from '../app/RequireAuth';
import type { LoginResult } from '../data/api';
import { migrationCommand } from '../data/migration';
import { usePanel } from '../data/store';
import { backToPassword, nextStep, rateLimitMessage } from './signin-view';
import type { SignInStep } from './signin-view';

const { Text, Title, Paragraph } = Typography;

/**
 * Auth screens sit outside the app shell (a signed-out visitor must not see the
 * sider). The split layout gives the panel a place to state what it is and what
 * it is not — the old screen was a bare card on grey, which read as an error
 * page rather than a product.
 */
function AuthLayout({
  children,
  footer,
  subtitle,
  title,
}: {
  children: React.ReactNode;
  footer?: React.ReactNode;
  subtitle: React.ReactNode;
  title: string;
}) {
  const { token } = theme.useToken();
  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Flex style={{ flex: 1, minHeight: '100vh' }}>
        <Flex
          justify="space-between"
          style={{
            background: token.colorPrimaryBg,
            borderInlineEnd: `1px solid ${token.colorBorderSecondary}`,
            flex: '0 0 44%',
            padding: 40,
          }}
          vertical
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
                <Text>
                  Shells out to the real <Text code>docker compose</Text> CLI
                </Text>
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

          <Text style={{ fontSize: 12 }} type="secondary">
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

/** The design's error handling: a failure stays on the card, it is not a toast. */
function AuthError({ message }: { message: string }) {
  return (
    <Alert
      showIcon
      style={{ marginBottom: 16 }}
      title={message}
      type="error"
    />
  );
}

export function SignInPage() {
  const { auth, isPending, signIn } = usePanel();
  const navigate = useNavigate();
  const location = useLocation();
  const [form] = Form.useForm<{ email: string; password: string }>();
  const [step, setStep] = useState<SignInStep>({ step: 'password' });
  const [error, setError] = useState<string>();
  const [codeError, setCodeError] = useState<string>();
  const [code, setCode] = useState('');

  // The deep link that sent the visitor here (set by the gate's redirect).
  const from = (location.state as { from?: unknown } | null)?.from;

  // Already signed in — arriving at /signin directly or right after the
  // submit resolves — means on to the app, at the deep link when there was one.
  useEffect(() => {
    if (auth.authenticated) {
      navigate(signInTarget(from, paths.overview), { replace: true });
    }
  }, [auth.authenticated, from, navigate]);

  if (!auth.checked) {
    return (
      <CenteredScreen>
        <Spin aria-label="Loading" size="large" />
      </CenteredScreen>
    );
  }

  // First run has no account to sign in with: the monolith routed straight to
  // setup, so /signin does the same while the API reports setupRequired.
  if (entryScreen(auth) === 'setup') {
    return <Navigate replace to={paths.setup} />;
  }

  // A result is an answer, not necessarily a failure: the code prompt advances
  // the step, the lock leaves a message, and only a completed sign-in moves the
  // store, which the effect above turns into navigation.
  const applyResult = (submitted: { email: string; password: string }, result: LoginResult) => {
    if (result.kind === 'rateLimited') {
      setError(rateLimitMessage(result.retryAfterSeconds));
    } else if (result.kind === 'codeRequired') {
      setError(undefined);
      setCode('');
    }
    setCodeError(undefined);
    setStep((current) => nextStep(current, submitted, result));
  };

  const submitPassword = async (values: { email: string; password: string }) => {
    setError(undefined);
    setCodeError(undefined);
    try {
      applyResult(values, await signIn(values.email, values.password));
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Sign in failed');
    }
  };

  const submitCode = async (value: string) => {
    if (step.step !== 'code') {
      return;
    }
    setError(undefined);
    setCodeError(undefined);
    try {
      applyResult({ email: step.email, password: step.password }, await signIn(step.email, step.password, value));
    } catch (reason: unknown) {
      // A refused code is spent: clear the boxes and show the API's words under them.
      setCode('');
      setCodeError(reason instanceof Error ? reason.message : 'Sign in failed');
    }
  };

  const back = () => {
    setCode('');
    setError(undefined);
    setCodeError(undefined);
    setStep(backToPassword());
  };

  const signingIn = isPending('signin');
  const onCodeStep = step.step === 'code';

  return (
    <AuthLayout
      footer={
        !onCodeStep && auth.setupRequired ? (
          <Flex gap={8} wrap>
            <Text type="secondary">First run?</Text>
            <Button onClick={() => navigate(paths.setup)} size="small" type="link">
              Create the admin account
            </Button>
          </Flex>
        ) : undefined
      }
      subtitle={onCodeStep ? 'Enter the six-digit code from your authenticator app.' : 'Use the admin account for this host.'}
      title={onCodeStep ? 'Two-factor code' : 'Sign in'}
    >
      {error && <AuthError message={error} />}
      {onCodeStep ? (
        <Flex gap={16} vertical>
          <Input.OTP
            autoComplete="one-time-code"
            autoFocus
            disabled={signingIn}
            length={6}
            onChange={(value) => {
              void submitCode(value);
            }}
            size="large"
            value={code}
          />
          {codeError && <AuthError message={codeError} />}
          <Button block onClick={back} size="small" type="link">
            Back
          </Button>
        </Flex>
      ) : (
        <>
          <Form
            form={form}
            layout="vertical"
            onFinish={(values) => {
              void submitPassword(values);
            }}
            preserve={false}
            requiredMark={false}
          >
            <Form.Item label="Email" name="email" rules={[{ message: 'Enter your email address', required: true }]}>
              <Input autoComplete="email" autoFocus placeholder="admin@relist.local" size="large" />
            </Form.Item>
            <Form.Item label="Password" name="password" rules={[{ message: 'Enter your password', required: true }]}>
              <Input.Password autoComplete="current-password" placeholder="••••••••" prefix={<LockOutlined />} size="large" />
            </Form.Item>
            <Button block htmlType="submit" loading={signingIn} size="large" type="primary">
              Sign in
            </Button>
          </Form>
          <Alert
            description="Sessions are cookie-based and survive a reload; signing out clears the session server-side."
            showIcon
            style={{ marginTop: 16 }}
            title="Staying signed in"
            type="info"
          />
        </>
      )}
    </AuthLayout>
  );
}

export function SetupPage() {
  const { auth, isPending, signUp } = usePanel();
  const { message } = App.useApp();
  const navigate = useNavigate();
  const [step, setStep] = useState(0);
  const [error, setError] = useState<string>();
  const [form] = Form.useForm<{ confirmPassword: string; email: string; password: string }>();

  // Setup exists only until the admin account does. (The check is skipped once
  // the account has been created — the Result below must survive the store
  // flipping `setupRequired` off.)
  if (step === 0 && auth.checked && entryScreen(auth) !== 'setup') {
    return <Navigate replace to={entryScreen(auth) === 'app' ? paths.overview : paths.signIn} />;
  }

  if (!auth.checked) {
    return (
      <CenteredScreen>
        <Spin aria-label="Loading" size="large" />
      </CenteredScreen>
    );
  }

  const submit = async (values: { email: string; password: string }) => {
    setError(undefined);
    try {
      await signUp(values.email, values.password);
      message.success('Admin account created');
      setStep(1);
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Setup failed');
    }
  };

  return (
    <AuthLayout subtitle="This is the only account the panel will ever create." title="Set up your admin account">
      <Steps current={step} items={[{ title: 'Account' }, { title: 'Ready' }]} size="small" style={{ marginBottom: 24 }} />
      {step === 0 ? (
        <>
          {error && <AuthError message={error} />}
          <Form
            form={form}
            layout="vertical"
            onFinish={(values) => {
              void submit(values);
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
            <Button block htmlType="submit" loading={isPending('signup')} size="large" type="primary">
              Create admin account
            </Button>
          </Form>
          <Paragraph style={{ marginBottom: 0, marginTop: 16 }} type="secondary">
            Moving a panel here? Don&apos;t create an admin. Run this on this server instead:{' '}
            <Text code copyable>
              {migrationCommand('pull')}
            </Text>
          </Paragraph>
        </>
      ) : (
        <Result
          extra={
            <Button onClick={() => navigate(paths.signIn, { replace: true })} type="primary">
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
    </AuthLayout>
  );
}
