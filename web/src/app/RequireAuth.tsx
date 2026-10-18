import { Alert, Button, Card, Layout, Spin, Typography } from 'antd';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';

import { api } from '../data/api';
import type { MigrationStatus } from '../data/migration';
import { usePanel } from '../data/store';
import { MigrationLockPage } from '../pages/MigrationLockPage';
import { entryScreen } from './entry';
import { paths } from './paths';

/**
 * The gate around the shell. The panel is single-admin: nothing renders until
 * the auth status settles, and a signed-out visitor is redirected to the entry
 * screen the API implies (setup on first run, sign-in after) — at whatever deep
 * link they arrived on, which sign-in sends them back to.
 */
export function RequireAuth() {
  const { auth, loadAuthStatus, isPending } = usePanel();
  const location = useLocation();
  // The provider settles the status once on mount, tracking the request under
  // `auth-status`. "Unchecked with nothing in flight, after having been
  // pending" means that read failed: surface Retry rather than a spinner that
  // never ends (the monolith's error screen, kept).
  const pending = isPending('auth-status');
  const [sawPending, setSawPending] = useState(false);
  const lock = useMigrationLock(auth.checked && auth.authenticated);
  const setLock = lock.reset;
  useEffect(() => {
    if (pending) {
      setSawPending(true);
    }
  }, [pending]);

  if (!auth.checked) {
    if (sawPending && !pending) {
      return (
        <CenteredScreen>
          <Card style={{ width: '100%', maxWidth: 440 }} variant="outlined">
            <Typography.Title level={3}>Relist Panel</Typography.Title>
            <Alert
              action={
                <Button loading={isPending('auth-status')} onClick={() => {
                  void loadAuthStatus().catch(() => {
                    // A failed retry leaves the state unchecked; the error
                    // card renders again on its own.
                  });
                }}>
                  Retry
                </Button>
              }
              title="Could not contact the panel"
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

  const screen = entryScreen(auth);
  if (screen === 'app' && lock.status && lock.status.step !== '') {
    return <MigrationLockPage onResumed={() => setLock()} status={lock.status} />;
  }
  if (screen !== 'app') {
    return (
      <Navigate
        replace
        state={{ from: `${location.pathname}${location.search}${location.hash}` }}
        to={screen === 'setup' ? paths.setup : paths.signIn}
      />
    );
  }
  return <Outlet />;
}

/**
 * The panel's Migration lock (ADR-0018), read once signed in and every five
 * seconds while it is set, so the lock page follows the old host's steps. A
 * failed read leaves the app as it is: the API's own 503s still say why.
 */
function useMigrationLock(enabled: boolean) {
  const [status, setStatus] = useState<MigrationStatus>();
  const [version, setVersion] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = () => {
      api
        .migrationStatus()
        .then((next) => {
          if (stopped) return;
          setStatus(next);
          if (next.step !== '') timer = setTimeout(read, 5000);
        })
        .catch(() => undefined);
    };
    read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [enabled, version]);
  return { status, reset: () => setVersion((v) => v + 1) };
}

export function CenteredScreen({ children }: { children: ReactNode }) {
  return (
    <Layout style={{ alignItems: 'center', display: 'flex', justifyContent: 'center', minHeight: '100vh', padding: 24 }}>
      {children}
    </Layout>
  );
}
