import { StyleProvider } from '@ant-design/cssinjs';
import { Alert, Button, Card, ConfigProvider, Form, Input, Layout, Spin, Typography, theme } from 'antd';
import { StrictMode, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';

import './index.css';

type AuthMode = 'setup' | 'login';
type Screen = AuthMode | 'authenticated' | 'loading' | 'error';

type Credentials = {
  email: string;
  password: string;
  confirmPassword?: string;
};

type AuthStatus = {
  setupRequired: boolean;
  authenticated: boolean;
};

type ErrorResponse = {
  error?: string;
};

async function getAuthStatus(): Promise<AuthStatus> {
  const response = await fetch('/api/auth/status', { credentials: 'same-origin' });
  if (!response.ok) {
    throw new Error(await responseError(response));
  }
  return response.json() as Promise<AuthStatus>;
}

async function responseError(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as ErrorResponse;
    if (payload.error) {
      return payload.error;
    }
  } catch {
    // Fall through to a status-based message when the response is not JSON.
  }
  return `Request failed (${response.status})`;
}

function App() {
  const [screen, setScreen] = useState<Screen>('loading');
  const [notice, setNotice] = useState<string>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    void getAuthStatus()
      .then((status) => {
        if (!active) return;
        setScreen(status.authenticated ? 'authenticated' : status.setupRequired ? 'setup' : 'login');
      })
      .catch((reason: unknown) => {
        if (!active) return;
        setError(reason instanceof Error ? reason.message : 'Could not contact the panel');
        setScreen('error');
      });

    return () => {
      active = false;
    };
  }, []);

  const submitCredentials = async (mode: AuthMode, values: Credentials) => {
    setError(undefined);
    const response = await fetch(`/api/auth/${mode}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ email: values.email, password: values.password }),
    });
    if (!response.ok) {
      throw new Error(await responseError(response));
    }

    if (mode === 'setup') {
      setNotice('Admin account created. Sign in to continue.');
      setScreen('login');
      return;
    }
    setNotice(undefined);
    setScreen('authenticated');
  };

  const logout = async () => {
    setError(undefined);
    const response = await fetch('/api/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
    });
    if (!response.ok) {
      throw new Error(await responseError(response));
    }
    setScreen('login');
    setNotice(undefined);
  };

  if (screen === 'loading') {
    return <LoadingScreen />;
  }

  if (screen === 'error') {
    return <ErrorScreen message={error ?? 'Could not contact the panel'} />;
  }

  if (screen === 'authenticated') {
    return <AuthenticatedScreen onLogout={() => void logout().catch((reason: unknown) => setError(errorMessage(reason)))} error={error} />;
  }

  return (
    <AuthScreen
      mode={screen}
      notice={notice}
      error={error}
      onSubmit={(values) => submitCredentials(screen, values).catch((reason: unknown) => setError(errorMessage(reason)))}
    />
  );
}

function errorMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : 'Something went wrong';
}

function LoadingScreen() {
  return (
    <Layout className="min-h-screen items-center justify-center bg-[#f5f5f5]">
      <Spin size="large" aria-label="Loading" />
    </Layout>
  );
}

function ErrorScreen({ message }: { message: string }) {
  return (
    <Layout className="min-h-screen items-center justify-center bg-[#f5f5f5] px-4">
      <Card className="w-full max-w-md">
        <Typography.Title level={3}>Relist Panel</Typography.Title>
        <Alert type="error" showIcon message={message} />
      </Card>
    </Layout>
  );
}

function AuthScreen({
  mode,
  notice,
  error,
  onSubmit,
}: {
  mode: AuthMode;
  notice?: string;
  error?: string;
  onSubmit: (values: Credentials) => Promise<void>;
}) {
  const [form] = Form.useForm<Credentials>();
  const [submitting, setSubmitting] = useState(false);
  const setup = mode === 'setup';

  const submit = async (values: Credentials) => {
    setSubmitting(true);
    try {
      await onSubmit(values);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Layout className="min-h-screen bg-[#f5f5f5]">
      <Layout.Header className="!flex !h-16 !items-center !border-b !border-[#f0f0f0] !bg-white !px-6">
        <Typography.Text className="!text-[#1f1f1f]" strong>
          Relist Panel
        </Typography.Text>
      </Layout.Header>
      <Layout.Content className="flex items-center justify-center px-4 py-12">
        <Card className="w-full max-w-[440px]" bordered>
          <div className="mb-8">
            <Typography.Title className="!mb-2" level={2}>
              {setup ? 'Set up your admin account' : 'Sign in to Relist Panel'}
            </Typography.Title>
            <Typography.Text type="secondary">
              {setup
                ? 'Create the account used to manage this host.'
                : 'Use your admin account to continue.'}
            </Typography.Text>
          </div>

          {notice && <Alert className="mb-6" type="success" showIcon message={notice} />}
          {error && <Alert className="mb-6" type="error" showIcon message={error} />}

          <Form form={form} layout="vertical" onFinish={(values) => void submit(values)} requiredMark={false}>
            <Form.Item
              label="Email"
              name="email"
              rules={[
                { required: true, message: 'Enter your email address' },
                { type: 'email', message: 'Enter a valid email address' },
              ]}
            >
              <Input size="large" autoComplete="email" autoFocus />
            </Form.Item>
            <Form.Item
              label="Password"
              name="password"
              rules={[
                { required: true, message: 'Enter your password' },
                ...(setup ? [{ min: 8, message: 'Use at least 8 characters' }] : []),
              ]}
            >
              <Input.Password size="large" autoComplete={setup ? 'new-password' : 'current-password'} />
            </Form.Item>
            {setup && (
              <Form.Item
                label="Confirm password"
                name="confirmPassword"
                dependencies={['password']}
                rules={[
                  { required: true, message: 'Confirm your password' },
                  ({ getFieldValue }) => ({
                    validator(_, value) {
                      if (!value || getFieldValue('password') === value) {
                        return Promise.resolve();
                      }
                      return Promise.reject(new Error('Passwords do not match'));
                    },
                  }),
                ]}
              >
                <Input.Password size="large" autoComplete="new-password" />
              </Form.Item>
            )}
            <Button block htmlType="submit" loading={submitting} size="large" type="primary">
              {setup ? 'Create admin account' : 'Sign in'}
            </Button>
          </Form>
        </Card>
      </Layout.Content>
    </Layout>
  );
}

function AuthenticatedScreen({ onLogout, error }: { onLogout: () => void; error?: string }) {
  return (
    <Layout className="min-h-screen bg-[#f5f5f5]">
      <Layout.Header className="!flex !h-16 !items-center !justify-between !border-b !border-[#f0f0f0] !bg-white !px-6">
        <Typography.Text className="!text-[#1f1f1f]" strong>
          Relist Panel
        </Typography.Text>
        <Button onClick={onLogout}>Log out</Button>
      </Layout.Header>
      <Layout.Content className="flex items-center justify-center px-4 py-12">
        <Card className="w-full max-w-[440px]">
          <Typography.Title className="!mb-2" level={2}>
            Welcome back
          </Typography.Title>
          <Typography.Paragraph type="secondary">
            Your admin session is active. Project management will appear here next.
          </Typography.Paragraph>
          {error && <Alert type="error" showIcon message={error} />}
        </Card>
      </Layout.Content>
    </Layout>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StyleProvider layer>
      <ConfigProvider theme={{ algorithm: theme.defaultAlgorithm }}>
        <App />
      </ConfigProvider>
    </StyleProvider>
  </StrictMode>,
);
