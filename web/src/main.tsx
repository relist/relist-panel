import { StyleProvider } from '@ant-design/cssinjs';
import { Alert, App as AntApp, Button, Card, ConfigProvider, Layout, Spin, Typography } from 'antd';
import { StrictMode, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider } from 'react-router';

import './index.css';
import './monaco';

import { router } from './app/routes';
import { buildTheme } from './app/theme';
import { EmptyState } from './components/primitives';
import { PanelProvider, usePanel } from './data/store';

/**
 * The cutover root (design-port ticket 04): theme, panel store, auth gate and
 * the routed app shell. The monolith's screen tree lived in this file until
 * the cutover; the screens now come from `app/routes.tsx`, and tickets 05–09
 * replace the placeholder pages the routes resolve to today.
 */

/**
 * Entry gate. The panel is single-admin: nothing renders until the auth status
 * settles, and an unauthenticated visitor only ever sees the sign-in screen —
 * at whatever deep link they arrived on, which the router picks up once they
 * are signed in.
 */
function AuthGate({ children }: { children: ReactNode }) {
  const { auth, loadAuthStatus, isPending } = usePanel();
  const [retrying, setRetrying] = useState(false);
  // The provider settles the status once on mount, tracking the request under
  // `auth-status`. "Unchecked with nothing in flight, after having been
  // pending" means that read failed: surface Retry rather than a spinner that
  // never ends (the monolith's error screen, kept).
  const pending = isPending('auth-status');
  const [sawPending, setSawPending] = useState(false);
  useEffect(() => {
    if (pending) {
      setSawPending(true);
    }
  }, [pending]);

  const retry = async () => {
    setRetrying(true);
    try {
      await loadAuthStatus();
    } catch {
      // A failed retry leaves the state unchecked and un-pending; the error
      // card below renders again on its own.
    } finally {
      setRetrying(false);
    }
  };

  if (!auth.checked) {
    if (sawPending && !pending) {
      return (
        <CenteredScreen>
          <Card style={{ width: '100%', maxWidth: 440 }} variant="outlined">
            <Typography.Title level={3}>Relist Panel</Typography.Title>
            <Alert
              action={
                <Button loading={retrying} onClick={() => void retry()}>
                  Retry
                </Button>
              }
              message="Could not contact the panel"
              showIcon
              type="error"
            />
          </Card>
        </CenteredScreen>
      );
    }
    return (
      <CenteredScreen>
        <Spin aria-label="Loading" size="large" />
      </CenteredScreen>
    );
  }

  if (!auth.authenticated) {
    return (
      <CenteredScreen>
        <EmptyState
          description="The panel's setup and sign-in screens are rebuilt by design-port ticket 05; until then, only an existing session opens the panel."
          title="Sign in is not ported yet"
        />
      </CenteredScreen>
    );
  }

  return <>{children}</>;
}

function CenteredScreen({ children }: { children: ReactNode }) {
  return (
    <Layout style={{ alignItems: 'center', display: 'flex', justifyContent: 'center', minHeight: '100vh', padding: 24 }}>
      {children}
    </Layout>
  );
}

function Root() {
  return (
    <StyleProvider layer>
      <ConfigProvider theme={buildTheme('light')}>
        <AntApp>
          <PanelProvider>
            <AuthGate>
              <RouterProvider router={router} />
            </AuthGate>
          </PanelProvider>
        </AntApp>
      </ConfigProvider>
    </StyleProvider>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
);
