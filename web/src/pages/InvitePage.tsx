import { Button, Form, Input, Result, Spin } from 'antd';
import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router';

import { CenteredScreen } from '../app/CenteredScreen';
import { paths } from '../app/paths';
import { ApiError, api } from '../data/api';
import type { IssuedLink } from '../data/api';
import { usePanel } from '../data/store';
import { AuthLayout } from './AuthPages';

type Invite = { email: string; kind: IssuedLink['kind'] };
type Values = { password: string; confirm: string };

/**
 * An invite or reset link's page (`/invite/:token`), outside the sign-in
 * gate: the visitor has no password yet, or has lost it. Setting one signs
 * them in and opens the Overview, or a member's Account. A dead link says
 * only that it is dead, as the API does.
 */
export function InvitePage() {
  const { token = '' } = useParams();
  const navigate = useNavigate();
  const { loadAuthStatus } = usePanel();
  const [form] = Form.useForm<Values>();
  const [invite, setInvite] = useState<Invite>();
  const [invalid, setInvalid] = useState(false);
  const [error, setError] = useState<string>();
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let current = true;
    api
      .getInvite(token)
      .then((found) => current && setInvite(found))
      .catch(() => current && setInvalid(true));
    return () => {
      current = false;
    };
  }, [token]);

  if (invalid) {
    return (
      <CenteredScreen>
        <Result
          extra={
            <Button onClick={() => navigate(paths.signIn)} type="primary">
              Go to sign in
            </Button>
          }
          status="warning"
          subTitle="Ask an admin for a new link."
          title="This link is no longer valid"
        />
      </CenteredScreen>
    );
  }
  if (!invite) {
    return (
      <CenteredScreen>
        <Spin aria-label="Loading" size="large" />
      </CenteredScreen>
    );
  }

  const submit = async ({ password }: Values) => {
    setError(undefined);
    setSubmitting(true);
    try {
      const { isAdmin } = await api.acceptInvite(token, password);
      await loadAuthStatus();
      // A User who is not an Admin has only their Account to open yet.
      navigate(isAdmin ? paths.overview : paths.account, { replace: true });
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 404) {
        setInvalid(true);
      } else {
        setError(reason instanceof Error ? reason.message : 'Could not set the password');
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <AuthLayout
      subtitle="Choose the password you will sign in with."
      title={invite.kind === 'reset' ? 'Reset your password' : 'Set your password'}
    >
      <Form form={form} layout="vertical" onFinish={submit} requiredMark={false}>
        <Form.Item htmlFor="invite-email" label="Email">
          <Input id="invite-email" readOnly value={invite.email} />
        </Form.Item>
        <Form.Item
          help={error}
          label="New password"
          name="password"
          rules={[{ required: true, message: 'Enter a password' }]}
          validateStatus={error ? 'error' : undefined}
        >
          <Input.Password autoComplete="new-password" autoFocus />
        </Form.Item>
        <Form.Item
          dependencies={['password']}
          label="Confirm password"
          name="confirm"
          rules={[
            { required: true, message: 'Enter the password again' },
            ({ getFieldValue }) => ({
              validator: (_, value) =>
                !value || value === getFieldValue('password')
                  ? Promise.resolve()
                  : Promise.reject(new Error('Passwords do not match')),
            }),
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Button block htmlType="submit" loading={submitting} type="primary">
          Set password
        </Button>
      </Form>
    </AuthLayout>
  );
}
