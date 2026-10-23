import { Alert } from 'antd';
import { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';

import { paths } from '../app/paths';
import { api } from '../data/api';

/**
 * A reminder across the app while the admin's password is still the one the
 * install's .env seeded: few people open the Account page on their own. It
 * checks again on every navigation, so it goes once the password changes.
 */
export function PasswordFromEnvBanner() {
  const location = useLocation();
  const [fromEnv, setFromEnv] = useState(false);

  useEffect(() => {
    let stale = false;
    api
      .account()
      .then((account) => {
        if (!stale) setFromEnv(account.passwordFromEnv);
      })
      .catch(() => undefined);
    return () => {
      stale = true;
    };
  }, [location.pathname]);

  if (!fromEnv) return null;
  return (
    <Alert
      action={<Link to={paths.account}>Change it in Account</Link>}
      showIcon
      style={{ marginBottom: 16 }}
      title="The admin password is still the one in RELIST_ADMIN_PASSWORD"
      type="warning"
    />
  );
}
