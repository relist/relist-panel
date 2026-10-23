import { Alert, Button, Card, Spin, Typography } from 'antd';
import { useEffect, useState } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';

import { ApiError, api } from '../data/api';
import type { MigrationStatus } from '../data/migration';
import { usePanel } from '../data/store';
import { MigrationLockPage } from '../pages/MigrationLockPage';
import { CenteredScreen } from './CenteredScreen';
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
  const lock = useMigrationLock(auth.checked && auth.authenticated, () => {
    void loadAuthStatus().catch(() => {
      // A failed recheck leaves the state as it is; the lock page stays.
    });
  });
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
    return <MigrationLockPage status={lock.status} />;
  }
  if (screen !== 'app') {
    return (
      <Navigate
        replace
        state={{ from: `${location.pathname}${location.search}${location.hash}` }}
        to={paths.signIn}
      />
    );
  }
  return <Outlet />;
}

/**
 * The panel's Migration lock (ADR-0018), read once signed in and every five
 * seconds while it is set, so the lock page follows the old host's steps and
 * leaves once `migration resume` lifts it (ADR-0019). A failed read leaves the
 * app as it is, the API's own 503s still saying why, but keeps a set lock
 * polling: the page has no other way out. A 401 or 403 ends the polling and
 * rechecks the sign-in instead, so an expired session goes to sign-in rather
 * than polling a stale lock forever.
 */
function useMigrationLock(enabled: boolean, signedOut: () => void) {
  const [status, setStatus] = useState<MigrationStatus>();
  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let locked = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = () => {
      api
        .migrationStatus()
        .then((next) => {
          if (stopped) return;
          setStatus(next);
          locked = next.step !== '';
        })
        .catch((reason: unknown) => {
          if (stopped || !(reason instanceof ApiError) || (reason.status !== 401 && reason.status !== 403)) return;
          locked = false;
          signedOut();
        })
        .finally(() => {
          if (!stopped && locked) timer = setTimeout(read, 5000);
        });
    };
    read();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [enabled]);
  return { status };
}
